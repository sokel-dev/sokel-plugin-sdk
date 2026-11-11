// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestNATSConnectionRecoversAfterWriteFailure(t *testing.T) {
	for _, cause := range []error{os.ErrDeadlineExceeded, syscall.EPIPE} {
		t.Run(cause.Error(), func(t *testing.T) {
			nc, dialer := recoveryConnection(t)
			first := <-dialer.connections
			calls := make(chan string, 1)
			if _, err := nc.Subscribe("plugin.calls", func(m *nats.Msg) { calls <- string(m.Data) }); err != nil {
				t.Fatal(err)
			}
			if err := nc.FlushTimeout(time.Second); err != nil {
				t.Fatal(err)
			}
			first.failure.Store(&recoveryWriteFailure{cause: cause})
			if err := nc.Publish("heartbeat", []byte("renew")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-dialer.connections:
			case <-time.After(2 * time.Second):
				t.Fatal("socket write failed, but the SDK did not reconnect; waiting for the default stale timer leaves the replica unavailable for minutes")
			}
			// Reconnect resends subscriptions before pending messages. A request also proves the reply inbox recovered.
			msg, err := nc.Request("register", []byte("renewed"), 2*time.Second)
			if err != nil || string(msg.Data) != "renewed" {
				t.Fatalf("renewal did not recover: message=%v error=%v", msg, err)
			}
			// An old asynchronous error must not replace a socket that has already recovered.
			nc.ErrorHandler()(nc, nil, &net.OpError{Op: "write", Net: "tcp", Err: cause})
			if dialer.dials.Load() != 2 || !nc.IsConnected() {
				t.Fatal("a delayed error from the failed socket replaced the recovered connection")
			}
			if err := nc.Publish("plugin.calls", []byte("served")); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-calls:
				if got != "served" {
					t.Fatalf("unexpected call after reconnect: %q", got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("operation subscription was not restored after reconnect")
			}
		})
	}
}

func TestNATSConnectionBoundsStalledWrites(t *testing.T) {
	nc, dialer := recoveryConnection(t)
	first := <-dialer.connections
	first.failure.Store(&recoveryWriteFailure{stall: true})
	if err := nc.Publish("heartbeat", []byte("renew")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialer.connections:
	case <-time.After(8 * time.Second):
		t.Fatal("stalled socket write did not trigger reconnect within the heartbeat request budget")
	}
	if _, err := nc.Request("register", []byte("renewed"), 2*time.Second); err != nil {
		t.Fatalf("renewal failed after stalled-write recovery: %v", err)
	}
}

func TestNATSConnectionKeepsIdleSocketAlive(t *testing.T) {
	_, dialer := recoveryConnection(t)
	first := <-dialer.connections
	<-first.pings // The connection handshake's PING is not a periodic keepalive.
	select {
	case <-first.pings:
	case <-time.After(15 * time.Second):
		t.Fatal("idle socket received no keepalive before a third of the platform's 45-second lease elapsed")
	}
}

func TestNATSConnectionRecoversWhenPongsStop(t *testing.T) {
	_, dialer := recoveryConnection(t, nats.PingInterval(20*time.Millisecond))
	first := <-dialer.connections
	first.dropPongs.Store(true)
	select {
	case <-dialer.connections:
	case <-time.After(time.Second):
		t.Fatal("connection with no PONG responses did not recover")
	}
}

func TestNATSConnectionKeepsSocketOnRequestTimeout(t *testing.T) {
	nc, dialer := recoveryConnection(t)
	first := <-dialer.connections
	if _, err := nc.Request("no.reply", nil, 20*time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("expected a request timeout, got %v", err)
	}
	if _, err := nc.Request("register", []byte("renewed"), time.Second); err != nil {
		t.Fatalf("connection was not usable after a request timeout: %v", err)
	}
	if dialer.dials.Load() != 1 || first.closed.Load() {
		t.Fatal("a slow platform reply caused the SDK to replace a healthy socket")
	}
}

