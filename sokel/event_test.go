// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

type msgEvent struct {
	ChatID int64  `sokel:"chat_id" label:"Chat ID"`
	Text   string `sokel:"text" label:"Text"`
	Raw    any    `sokel:"raw" label:"Raw event"`
}

// DeclareEvent[T] derives an event contract by reflecting over the payload struct, using the same
// deriveFields as operation I/O.
func TestDeclareEventDerivesFields(t *testing.T) {
	p := New(Config{Token: "skp_x", Name: "t"})
	DeclareEvent[msgEvent](p, Event{ID: "message", Label: "Message received"})

	evs := p.eventContract()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event contract, got %d", len(evs))
	}
	e := evs[0]
	if e.ID != "message" || e.Label != "Message received" {
		t.Errorf("wrong event id or label: %+v", e)
	}
	names := map[string]string{}
	for _, f := range e.Fields {
		names[f.Name] = string(f.Type)
	}
	if names["chat_id"] != "number" || names["text"] != "string" || names["raw"] != "json" {
		t.Errorf("the payload field contract was reflected wrongly: %+v", e.Fields)
	}
}

// Explicit Fields are not overwritten by reflection, as with an Operation's Inputs and Outputs.
func TestDeclareEventExplicitFields(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "e", Fields: []Field{{Name: "only", Type: TString}}})
	evs := p.eventContract()
	if len(evs[0].Fields) != 1 || evs[0].Fields[0].Name != "only" {
		t.Errorf("explicit Fields should be kept as they are: %+v", evs[0].Fields)
	}
}

