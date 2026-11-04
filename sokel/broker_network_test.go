// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func brokerDiscoveryServer(t *testing.T, internal string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "enrolled-token",
			"nats": map[string]any{
				"url": "tls://public.example:4222", "internal_url": internal,
				"user": "group-user", "pass": "group-pass", "subject": "sokel.plugin.group",
				"ca": "test-ca-pem", "inbox_prefix": "_INBOX_G.group", "min_protocol": 2,
			},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestBrokerNetworkDiscoveryAndEnrollment(t *testing.T) {
	for _, network := range []string{"", "public", "internal"} {
		t.Run(network, func(t *testing.T) {
			t.Setenv("SOKEL_BROKER_NETWORK", network)
			s := brokerDiscoveryServer(t, "tls://broker.namespace.svc:4222")
			want := "tls://public.example:4222"
			if network == "internal" {
				want = "tls://broker.namespace.svc:4222"
			}
			for _, enroll := range []bool{false, true} {
				var a access
				var err error
				if enroll {
					a, err = enrollAccess(s.URL, "test-token", "test-plugin")
				} else {
					a, err = discoverAccess(s.URL, "test-token")
				}
				if err != nil {
					t.Fatal(err)
				}
				if a.URL != want {
					t.Errorf("enroll=%v: connected to %q, want %q", enroll, a.URL, want)
				}
				if a.User != "group-user" || a.Pass != "group-pass" || a.CA != "test-ca-pem" || a.Subject != "sokel.plugin.group" || a.InboxPrefix != "_INBOX_G.group" || a.MinProtocol != 2 {
					t.Error("selecting a broker changed the access credentials or protocol metadata")
				}
				if enroll && a.Token != "enrolled-token" {
					t.Error("enrollment lost its access token")
				}
			}
		})
	}
}

func TestBrokerNetworkRejectsMissingInvalidOrMultipleInternalURLs(t *testing.T) {
	for _, internal := range []string{"", "https://broker:4222", "nats://", "nats://a:4222,nats://b:4222", "nats://broker:4222/path", "nats://user:secret@broker:4222", "nats://broker:4222?x=1", "nats://broker:0", "nats://broker:65536", "nats://broker:", "nats://broker:word", "nats://broker#", "nats://broker?", "nats://bad host:4222", "nats://[broker]:4222", "nats://2001:db8::1", "nats://2001:db8::1:4222"} {
		t.Run(internal, func(t *testing.T) {
			t.Setenv("SOKEL_BROKER_NETWORK", "internal")
			s := brokerDiscoveryServer(t, internal)
			if _, err := discoverAccess(s.URL, "test-token"); err == nil || !strings.Contains(err.Error(), "internal_url") {
				t.Errorf("invalid internal_url must stop discovery with a clear error, got %v", err)
			}
			if _, err := enrollAccess(s.URL, "test-token", "test-plugin"); err == nil || !strings.Contains(err.Error(), "internal_url") {
				t.Errorf("invalid internal_url must stop enrollment with a clear error, got %v", err)
			}
		})
	}
}

func TestBrokerNetworkOfflineInternalMetadataPinsFinalURL(t *testing.T) {
	t.Setenv("SOKEL_BROKER_NETWORK", "")
	for _, network := range []string{"", "public", "internal", "typo"} {
		t.Run(network, func(t *testing.T) {
			bundle, _ := json.Marshal(map[string]string{
				"url": "tls://selected.internal:4222", "network": network,
				"user": "u", "pass": "p", "token": "skp_offline", "ca": "test-ca",
			})
			t.Setenv("SOKEL_ACCESS", string(bundle))
			a, err := New(Config{}).resolveAccess()
			if network == "typo" {
				if err == nil || !strings.Contains(err.Error(), "network") {
					t.Fatalf("invalid bundle network must fail: %v", err)
				}
				return
			}
			if err != nil || a.URL != "tls://selected.internal:4222" || a.CA != "test-ca" {
				t.Fatalf("bundle selection changed its final URL or credentials: %v", err)
			}
			if got := len(brokerNetworkOptions(a)) > 0; got != (network == "internal") {
				t.Errorf("network %q: internal connection guard enabled=%v", network, got)
			}
		})
	}
	t.Setenv("SOKEL_ACCESS", `{"url":"tls://a:4222,tls://b:4222","network":"internal","user":"u","pass":"p","token":"skp_offline"}`)
	if _, err := New(Config{}).resolveAccess(); err == nil {
		t.Fatal("internal offline bundles must reject multiple destinations")
	}
}

func TestBrokerNetworkRejectsUnknownNetwork(t *testing.T) {
	t.Setenv("SOKEL_BROKER_NETWORK", "internla")
	s := brokerDiscoveryServer(t, "tls://broker:4222")
	if _, err := discoverAccess(s.URL, "test-token"); err == nil || !strings.Contains(err.Error(), "SOKEL_BROKER_NETWORK") {
		t.Fatalf("invalid network must fail explicitly, got %v", err)
	}
}

func TestBrokerNetworkOfflineBundleIsAlreadySelected(t *testing.T) {
	t.Setenv("SOKEL_ACCESS", `{"url":"tls://selected.example:4222","user":"u","pass":"p","token":"skp_offline"}`)
	for _, network := range []string{"", "public", "internal"} {
		t.Run(network, func(t *testing.T) {
			t.Setenv("SOKEL_BROKER_NETWORK", network)
			p := New(Config{})
			a, err := p.resolveAccess()
			if network != "" {
				if err == nil || !strings.Contains(err.Error(), "SOKEL_ACCESS") || !strings.Contains(err.Error(), "SOKEL_BROKER_NETWORK") {
					t.Errorf("a selector must conflict with an already selected bundle, got %v", err)
				}
				return
			}
			if err != nil || a.URL != "tls://selected.example:4222" || p.cfg.Token != "skp_offline" {
				t.Errorf("the existing offline bundle must still work: url=%q err=%v", a.URL, err)
			}
		})
	}
}

