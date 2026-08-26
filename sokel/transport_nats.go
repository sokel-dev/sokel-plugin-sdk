// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

// natsTransport is the outbound NATS deployment: the plugin dials in with its token, registers by
// reporting the contract, subscribes to the subject the platform assigns, then per call binds the
// input, runs the handler and replies in frames. Non-streaming uses request-reply; streaming
// publishes each frame to the reply subject and finishes with a terminator.
type natsTransport struct{}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

var processStart = time.Now().UTC().Format(time.RFC3339)

// registerBody is the registration handshake payload.
//
// It is a separate function so it can be tested: **declared but never reported** is the classic
// silent failure of a self-reporting mechanism — everything looks fine on the plugin side, nothing
// happens on the platform side, and the author is left staring at an inert UI. (auth_flow fell into
// exactly that on the day it shipped: WithAuth was written, and this function was missing one line.)
func (p *Plugin) registerBody(instanceID, host string, ops []Operation) map[string]any {
	return map[string]any{
		"token": p.cfg.Token, "instance_id": instanceID, "host": host,
		// The process start time: registration and every heartbeat resend **the same value**, and a new
		// process gets a new one. That is how the platform tells "a replica came back up" from "the old
		// one is still alive" when instance_id and host are identical.
		"started_at": processStart,
		"region":     pluginenv.Get("REGION"), // optional deployment region label shown in the replica list
		// Version, in order: SetVersion in code > the SOKEL_VERSION environment variable (the easiest
		// place to inject when building a release image) > "sdk-go". It used to be hard-coded to sdk-go,
		// which made the replica list's version column useless; together with started_at it now answers
		// "is what is running out there the build I just shipped?".
		"version":   firstNonEmpty(p.version, pluginenv.Get("VERSION"), "sdk-go"),
		"transport": string(NATS), "operations": ops,
		"managed":           p.managed,                // the token came from deployment-level enrollment
		"credential_schema": p.credFields,             // the credential contract, for display only
		"oauth":             p.oauth,                  // declares the credential is obtained through OAuth
		"auth_flow":         p.authFlow,               // the collaborative auth flow; the panel adds a login button from it
		"events":            p.eventContract(),        // the event contracts
		"events_common":     p.eventsCommonContract(), // fields every event carries, flattened on trigger
		"capabilities":      p.capabilitiesContract(), // how far each optional capability goes
		"doc":               p.doc,                    // the user-facing markdown
		"doc_url":           p.docURL,                 // a link instead, when a doc site already exists
	}
}

