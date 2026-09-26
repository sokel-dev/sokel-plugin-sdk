// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package integration runs a real plugin (sokel.Run) against an embedded NATS server and a minimal
// platform stub: discovery, registration, a call that uploads and reads back a file, a streaming call,
// and the wire-protocol floor.
//
// It lives in its own module so the NATS server it needs does not become a dependency of the SDK.
// The platform side here is deliberately small and written from docs/plugin-wire-protocol.md of the
// platform repository; what it pins is the SDK's side of that protocol. Changes such as the per-group
// reply prefix or the call file ticket used to be checked only by hand against a running platform.
package integration

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

const (
	subject     = "sokel.plugin.it.g1"
	inboxPrefix = "_INBOX_G.g1"
)

// platform is the stub: it answers connect-info, registration and the file channel, and records what
// the SDK sent so the test can check it.
type platform struct {
	t           *testing.T
	nc          *nats.Conn
	http        *httptest.Server
	minProtocol int

	mu       sync.Mutex
	register map[string]any
	uploads  map[string][]byte // upload_id -> bytes so far
	files    map[string][]byte // file id -> bytes
	tickets  []string          // tickets seen on file.put / file.get
	regs     chan struct{}
}

func startPlatform(t *testing.T, minProtocol int) *platform {
	t.Helper()
	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	p := &platform{t: t, nc: nc, minProtocol: minProtocol,
		uploads: map[string][]byte{}, files: map[string][]byte{}, regs: make(chan struct{}, 16)}

	p.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/connect-info" || r.Header.Get("Authorization") != "Bearer skp_it" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"nats": map[string]any{
			"url": ns.ClientURL(), "subject": subject, "inbox_prefix": inboxPrefix, "min_protocol": p.minProtocol,
		}})
	}))
	t.Cleanup(p.http.Close)

	sub := func(subj string, h nats.MsgHandler) {
		if _, err := nc.Subscribe(subj, h); err != nil {
			t.Fatal(err)
		}
	}
	sub("sokel.register", func(m *nats.Msg) {
		var body map[string]any
		_ = json.Unmarshal(m.Data, &body)
		p.mu.Lock()
		p.register = body
		p.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"ok": true, "name": "it", "subject": subject})
		_ = m.Respond(b)
		select {
		case p.regs <- struct{}{}:
		default:
		}
	})
	sub("sokel.file.put", func(m *nats.Msg) {
		var req struct {
			UploadID, Name, Mime, Data, Ticket string
			Seq                                int
			Last                               bool
		}
		_ = json.Unmarshal(m.Data, &req)
		b, _ := base64.StdEncoding.DecodeString(req.Data)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.tickets = append(p.tickets, req.Ticket)
		id := req.UploadID
		if id == "" {
			id = fmt.Sprintf("up_%d", len(p.uploads)+1)
		}
		p.uploads[id] = append(p.uploads[id], b...)
		resp := map[string]any{"upload_id": id}
		if req.Last {
			fid := "f_" + id
			p.files[fid] = p.uploads[id]
			resp["file"] = map[string]any{"id": fid, "name": req.Name, "mime": req.Mime, "size": len(p.files[fid]), "url": "http://files/" + fid}
		}
		out, _ := json.Marshal(resp)
		_ = m.Respond(out)
	})
	sub("sokel.file.get", func(m *nats.Msg) {
		var req struct {
			ID, Ticket string
			Seq        int
		}
		_ = json.Unmarshal(m.Data, &req)
		p.mu.Lock()
		p.tickets = append(p.tickets, req.Ticket)
		data, ok := p.files[req.ID]
		p.mu.Unlock()
		resp := map[string]any{"data": base64.StdEncoding.EncodeToString(data), "last": true}
		if !ok {
			resp = map[string]any{"error": "no such file"}
		}
		out, _ := json.Marshal(resp)
		_ = m.Respond(out)
	})
	return p
}

type roundtripIn struct {
	Text string `json:"text" sokel:"text"`
}
type roundtripOut struct {
	File *plugin.File `json:"file" sokel:"file"`
	Back string       `json:"back" sokel:"back"`
}
type streamOut struct {
	N int `json:"n" sokel:"n"`
}