func TestBrokerNetworkRediscoveryKeepsInternalSelection(t *testing.T) {
	t.Setenv("SOKEL_BROKER_NETWORK", "internal")
	s := brokerDiscoveryServer(t, "tls://broker.namespace.svc:4222")
	discover := func() (access, error) { return discoverAccess(s.URL, "test-token") }
	if exit, reason := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, discover, "tls://broker.namespace.svc:4222"); exit {
		t.Fatalf("a different public address must not move an internal replica: %s", reason)
	}
	if exit, _ := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, discover, "tls://old-internal:4222"); !exit {
		t.Fatal("a changed internal address must be rediscovered")
	}
}

func TestBrokerNetworkOldPlatformPublicAndMissingInternal(t *testing.T) {
	s := brokerDiscoveryServer(t, "")
	for _, network := range []string{"", "public"} {
		t.Setenv("SOKEL_BROKER_NETWORK", network)
		a, err := discoverAccess(s.URL, "test-token")
		if err != nil || a.URL != "tls://public.example:4222" || len(brokerNetworkOptions(a)) != 0 {
			t.Fatalf("default and public must work with old platforms: url=%q err=%v", a.URL, err)
		}
	}
	t.Setenv("SOKEL_BROKER_NETWORK", "internal")
	t.Setenv("SOKEL_ACCESS", "")
	t.Setenv("SOKEL_DEPLOY_KEY", "test-token")
	p := New(Config{Endpoint: s.URL, Name: "test-plugin"})
	if _, err := p.resolveAccess(); err == nil || !strings.Contains(err.Error(), "internal_url") {
		t.Fatalf("missing internal_url must stop enrollment without retrying: %v", err)
	}
	if exit, reason := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute,
		func() (access, error) { return discoverAccess(s.URL, "test-token") }, "tls://old:4222"); !exit || !strings.Contains(reason, "internal_url") {
		t.Fatalf("rediscovery must surface a removed internal address: exit=%v reason=%s", exit, reason)
	}
}

func TestBrokerNetworkInternalAddressDefaultsAndIPv6(t *testing.T) {
	for raw, want := range map[string]string{
		"nats://broker":            "broker:4222",
		"tls://broker:4333":        "broker:4333",
		"nats://[::1]":             "[::1]:4222",
		"tls://[2001:db8::1]:4333": "[2001:db8::1]:4333",
	} {
		got, err := internalBrokerAddress(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", raw, got, err, want)
		}
	}
}

// A small NATS protocol peer deliberately advertises another address. The real nats.go client
// learns it, so these tests exercise actual dial/reconnect behavior rather than inspecting options.
type networkTestPeer struct {
	listener net.Listener
	accepted atomic.Int32
	mu       sync.Mutex
	conns    []net.Conn
	closed   bool
}

func startNetworkTestPeer(t *testing.T, advertised string) *networkTestPeer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &networkTestPeer{listener: l}
	t.Cleanup(p.stop)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			p.mu.Lock()
			if p.closed {
				_ = c.Close()
				p.mu.Unlock()
				continue
			}
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go func() {
				defer c.Close()
				info, _ := json.Marshal(map[string]any{"server_id": "network-test", "version": "2.10.0", "proto": 1, "max_payload": 1048576, "connect_urls": []string{advertised}})
				if _, err := fmt.Fprintf(c, "INFO %s\r\n", info); err != nil {
					return
				}
				scan := bufio.NewScanner(c)
				for scan.Scan() {
					if scan.Text() == "PING" {
						if _, err := fmt.Fprint(c, "PONG\r\n"); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	return p
}

func (p *networkTestPeer) dropConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *networkTestPeer) stop() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.listener.Close()
	p.dropConnections()
}

func TestBrokerNetworkInternalReconnectNeverDialsAdvertisedPublic(t *testing.T) {
	public := startNetworkTestPeer(t, "127.0.0.1:1")
	internal := startNetworkTestPeer(t, public.listener.Addr().String())
	// Use a hostname, not an IP: the dial guard must run before DNS resolution.
	_, port, _ := net.SplitHostPort(internal.listener.Addr().String())
	a, err := selectBrokerNetwork(access{InternalURL: "nats://localhost:" + port}, "internal")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	reconnected := make(chan struct{}, 1)
	opts := append(brokerNetworkOptions(a), nats.DontRandomize(), nats.ReconnectWait(10*time.Millisecond), nats.MaxReconnects(2),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
		nats.ReconnectHandler(func(*nats.Conn) { reconnected <- struct{}{} }))
	nc, err := nats.Connect(a.URL, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if len(nc.DiscoveredServers()) == 0 {
		t.Fatal("the fixture must advertise a real public fallback")
	}
	internal.dropConnections()
	select {
	case <-reconnected:
		if !strings.Contains(nc.ConnectedUrl(), "localhost:"+port) {
			t.Fatal("reconnected outside the selected internal endpoint")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not reconnect to the internal endpoint")
	}
	internal.stop()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the finite test reconnect budget did not expire; a public fallback may have connected")
	}
	if got := public.accepted.Load(); got != 0 {
		t.Fatalf("the public endpoint received %d connections", got)
	}
	if got := internal.accepted.Load(); got < 2 {
		t.Fatalf("expected an initial connection and an internal reconnect, got %d", got)
	}
}