func TestNATSConnectionKeepsSocketOnPermissionViolation(t *testing.T) {
	nc, dialer := recoveryConnection(t)
	first := <-dialer.connections
	handler := nc.ErrorHandler()
	observed := make(chan error, 1)
	nc.SetErrorHandler(func(c *nats.Conn, sub *nats.Subscription, err error) {
		handler(c, sub, err)
		observed <- err
	})
	if err := nc.Publish("permission.denied", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-observed:
		if !errors.Is(err, nats.ErrPermissionViolation) {
			t.Fatalf("expected a permission violation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("permission violation was not reported")
	}
	if _, err := nc.Request("register", []byte("renewed"), time.Second); err != nil {
		t.Fatalf("connection was not usable after a permission violation: %v", err)
	}
	if dialer.dials.Load() != 1 || first.closed.Load() {
		t.Fatal("a permission violation caused the SDK to replace a healthy socket")
	}
}

func recoveryConnection(t *testing.T, overrides ...nats.Option) (*nats.Conn, *recoveryDialer) {
	t.Helper()
	var violation atomic.Value
	violation.Store("")
	d := &recoveryDialer{connections: make(chan *recoveryConn, 8)}
	opts := natsConnectionOptions("recovery-test", access{}, &violation)
	opts = append(opts, nats.SetCustomDialer(d), nats.ReconnectWait(time.Millisecond), nats.ReconnectJitter(0, 0))
	opts = append(opts, overrides...)
	nc, err := nats.Connect("nats://127.0.0.1:4222", opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc, d
}

type recoveryWriteFailure struct {
	cause error
	stall bool
}

type recoveryConn struct {
	net.Conn
	failure   atomic.Pointer[recoveryWriteFailure]
	dropPongs atomic.Bool
	closed    atomic.Bool
	pings     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	deadline  time.Time // SetWriteDeadline and Write are serialized by the NATS writer.
}

func (c *recoveryConn) SetWriteDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.Conn.SetWriteDeadline(deadline)
}

func (c *recoveryConn) Write(p []byte) (int, error) {
	if f := c.failure.Load(); len(p) > 0 && f != nil {
		if f.stall {
			timer := time.NewTimer(time.Until(c.deadline))
			defer timer.Stop()
			select {
			case <-timer.C:
				return 0, &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded}
			case <-c.done:
				return 0, net.ErrClosed
			}
		}
		return 0, &net.OpError{Op: "write", Net: "tcp", Err: f.cause}
	}
	return c.Conn.Write(p)
}

func (c *recoveryConn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
	})
	return c.Conn.Close()
}

type recoveryDialer struct {
	connections chan *recoveryConn
	dials       atomic.Int32
}

func (d *recoveryDialer) Dial(_, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	c := &recoveryConn{Conn: client, pings: make(chan struct{}, 16), done: make(chan struct{})}
	d.dials.Add(1)
	d.connections <- c
	go c.serve(server)
	return c, nil
}

// The broker stub speaks only the core protocol needed to verify socket recovery, reply inboxes and subscriptions.
func (c *recoveryConn) serve(server net.Conn) {
	// net.Pipe has no TCP buffers. Keep broker reads independent from its writes so a
	// PONG never blocks reading the next request while the client's writer holds its lock.
	writes := make(chan string, 16)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for frame := range writes {
			if _, err := io.WriteString(server, frame); err != nil {
				server.Close()
				return
			}
		}
	}()
	defer func() {
		close(writes)
		server.Close()
		<-writerDone
	}()
	send := func(frame string) error {
		select {
		case writes <- frame:
			return nil
		case <-writerDone:
			return io.ErrClosedPipe
		}
	}
	if err := send("INFO {\"server_id\":\"recovery-test\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576}\r\n"); err != nil {
		return
	}
	r := bufio.NewReader(server)
	subs := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "PING":
			c.pings <- struct{}{}
			if !c.dropPongs.Load() {
				if err := send("PONG\r\n"); err != nil {
					return
				}
			}
		case "SUB":
			subs[parts[1]] = parts[len(parts)-1]
		case "PUB":
			size, err := strconv.Atoi(parts[len(parts)-1])
			if err != nil {
				return
			}
			body := make([]byte, size+2)
			if _, err := io.ReadFull(r, body); err != nil {
				return
			}
			if parts[1] == "no.reply" {
				continue
			}
			if parts[1] == "permission.denied" {
				if err := send("-ERR 'Permissions Violation for Publish to \"permission.denied\"'\r\n"); err != nil {
					return
				}
				continue
			}
			subject := parts[1]
			if len(parts) == 4 {
				subject = parts[2]
			}
			for pattern, sid := range subs {
				if pattern == subject || strings.HasSuffix(pattern, "*") && strings.HasPrefix(subject, strings.TrimSuffix(pattern, "*")) {
					if err := send(fmt.Sprintf("MSG %s %s %d\r\n%s", subject, sid, size, body)); err != nil {
						return
					}
				}
			}
		}
	}
}
