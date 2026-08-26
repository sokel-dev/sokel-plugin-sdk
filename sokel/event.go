// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plugin event triggering (wire protocol §7).
//
// Events are the third self-reported contract, alongside operations and credentials: the plugin
// declares which events it produces and what payload each carries, and a long-running source loop
// pushes external events to the platform with ctx.Trigger to start workflows.
//
// How this differs from operations: an operation is request/reply (the platform calls the plugin);
// an event is fire-and-forget (the plugin pushes to the platform).

const triggerSubject = "sokel.trigger"

// Event is one event type's contract. Fields is the typed contract of its payload, using the same
// Field type and reflection as operation I/O. It is defined in the contract package next to the
// operation contract, shared by the platform and the SDK.
type Event = contract.Event

// Source is one long-running event source (a long poll, a subscription to an upstream system). The
// SDK runs fn in a goroutine of its own.
type Source struct {
	ID    string
	Label string
}

type sourceEntry struct {
	src Source
	fn  func(SourceCtx) error
}

// DeclareEvent declares an event and the contract of its payload. Leaving Fields empty derives them
// from T by reflection, as with an operation's In/Out.
func DeclareEvent[T any](p *Plugin, e Event) {
	if e.Fields == nil {
		e.Fields = deriveFields(reflect.TypeOf(new(T)).Elem())
	}
	if e.Fields == nil {
		e.Fields = []Field{}
	}
	p.events = append(p.events, e)
}

// DeclareEvent implements plugin.EventHost: it declares one event.
func (p *Plugin) DeclareEvent(e contract.Event) { p.events = append(p.events, e) }

// DeclareEventsCommon implements plugin.EventHost: it records the common fields, already validated
// on the contract side.
func (p *Plugin) DeclareEventsCommon(fields []contract.Field, _ []string) {
	p.eventsCommon = fields
}

// RegisterSource registers a long-running event source. p.Run() starts a goroutine for fn, which
// pushes events with ctx.Trigger.
func RegisterSource(p *Plugin, src Source, fn func(SourceCtx) error) {
	p.sources = append(p.sources, sourceEntry{src: src, fn: fn})
}

// eventContract is the event contract reported in the registration handshake.
func (p *Plugin) eventContract() []Event { return p.events }

// DeclareEventsCommon declares the fields every event payload carries (a chat_id, say). On trigger
// the platform flattens them to the top level of the input, so every event branch shares one
// variable.
//
// Call it after all DeclareEvent calls. Validation fails fast rather than silently shrinking the set,
// because a new event that omits a field would otherwise break existing workflows:
//   - each field must exist in **every** declared event with the same type;
//   - it may not collide with a reserved key (_event, event, input, credential_id) or with any event
//     id. credential_id is flattened by the platform: it is the credential that pushed the event, so
//     a downstream node can reply through the same one.
func DeclareEventsCommon(p *Plugin, names ...string) error {
	if len(p.events) == 0 {
		return fmt.Errorf("common fields must be declared after DeclareEvent (there are no events yet)")
	}
	reserved := map[string]bool{"_event": true, "event": true, "input": true, "credential_id": true}
	eventIDs := map[string]bool{}
	for _, e := range p.events {
		eventIDs[e.ID] = true
	}
	var out []Field
	for _, name := range names {
		if reserved[name] {
			return fmt.Errorf("common field %q collides with a reserved key", name)
		}
		if eventIDs[name] {
			return fmt.Errorf("common field %q collides with an event id", name)
		}
		var spec *Field
		for _, e := range p.events {
			var hit *Field
			for i := range e.Fields {
				if e.Fields[i].Name == name {
					hit = &e.Fields[i]
					break
				}
			}
			if hit == nil {
				return fmt.Errorf("common field %q does not exist in the contract of event %q", name, e.ID)
			}
			if spec == nil {
				spec = hit
			} else if spec.Type != hit.Type {
				return fmt.Errorf("common field %q has different types across events (%s vs %s)", name, spec.Type, hit.Type)
			}
		}
		out = append(out, *spec)
	}
	p.eventsCommon = out
	return nil
}

// eventsCommonContract is the common-field contract reported in the registration handshake.
func (p *Plugin) eventsCommonContract() []Field { return p.eventsCommon }

// credEntry is one entry of the registration reply's credentials list: a bot identity assigned to
// this replica.
type credEntry struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

// fieldsSig is a stable signature of the credential fields (sorted k=v), which reconcile uses to
// decide "fields changed -> restart this source instance".
func (c credEntry) fieldsSig() string {
	keys := make([]string, 0, len(c.Fields))
	for k := range c.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(c.Fields[k])
		b.WriteByte('\n')
	}
	return b.String()
}