func (natsTransport) run(p *Plugin) error {
	// One endpoint: an https platform URL is resolved to the real transport address through
	// /connect-info, while a literal nats:// skips discovery.
	target, err := discoverNATS(p.cfg.Endpoint, p.cfg.Token)
	if err != nil {
		return err
	}
	// Connection auth prefers SOKEL_NATS_TOKEN, the broker's shared transport secret, and falls back
	// to the access token (a broker without auth ignores it either way). The access token still
	// establishes identity inside the registration payload.
	natsToken := pluginenv.Get("NATS_TOKEN")
	if natsToken == "" {
		natsToken = p.cfg.Token
	}
	// RetryOnFailedConnect: a broker that is not up yet does not abort startup, the plugin waits.
	// Disconnects reconnect forever and subscriptions restore themselves.
	opts := []nats.Option{
		nats.Token(natsToken), nats.Name(p.cfg.Name), nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true), nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, derr error) {
			log.Printf("[sokel] disconnected from the platform: %v (reconnecting)", derr)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Printf("[sokel] reconnected to the platform: %s", c.ConnectedUrl()) }),
	}
	if ca := pluginenv.Get("NATS_CA"); ca != "" { // custom CA for a tls:// broker outside the system trust store
		opts = append(opts, nats.RootCAs(ca))
	}
	nc, err := nats.Connect(target, opts...)
	if err != nil {
		return fmt.Errorf("connecting to the platform at %q: %w", target, err)
	}
	defer nc.Drain()

	// Zero-touch enrollment: with no access token but a deployment-level key (SOKEL_DEPLOY_KEY), the
	// plugin exchanges "plugin name + key" for its default group's real token over sokel.enroll. A
	// first-party container that ships with the deployment needs no hand-copied access command. After
	// the exchange the flow is identical to a manually configured one.
	//
	// This path requires a literal nats:// endpoint (which is what such containers have): discovery
	// through an https endpoint needs an access token itself, so that would be circular. When the
	// platform has not enabled enrollment nobody answers, and the same 8s retry as registration keeps
	// the symptom visible rather than silent.
	if p.cfg.Token == "" {
		if key := pluginenv.Get("DEPLOY_KEY"); key != "" {
			payload, _ := json.Marshal(map[string]string{"plugin": p.cfg.Name, "key": key})
			for {
				resp, rerr := nc.Request("sokel.enroll", payload, 8*time.Second)
				if rerr == nil {
					var er struct {
						OK    bool   `json:"ok"`
						Token string `json:"token"`
						Error string `json:"error"`
					}
					_ = json.Unmarshal(resp.Data, &er)
					if er.OK && er.Token != "" {
						p.cfg.Token = er.Token
						p.managed = true // reported at registration so the replica list shows its origin
						log.Printf("[sokel] enrolled (deploy key -> default group token)")
						break
					}
					log.Printf("[sokel] enrollment refused (%s), retrying in 8s…", er.Error)
				} else {
					log.Printf("[sokel] enrollment request failed (%v), retrying in 8s…", rerr)
				}
				time.Sleep(8 * time.Second)
			}
		}
	}

	host, _ := os.Hostname()
	instanceID := stableInstanceID(p.cfg.Token) // stable across restarts, so registration does not mint a new replica each time
	ops := p.contract()

	board := newStateBoard() // source runtime state: written by source goroutines, reported with each registration
	var notifySubject string // the group-broadcast subject for credential-change notifications
	register := func() (subject, name string, creds []credEntry, err error) {
		body := p.registerBody(instanceID, host, ops)
		if len(p.sources) > 0 {
			body["source_states"] = board.snapshot() // per source × credential, so the panel can show every bot
		}
		payload, _ := json.Marshal(body)
		resp, rerr := nc.Request("sokel.register", payload, 8*time.Second)
		if rerr != nil {
			return "", "", nil, rerr
		}
		var reg struct {
			OK            bool              `json:"ok"`
			Name          string            `json:"name"`
			Subject       string            `json:"subject"`
			NotifySubject string            `json:"notify_subject"` // credential-change notifications, broadcast to the group
			Error         string            `json:"error"`
			Credentials   []credEntry       `json:"credentials"`   // the credential subset assigned to this replica
			Credential    map[string]string `json:"credential"`    // older platforms: a single credential
			CredentialID  string            `json:"credential_id"` // its id, the event routing key
		}
		_ = json.Unmarshal(resp.Data, &reg)
		if !reg.OK || reg.Subject == "" {
			return "", "", nil, fmt.Errorf("registration refused: %s", reg.Error)
		}
		if reg.NotifySubject != "" {
			notifySubject = reg.NotifySubject
		}
		// Prefer the plural credential set (sharded assignment); an older platform sends only the
		// singular form, folded into a one-element set.
		creds = reg.Credentials
		if len(creds) == 0 && (reg.CredentialID != "" || len(reg.Credential) > 0) {
			creds = []credEntry{{ID: reg.CredentialID, Fields: reg.Credential}}
		}
		return reg.Subject, reg.Name, creds, nil
	}

	// The first registration does not abort on failure (the broker or the platform may still be
	// starting); it retries at a fixed interval until it succeeds. Together with reconnect-and-resume
	// and heartbeat re-registration, that is the whole self-healing story.
	subject, name, creds, err := register()
	for err != nil {
		log.Printf("[sokel] registration failed (%v), retrying in 8s…", err)
		time.Sleep(8 * time.Second)
		subject, name, creds, err = register()
	}

	rt := natsFiles{nc: nc, token: p.cfg.Token} // file bytes travel in chunks over this same connection
	// QueueSubscribe: replicas of a group share one queue, so each call reaches exactly one of them.
	// A plain Subscribe was used once — every call was broadcast, every replica executed it (duplicating
	// POST-style side effects!), and whoever answered first won.
	if _, err := nc.QueueSubscribe(subject, "sokel-workers", func(m *nats.Msg) {
		p.dispatchNATS(nc, m, rt, instanceID)
	}); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}
	log.Printf("[sokel] connected: plugin %q ready, replica %s listening on %s", name, instanceID, subject)

	// Long-running sources, many bots on one replica: a per-credential supervisor. Every registration
	// and heartbeat carries the credential subset assigned to this replica (sharded across the online
	// ones), and the supervisor reconciles: each credential gets its own source goroutines (with a
	// SourceCtx bound to it, so Trigger carries that credential_id back); a credential removed or
	// sharded elsewhere is cancelled (fn notices through ctx.Err()); changed fields, such as a
	// refreshed session, restart it. A plugin without credentials runs one bare instance. Several
	// replicas therefore give both horizontal scaling across bots and automatic failover.
	var supervisor *sourceSupervisor
	if len(p.sources) > 0 {
		valid := map[string]bool{}
		for _, e := range p.events {
			valid[e.ID] = true
		}
		supervisor = newSourceSupervisor(func(c credEntry) func() {
			ctx, cancel := context.WithCancel(context.Background())
			for _, se := range p.sources {
				se := se
				// One ctx per source (carrying sourceID, the board and the file runtime): ReportStatus
				// lands on the right source × credential entry, and rt uploads event attachments so a
				// platform file reference ends up in the payload.
				sctx := SourceCtx{Context: ctx, token: p.cfg.Token, valid: valid, publish: nc.Publish, cred: c.Fields, credID: c.ID, sourceID: se.src.ID, board: board, rt: rt}
				go func() {
					log.Printf("[sokel] event source %q started (credential=%s)", se.src.ID, orBare(c.ID))
					board.set(se.src.ID, c.ID, "running", "")
					err := se.fn(sctx)
					if ctx.Err() != nil {
						board.removeCred(c.ID) // stopped by reconcile: drop the state wholesale
						return
					}
					if err != nil {
						log.Printf("[sokel] event source %q exited (credential=%s): %v", se.src.ID, orBare(c.ID), err)
						board.set(se.src.ID, c.ID, "error", err.Error())
					} else {
						board.set(se.src.ID, c.ID, "exited", "")
					}
				}()
			}
			return cancel
		})
		supervisor.reconcile(desiredSourceCreds(creds))

		// Credential-change notifications: the platform broadcasts to the group whenever a credential is
		// added, edited, removed, or has a scanned session written into it. Receiving one debounces a
		// re-register plus reconcile, so a new bot comes up within seconds and a deleted one stops within
		// seconds (the 20-40s heartbeat is only the fallback). It is a plain subscription rather than a
		// queue group: every replica must hear it, since any assignment may have changed.
		if notifySubject != "" {
			deb := newDebouncer(300*time.Millisecond, func() {
				if _, _, ncreds, rerr := register(); rerr == nil {
					supervisor.reconcile(desiredSourceCreds(ncreds))
				} else {
					log.Printf("[sokel] re-register after a credential change failed: %v", rerr)
				}
			})
			if _, serr := nc.Subscribe(notifySubject, func(*nats.Msg) { deb.trigger() }); serr != nil {
				log.Printf("[sokel] subscribing to credential-change notifications failed: %v", serr)
			}
		}
	}

	// The heartbeat keeps the replica online. SIGINT/SIGTERM (docker stop, Ctrl-C) shuts down
	// gracefully: the platform marks it offline within seconds instead of waiting out the heartbeat
	// sweep (45s+). A crash still falls back to that timeout.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if _, _, hbCreds, err := register(); err != nil {
				log.Printf("[sokel] heartbeat renewal failed: %v", err)
			} else if supervisor != nil {
				// Reconcile against the latest assignment each beat: shard moves, credentials added or
				// removed, fields refreshed.
				supervisor.reconcile(desiredSourceCreds(hbCreds))
			}
		case sig := <-quit:
			bye, _ := json.Marshal(map[string]any{"token": p.cfg.Token, "instance_id": instanceID})
			_ = nc.Publish("sokel.unregister", bye)
			_ = nc.Flush() // make sure the goodbye lands before the connection drops
			log.Printf("[sokel] got %v, told the platform we are going offline, exiting", sig)
			return nil // the deferred Drain finishes up
		}
	}
}