// Trigger produces the §7 push message {token, event, event_id, payload} and publishes it to
// sokel.trigger.
func TestSourceTriggerWireShape(t *testing.T) {
	var gotSubject string
	var gotData []byte
	sc := SourceCtx{
		token:   "skp_abc",
		valid:   map[string]bool{"message": true},
		publish: func(subject string, data []byte) error { gotSubject = subject; gotData = data; return nil },
	}
	if err := sc.Trigger("message", "12345", msgEvent{ChatID: 7, Text: "hi"}); err != nil {
		t.Fatalf("Trigger failed: %v", err)
	}
	if gotSubject != "sokel.trigger" {
		t.Errorf("the subject should be sokel.trigger, got %q", gotSubject)
	}
	var m struct {
		Token   string          `json:"token"`
		Event   string          `json:"event"`
		EventID string          `json:"event_id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gotData, &m); err != nil {
		t.Fatalf("the message is not valid JSON: %v", err)
	}
	if m.Token != "skp_abc" || m.Event != "message" || m.EventID != "12345" {
		t.Errorf("wrong message header: %+v", m)
	}
	var pl map[string]any
	_ = json.Unmarshal(m.Payload, &pl)
	if pl["chat_id"] != float64(7) || pl["text"] != "hi" {
		t.Errorf("wrong payload: %v", pl)
	}
}

// SourceCtx.Credential exposes the credential bound to this source instance — a bot_token, say — which
// event sources need; since v1.3 each instance binds exactly one.
func TestSourceCtxCredential(t *testing.T) {
	sc := SourceCtx{cred: map[string]string{"bot_token": "123:ABC"}, credID: "cred_a"}
	if sc.Credential()["bot_token"] != "123:ABC" {
		t.Errorf("Credential should return the bound credential: %v", sc.Credential())
	}
	// Unbound gives a nil map, and reading a key yields an empty string rather than a panic.
	empty := SourceCtx{}
	if empty.Credential()["bot_token"] != "" {
		t.Error("reading a key with no credential should give an empty string")
	}
}

// Triggering an undeclared event fails, catching typos, rather than publishing silently.
func TestSourceTriggerRejectsUndeclared(t *testing.T) {
	called := false
	sc := SourceCtx{
		token:   "skp_x",
		valid:   map[string]bool{"message": true},
		publish: func(string, []byte) error { called = true; return nil },
	}
	if err := sc.Trigger("callback_query", "1", nil); err == nil {
		t.Error("an undeclared event should fail")
	}
	if called {
		t.Error("an undeclared event should not be published")
	}
}

type cbEvent struct {
	ChatID int64  `sokel:"chat_id" label:"Chat ID"`
	Data   string `sokel:"callback_data"`
}

type noChatEvent struct {
	Data string `sokel:"data"`
}

// Declaring common fields: the fields every event payload shares (chat_id, say) are reported as the
// events_common contract, and on trigger the platform flattens them to the top level of the input
// ({{node.chat_id}}) so every branch shares one variable.
func TestDeclareEventsCommon(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "message"})
	DeclareEvent[cbEvent](p, Event{ID: "callback_query"})
	if err := DeclareEventsCommon(p, "chat_id"); err != nil {
		t.Fatalf("a valid common-field declaration should not fail: %v", err)
	}
	fs := p.eventsCommonContract()
	if len(fs) != 1 || fs[0].Name != "chat_id" || fs[0].Type != TNumber {
		t.Errorf("wrong common-field contract: %+v", fs)
	}
}

// Validation: an event missing the field, a type mismatch, or a collision with a reserved key or an
// event id all fail fast rather than shrinking the set silently.
func TestDeclareEventsCommonValidation(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "message"})
	DeclareEvent[noChatEvent](p, Event{ID: "bare"})
	if err := DeclareEventsCommon(p, "chat_id"); err == nil {
		t.Error("the bare event lacks chat_id and should fail")
	}

	p2 := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p2, Event{ID: "message"})
	DeclareEvent[cbEvent](p2, Event{ID: "callback_query"})
	if err := DeclareEventsCommon(p2, "_event"); err == nil {
		t.Error("the reserved key _event should fail")
	}
	if err := DeclareEventsCommon(p2, "message"); err == nil {
		t.Error("colliding with an event id should fail")
	}
	if err := DeclareEventsCommon(p2, "nope"); err == nil {
		t.Error("a non-existent field should fail")
	}

	p3 := New(Config{Token: "skp_x"})
	if err := DeclareEventsCommon(p3, "chat_id"); err == nil {
		t.Error("declaring no events at all should fail")
	}
}

// The per-credential source supervisor (many bots in one instance, wire protocol v1.3): reconcile
// starts, stops and restarts source instances against the desired credential set — a new credential
// starts one, a removed one is cancelled, and a field change restarts it.
func TestSourceSupervisorReconcile(t *testing.T) {
	started := []string{}
	canceled := map[string]int{}
	sup := newSourceSupervisor(func(c credEntry) func() {
		started = append(started, c.ID+":"+c.Fields["k"])
		id := c.ID
		return func() { canceled[id]++ }
	})

	// To begin with, two credentials start two instances.
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}, {ID: "b", Fields: map[string]string{"k": "2"}}})
	if len(started) != 2 {
		t.Fatalf("2 instances should start, got %v", started)
	}
	// Idempotent: reconciling the same set again changes nothing.
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}, {ID: "b", Fields: map[string]string{"k": "2"}}})
	if len(started) != 2 || len(canceled) != 0 {
		t.Fatalf("the same set should start and stop nothing, started=%v canceled=%v", started, canceled)
	}
	// b is removed, by a sharding change or a disabled credential, so b is cancelled.
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}})
	if canceled["b"] != 1 {
		t.Fatalf("b should be stopped, canceled=%v", canceled)
	}
	// a's fields change, a refreshed session for instance, so it restarts: cancel the old, start the new.
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "9"}}})
	if canceled["a"] != 1 || started[len(started)-1] != "a:9" {
		t.Fatalf("a should restart, canceled=%v started=%v", canceled, started)
	}
}

// The source instance status board: one running state per source and credential, reported with the
// registration and the heartbeat, so the panel can show every bot.
func TestStateBoard(t *testing.T) {
	b := newStateBoard()
	b.set("updates", "cred_a", "running", "")
	b.set("updates", "cred_b", "running", "")
	b.set("updates", "cred_b", "error", "getUpdates 401")
	ss := b.snapshot()
	if len(ss) != 2 {
		t.Fatalf("expected 2 statuses, got %v", ss)
	}
	// The order is stable (source_id then credential_id), and an upsert overwrites the same key.
	if ss[0].CredentialID != "cred_a" || ss[0].Status != "running" {
		t.Errorf("cred_a should be running: %+v", ss[0])
	}
	if ss[1].CredentialID != "cred_b" || ss[1].Status != "error" || ss[1].Error != "getUpdates 401" {
		t.Errorf("cred_b should be in error: %+v", ss[1])
	}
	// When reconcile stops a credential's instance, all of its source statuses are removed.
	b.removeCred("cred_b")
	if ss := b.snapshot(); len(ss) != 1 || ss[0].CredentialID != "cred_a" {
		t.Errorf("cred_b's status should be removed: %+v", ss)
	}
}

// SourceCtx.UpdateCredential is the write-back channel: it publishes sokel.credential.update with
// {token, credential_id (this instance's bound credential), patch}, so a session-style credential
// refreshed while running is persisted back on the platform.
func TestSourceCtxUpdateCredential(t *testing.T) {
	var gotSubject string
	var gotData []byte
	sc := SourceCtx{
		token:   "skp_x",
		credID:  "cred_a",
		publish: func(subject string, data []byte) error { gotSubject = subject; gotData = data; return nil },
	}
	if err := sc.UpdateCredential(map[string]string{"session": "s2"}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	if gotSubject != "sokel.credential.update" {
		t.Errorf("subject: %q", gotSubject)
	}
	var m struct {
		Token        string            `json:"token"`
		CredentialID string            `json:"credential_id"`
		Patch        map[string]string `json:"patch"`
	}
	_ = json.Unmarshal(gotData, &m)
	if m.Token != "skp_x" || m.CredentialID != "cred_a" || m.Patch["session"] != "s2" {
		t.Errorf("wrong message: %+v", m)
	}
	// With no bound credential a bare instance fails instead of sending: there is nothing to write back
	// to.
	bare := SourceCtx{token: "skp_x", publish: func(string, []byte) error { t.Error("nothing should be published"); return nil }}
	if err := bare.UpdateCredential(map[string]string{"k": "v"}); err == nil {
		t.Error("no credential should fail")
	}
}

// SourceCtx.ReportStatus lets a source report its own state — an expired session becoming
// auth_required — writing to the status board, which the heartbeat carries.
func TestSourceCtxReportStatus(t *testing.T) {
	b := newStateBoard()
	sc := SourceCtx{credID: "cred_a", sourceID: "updates", board: b}
	sc.ReportStatus("auth_required", "the session expired; scan the code again")
	ss := b.snapshot()
	if len(ss) != 1 || ss[0].Status != "auth_required" || ss[0].CredentialID != "cred_a" {
		t.Errorf("the status board should record auth_required: %+v", ss)
	}
	// With no board — a bare ctx injected by a test — it must not panic.
	SourceCtx{}.ReportStatus("running", "")
}

// SourceCtx.Upload uploads an event attachment, falling back to inline bytes when there is no runtime,
// with the same semantics as Ctx.Upload on the operation side.
func TestSourceCtxUploadFallback(t *testing.T) {
	f, err := SourceCtx{}.Upload("a.png", "image/png", []byte{1, 2, 3})
	if err != nil || f.Name != "a.png" || f.Size != 3 || len(f.Data) != 3 {
		t.Errorf("with no runtime it should fall back to inline: %+v %v", f, err)
	}
}

// Notification debouncing: several triggers within a short window run once, so a batch of credential
// changes re-registers only once.
func TestDebouncer(t *testing.T) {
	var n atomic.Int32
	d := newDebouncer(30*time.Millisecond, func() { n.Add(1) })
	d.trigger()
	d.trigger()
	d.trigger()
	time.Sleep(80 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Errorf("three triggers should collapse into one run, got %d", got)
	}
	d.trigger()
	time.Sleep(80 * time.Millisecond)
	if got := n.Load(); got != 2 {
		t.Errorf("a trigger after the window should run again, got %d", got)
	}
}