// desiredSourceCreds turns the reply's credential set into the supervisor's desired set. Empty (a
// plugin with no credentials) becomes one bare instance, so both cases take the same code path.
func desiredSourceCreds(creds []credEntry) []credEntry {
	if len(creds) == 0 {
		return []credEntry{{}}
	}
	return creds
}

// orBare is for logs: an empty credential id prints as "(none)".
func orBare(id string) string {
	if id == "" {
		return "(none)"
	}
	return id
}

// debouncer collapses repeated triggers within a short window into one run: credential-change
// notifications can arrive in bursts, and a bulk edit should re-register once.
type debouncer struct {
	mu    sync.Mutex
	timer *time.Timer
	d     time.Duration
	fn    func()
}

func newDebouncer(d time.Duration, fn func()) *debouncer {
	return &debouncer{d: d, fn: fn}
}

func (b *debouncer) trigger() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(b.d, b.fn)
}

// sourceState is the runtime state of one source × credential instance, reported with each
// registration and heartbeat so the panel can show every bot.
type sourceState struct {
	SourceID     string `json:"source_id"`
	CredentialID string `json:"credential_id,omitempty"`
	Status       string `json:"status"` // running | error | exited | auth_required
	Error        string `json:"error,omitempty"`
	Since        string `json:"since"` // RFC3339, when this state was entered
}

// stateBoard holds the source instances' states, concurrency-safe. The key is source_id ×
// credential_id, and snapshot sorts stably for reporting.
type stateBoard struct {
	mu  sync.Mutex
	m   map[string]sourceState
	now func() string // injectable for tests; in production the current time in RFC3339
}

func newStateBoard() *stateBoard {
	return &stateBoard{m: map[string]sourceState{}, now: func() string { return time.Now().Format(time.RFC3339) }}
}

func (b *stateBoard) set(sourceID, credID, status, errMsg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[sourceID+"|"+credID] = sourceState{SourceID: sourceID, CredentialID: credID, Status: status, Error: errMsg, Since: b.now()}
}

// removeCred drops every source state of a credential whose instance reconcile stopped, so it is no
// longer reported.
func (b *stateBoard) removeCred(credID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, st := range b.m {
		if st.CredentialID == credID {
			delete(b.m, k)
		}
	}
}

func (b *stateBoard) snapshot() []sourceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]sourceState, 0, len(b.m))
	for _, st := range b.m {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].CredentialID < out[j].CredentialID
	})
	return out
}

// sourceSupervisor supervises source instances per credential (many bots, one replica).
//
// Every registration and heartbeat reconciles against the assigned credential set: a new credential
// starts an instance, a removed one is cancelled, and changed fields (a refreshed session, say)
// restart it. start returns that instance's stop function.
type sourceSupervisor struct {
	mu      sync.Mutex
	running map[string]struct {
		stop func()
		sig  string
	}
	start func(credEntry) func()
}

func newSourceSupervisor(start func(credEntry) func()) *sourceSupervisor {
	return &sourceSupervisor{running: map[string]struct {
		stop func()
		sig  string
	}{}, start: start}
}

func (s *sourceSupervisor) reconcile(desired []credEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]credEntry{}
	for _, c := range desired {
		want[c.ID] = c
	}
	// Stop: not in the desired set, or its fields changed (stop then start = restart).
	for id, r := range s.running {
		c, ok := want[id]
		if ok && c.fieldsSig() == r.sig {
			continue
		}
		r.stop()
		delete(s.running, id)
	}
	// Start: desired but not running.
	for id, c := range want {
		if _, ok := s.running[id]; ok {
			continue
		}
		s.running[id] = struct {
			stop func()
			sig  string
		}{stop: s.start(c), sig: c.fieldsSig()}
	}
}

// SourceCtx is a long-running source's context: Trigger pushes events to the platform, Credential
// reads the credential bound to this instance.
//
// Many bots, one replica: each source instance is bound to **one** credential (the supervisor starts
// and stops them to match the assigned set), and ctx.Context is cancelled when that credential is
// removed or changed, so a fn watching ctx.Err() notices and exits.
type SourceCtx struct {
	context.Context
	token    string
	valid    map[string]bool                         // the declared event ids, to catch typos
	publish  func(subject string, data []byte) error // nc.Publish in production; injectable in tests
	cred     map[string]string                       // the fields of the credential bound to this instance
	credID   string                                  // its id: the routing key Trigger carries back automatically
	sourceID string                                  // this source's id, for ReportStatus
	board    *stateBoard                             // the state board reported with the heartbeat; nil in a bare test ctx
	rt       fileRuntime                             // the file runtime for attachments; nil in a bare test ctx
}