// dispatchNATS handles one call.
func (p *Plugin) dispatchNATS(nc *nats.Conn, m *nats.Msg, rt fileRuntime, instanceID string) {
	if m.Reply == "" {
		return
	}
	var call struct {
		Operation    string            `json:"operation"`
		Input        json.RawMessage   `json:"input"`
		Credential   map[string]string `json:"credential"`    // resolved credential fields, for the plugin's own upstream calls
		CredentialID string            `json:"credential_id"` // the credential id: the webhook frame's routing key
		Trace        map[string]string `json:"trace"`         // tracing context (run_id/workflow_id/node_id), for logs
	}
	_ = json.Unmarshal(m.Data, &call)
	tag := traceTag(call.Trace)
	// The platform-relayed webhook frame is intercepted before dispatch. An older SDK without this
	// branch falls through to "unknown operation", which the platform translates into "the plugin
	// registered no webhook handler".
	if call.Operation == "__webhook__" {
		log.Printf("[sokel] ← webhook inbound%s", tag)
		valid := map[string]bool{}
		for _, e := range p.events {
			valid[e.ID] = true
		}
		sctx := SourceCtx{Context: context.Background(), token: p.cfg.Token, valid: valid,
			publish: nc.Publish, cred: call.Credential, credID: call.CredentialID, sourceID: "webhook", rt: rt}
		_ = m.Respond(p.handleWebhookFrame(sctx, call.Input))
		return
	}
	entry := p.find(call.Operation)
	if entry == nil && len(p.ops) == 1 {
		entry = &p.ops[0] // single-operation plugin: a missing `operation` means the only one
	}
	if entry == nil {
		log.Printf("[sokel] ✗ unknown operation %q%s", call.Operation, tag)
		_ = m.Respond([]byte(fmt.Sprintf(`{"error":"unknown operation %q"}`, call.Operation)))
		return
	}
	op := entry.op.ID
	log.Printf("[sokel] ← %s started%s", op, tag)
	start := time.Now()
	// Trace goes into the context so a plugin can read sokel.TraceValue(ctx, "run_id"). A sending
	// plugin derives its idempotency key from it, so however many times one node execution retries, it
	// is still the same message.
	ctx := natsCtx{Context: context.WithValue(context.Background(), traceCtxKey{}, call.Trace), rt: rt, cred: call.Credential}

	if entry.op.Stream {
		// Streaming: publish each frame to the reply subject, then the terminator.
		sink := &natsStreamSink{nc: nc, reply: m.Reply, instance: instanceID}
		if err := entry.invoke(ctx, call.Input, sink); err != nil {
			log.Printf("[sokel] ✗ %s failed (%s)%s: %v", op, time.Since(start).Round(time.Millisecond), tag, err)
			b, _ := json.Marshal(frame{Kind: "error", Text: err.Error()})
			_ = nc.PublishMsg(msgWithInstance(m.Reply, instanceID, b))
		} else {
			log.Printf("[sokel] ✓ %s done (%s)%s", op, time.Since(start).Round(time.Millisecond), tag)
		}
		_ = nc.PublishMsg(msgWithInstance(m.Reply, instanceID, []byte(`{"kind":"end"}`)))
		return
	}

	// Non-streaming: buffer the frames and merge the variables into one reply, which is the node's
	// output object.
	sink := &bufferSink{}
	if err := entry.invoke(ctx, call.Input, sink); err != nil {
		log.Printf("[sokel] ✗ %s failed (%s)%s: %v", op, time.Since(start).Round(time.Millisecond), tag, err)
		_ = m.RespondMsg(msgWithInstance(m.Reply, instanceID, []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))))
		return
	}
	log.Printf("[sokel] ✓ %s done (%s)%s", op, time.Since(start).Round(time.Millisecond), tag)
	reply, _ := json.Marshal(sink.vars)
	_ = m.RespondMsg(msgWithInstance(m.Reply, instanceID, reply)) // report which replica answered
}

