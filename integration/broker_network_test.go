// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Run each replica in a process so its normal SIGTERM shutdown also gets exercised, without
// leaving transport goroutines behind or changing the network environment of another replica.
func TestBrokerNetworkPluginProcess(t *testing.T) {
	if os.Getenv("SOKEL_TEST_BROKER_PROCESS") != "1" {
		return
	}
	if err := newPlugin(os.Getenv("SOKEL_ENDPOINT")).Run(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerNetworkPublicAndInternalTLSReachSameBroker(t *testing.T) {
	cert, ca, roots := brokerTestCertificate(t)
	p := startPlatformWithBroker(t, 2, &natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}, []nats.Option{nats.Secure(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})}, ca)

	for _, network := range []string{"public", "internal", "offline-internal"} {
		t.Run(network, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrokerNetworkPluginProcess$")
			selector, bundle := network, ""
			if network == "offline-internal" {
				selector = ""
				raw, _ := json.Marshal(map[string]any{
					"url": p.internalURL, "network": "internal", "token": "skp_it", "user": "unused", "pass": "unused",
					"ca": ca, "subject": subject, "inbox_prefix": inboxPrefix, "min_protocol": 2,
				})
				bundle = string(raw)
			}
			cmd.Env = append(os.Environ(), "SOKEL_TEST_BROKER_PROCESS=1", "SOKEL_ENDPOINT="+p.http.URL,
				"SOKEL_BROKER_NETWORK="+selector, "SOKEL_ACCESS="+bundle, "SOKEL_INSTANCE_ID=it-"+network)
			var logs bytes.Buffer
			cmd.Stdout, cmd.Stderr = &logs, &logs
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			waited := false
			defer func() {
				cancel()
				if !waited {
					<-done
				}
			}()
			select {
			case <-p.regs:
			case err := <-done:
				waited = true
				t.Fatalf("replica stopped before registering: %v\n%s", err, logs.String())
			case <-ctx.Done():
				t.Fatal("replica did not register over TLS")
			}
			call, _ := json.Marshal(map[string]any{"operation": "roundtrip", "input": map[string]any{"text": network}, "file_ticket": "tk_network"})
			var msg *nats.Msg
			var err error
			for i := 0; i < 50; i++ {
				msg, err = p.nc.Request(subject, call, time.Second)
				if !errors.Is(err, nats.ErrNoResponders) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("file roundtrip: %v", err)
			}
			var result struct{ Back, Error string }
			if err := json.Unmarshal(msg.Data, &result); err != nil || result.Error != "" || result.Back != network {
				t.Fatalf("file roundtrip failed: %s (%v)", msg.Data, err)
			}
			connections, err := p.broker.Connz(nil)
			if err != nil {
				t.Fatal(err)
			}
			var oldID uint64
			for _, connection := range connections.Conns {
				if connection.Name == "it" {
					oldID = connection.Cid
					if connection.TLSVersion == "" {
						t.Fatal("replica connected without TLS")
					}
				}
			}
			if oldID == 0 {
				t.Fatal("replica missing from the broker")
			}
			if err := p.broker.DisconnectClientByID(oldID); err != nil {
				t.Fatal(err)
			}
			reconnected := false
			for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
				connections, err = p.broker.Connz(nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, connection := range connections.Conns {
					if connection.Name == "it" && connection.Cid != oldID && connection.NumSubs > 0 && connection.TLSVersion != "" {
						reconnected = true
					}
				}
				if reconnected {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !reconnected {
				t.Fatal("replica did not reconnect over TLS")
			}
			msg, err = p.nc.Request(subject, call, time.Second)
			if err != nil {
				t.Fatalf("file roundtrip after reconnect: %v", err)
			}
			if err := json.Unmarshal(msg.Data, &result); err != nil || result.Error != "" || result.Back != network {
				t.Fatalf("file roundtrip after reconnect failed: %s (%v)", msg.Data, err)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				waited = true
				if err != nil {
					t.Fatalf("replica shutdown: %v\n%s", err, logs.String())
				}
			case <-ctx.Done():
				t.Fatal("replica did not shut down")
			}
			wantURL := p.brokerURL
			if network != "public" {
				wantURL = p.internalURL
			}
			if !strings.Contains(logs.String(), "reconnected to the platform: "+wantURL) {
				t.Fatalf("replica did not reconnect to its selected URL %s:\n%s", wantURL, logs.String())
			}
		})
	}
}

func brokerTestCertificate(t *testing.T) (tls.Certificate, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "broker integration"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	return cert, string(ca), roots
}