// Upload produces a platform file reference, the same as Ctx.Upload on the operation side: a source
// uploads a message attachment (an image, a file, a voice note) back into the platform's file layer
// and puts the reference in the event payload, so a downstream file parameter takes it natively.
// Without a runtime (in tests) it falls back to inline bytes.
func (c SourceCtx) Upload(name, mime string, data []byte) (*File, error) {
	if c.rt == nil {
		return &File{Name: name, Mime: mime, Size: int64(len(data)), Data: data}, nil
	}
	return c.rt.store(c.Context, name, mime, data)
}

// UploadReader streams while reading, as on the operation side: use it when a source moves a large
// file, and memory stays at one chunk.
func (c SourceCtx) UploadReader(name, mime string, r io.Reader) (*File, error) {
	if c.rt == nil {
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return &File{Name: name, Mime: mime, Size: int64(len(b)), Data: b}, nil
	}
	return c.rt.storeReader(c.Context, name, mime, r)
}

// Credential returns the fields of the credential bound to this source instance. It may be empty for
// a plugin without credentials.
func (c SourceCtx) Credential() map[string]string { return c.cred }

// UpdateCredential writes a patch back into the platform credential bound to this instance.
//
// A session-style credential that refreshes while running must write back: the platform is the only
// store, and nothing is persisted locally. The platform authorises by access-group token, so a plugin
// can only touch its own group's credentials. The publish is fire-and-forget.
func (c SourceCtx) UpdateCredential(patch map[string]string) error {
	if c.credID == "" {
		return fmt.Errorf("this source instance has no bound credential to write back to")
	}
	if len(patch) == 0 {
		return nil
	}
	data, err := json.Marshal(map[string]any{"token": c.token, "credential_id": c.credID, "patch": patch})
	if err != nil {
		return err
	}
	return c.publish("sokel.credential.update", data)
}

// ReportStatus lets a source report its own state: on detecting an expired session, call
// ReportStatus("auth_required", …) and the credential and replica rows light up "needs login" with
// the next heartbeat. status is one of running, error, exited, auth_required.
func (c SourceCtx) ReportStatus(status, msg string) {
	if c.board == nil {
		return
	}
	c.board.set(c.sourceID, c.credID, status, msg)
}

type triggerMsg struct {
	Token        string `json:"token"`
	Event        string `json:"event"`
	EventID      string `json:"event_id,omitempty"`      // idempotency key, deduplicated by the platform
	CredentialID string `json:"credential_id,omitempty"` // the bot's credential id; the SDK carries back what registration assigned
	Payload      any    `json:"payload"`
}

// Trigger pushes one event to the platform (fire-and-forget).
//   - event must be an id declared with DeclareEvent;
//   - eventID is the idempotency key; the platform deduplicates on (pluginId, event, eventID). It may
//     be empty;
//   - payload is an object following that event's Fields contract, which downstream nodes reference.
func (c SourceCtx) Trigger(event, eventID string, payload any) error {
	if !c.valid[event] {
		return fmt.Errorf("undeclared event %q — declare it with DeclareEvent first", event)
	}
	// The payload struct becomes a map keyed by sokel tag names, matching the declared Fields so
	// downstream references work. A plain json.Marshal would use Go field names or json tags, which do
	// not match the contract names — hence structToVars, the same path Emitter.Vars takes.
	var out any = payload
	if payload != nil {
		if m := structToVars(payload); m != nil {
			out = m
		}
	}
	data, err := json.Marshal(triggerMsg{Token: c.token, Event: event, EventID: eventID, CredentialID: c.credID, Payload: out})
	if err != nil {
		return fmt.Errorf("serialising event %q: %w", event, err)
	}
	return c.publish(triggerSubject, data)
}

// Compile-time confirmation that sokel is the NATS implementation of the event-side interfaces.
var (
	_ plugin.EventHost = (*Plugin)(nil)
	_ plugin.SourceCtx = SourceCtx{}
)

// Fetch implements plugin.Ctx: a source can read file bytes too, e.g. to re-read an attachment it
// just uploaded.
func (c SourceCtx) Fetch(f *File) ([]byte, error) {
	if c.rt == nil {
		return nil, errors.New("file runtime not ready")
	}
	return c.rt.fetch(c.Context, f)
}