func newPlugin(endpoint string) *sokel.Plugin {
	p := sokel.New(sokel.Config{Endpoint: endpoint, Token: "skp_it", Name: "it"})
	f := func(name, typ string) contract.Field {
		return contract.Field{Name: name, Label: name, Type: contract.ParamType(typ)}
	}
	p.Register(contract.Operation{ID: "roundtrip", Label: "roundtrip",
		Inputs: []contract.Field{f("text", "string")}, Outputs: []contract.Field{f("file", "file"), f("back", "string")}},
		func(ctx plugin.Ctx, raw json.RawMessage, out plugin.Sink) error {
			var in roundtripIn
			_ = json.Unmarshal(raw, &in)
			up, err := ctx.Upload("it.txt", "text/plain", []byte(in.Text))
			if err != nil {
				return fmt.Errorf("upload: %w", err)
			}
			b, err := ctx.Fetch(up)
			if err != nil {
				return fmt.Errorf("fetch: %w", err)
			}
			out.Vars(&roundtripOut{File: up, Back: string(b)})
			return nil
		})
	p.Register(contract.Operation{ID: "streamy", Label: "streamy", Stream: true, Outputs: []contract.Field{f("n", "number")}},
		func(ctx plugin.Ctx, raw json.RawMessage, out plugin.Sink) error {
			for i := 0; i < 3; i++ {
				out.Text(fmt.Sprintf("chunk%d", i))
			}
			out.Vars(&streamOut{N: 3})
			return nil
		})
	return p
}

func TestPluginEndToEnd(t *testing.T) {
	pf := startPlatform(t, 2)
	go func() { _ = newPlugin(pf.http.URL).Run() }() // Run blocks; the test binary exits with it

	select {
	case <-pf.regs:
	case <-time.After(15 * time.Second):
		t.Fatal("the plugin never registered")
	}
	pf.mu.Lock()
	reg := pf.register
	pf.mu.Unlock()
	if reg["protocol"] != float64(2) || !strings.HasPrefix(fmt.Sprint(reg["sdk"]), "go/") {
		t.Errorf("registration must report protocol 2 and the SDK: protocol=%v sdk=%v", reg["protocol"], reg["sdk"])
	}

	// A call that uploads a file and reads it back, carrying the call's file ticket both ways.
	call, _ := json.Marshal(map[string]any{"operation": "roundtrip", "input": map[string]any{"text": "hello"}, "file_ticket": "tk_call"})
	// The plugin subscribes to its subject right after the registration reply; a real platform only
	// calls a replica once it shows up in the replica list, so wait for the subscription the same way.
	var msg *nats.Msg
	var err error
	for i := 0; i < 50; i++ {
		if msg, err = pf.nc.Request(subject, call, 10*time.Second); !errors.Is(err, nats.ErrNoResponders) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var res struct {
		Back  string          `json:"back"`
		File  json.RawMessage `json:"file"`
		Error string          `json:"error"`
	}
	_ = json.Unmarshal(msg.Data, &res)
	if res.Error != "" || res.Back != "hello" || len(res.File) == 0 {
		t.Fatalf("round trip went wrong: %s", msg.Data)
	}
	pf.mu.Lock()
	for _, tk := range pf.tickets {
		if tk != "tk_call" {
			t.Errorf("a file transfer did not carry the call's ticket: %q (all: %v)", tk, pf.tickets)
		}
	}
	pf.mu.Unlock()

	// A streaming call: frames arrive one by one on the reply subject, ending with an end frame.
	inbox := nats.NewInbox()
	frames, _ := pf.nc.SubscribeSync(inbox)
	scall, _ := json.Marshal(map[string]any{"operation": "streamy", "input": map[string]any{}})
	if err := pf.nc.PublishRequest(subject, inbox, scall); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for {
		m, err := frames.NextMsg(10 * time.Second)
		if err != nil {
			t.Fatalf("stream stopped after %v: %v", kinds, err)
		}
		var fr struct{ Kind string }
		_ = json.Unmarshal(m.Data, &fr)
		kinds = append(kinds, fr.Kind)
		if fr.Kind == "end" {
			break
		}
	}
	if got := strings.Join(kinds, ","); got != "text,text,text,variables,end" {
		t.Errorf("stream frames = %s", got)
	}
}

// A platform that asks for a newer wire protocol stops the plugin with ErrSDKTooOld, before it ever
// connects, instead of letting it retry registration forever.
func TestRunStopsWhenThePlatformNeedsANewerProtocol(t *testing.T) {
	pf := startPlatform(t, 99)
	done := make(chan error, 1)
	go func() { done <- newPlugin(pf.http.URL).Run() }()
	select {
	case err := <-done:
		if !errors.Is(err, sokel.ErrSDKTooOld) {
			t.Fatalf("Run returned %v, want ErrSDKTooOld", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run kept going against a platform that requires a newer protocol")
	}
}