// instanceHeader names the header that reports which replica answered, on replies and stream frames.
// An older platform ignores it and an older SDK omits it, so both directions degrade gracefully.
const instanceHeader = "Sokel-Instance"

func msgWithInstance(subject, instanceID string, data []byte) *nats.Msg {
	msg := nats.NewMsg(subject)
	msg.Header.Set(instanceHeader, instanceID)
	msg.Data = data
	return msg
}

// traceTag turns the tracing context into a log suffix " [run=… wf=… node=…]", empty when there is none.
func traceTag(t map[string]string) string {
	if len(t) == 0 {
		return ""
	}
	s := ""
	for _, k := range []struct{ key, label string }{{"run_id", "run"}, {"workflow_id", "wf"}, {"node_id", "node"}, {"trace_id", "tr"}} {
		if v := t[k.key]; v != "" {
			if s != "" {
				s += " "
			}
			s += k.label + "=" + v
		}
	}
	if s == "" {
		return ""
	}
	return " [" + s + "]"
}

// bufferSink is the non-streaming sink: it merges every variables frame (a later frame overwrites
// same-named fields) and ignores text/json, which exist for streaming display only.
type bufferSink struct{ vars map[string]any }

func (s *bufferSink) emit(f frame) {
	if f.Kind != frameVars {
		return
	}
	if s.vars == nil {
		s.vars = map[string]any{}
	}
	for k, v := range f.Vars {
		s.vars[k] = v
	}
}

// natsStreamSink is the streaming sink: each frame is published to the reply subject as one message.
type natsStreamSink struct {
	nc       *nats.Conn
	reply    string
	instance string // every frame's header reports which replica produced it
}

func (s *natsStreamSink) emit(f frame) {
	b, _ := json.Marshal(f)
	_ = s.nc.PublishMsg(msgWithInstance(s.reply, s.instance, b))
}
