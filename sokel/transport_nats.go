// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"errors"
	"strings"
	"sync/atomic"
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
		// place to inject when building a release image) > empty. Empty means "not declared", and the
		// platform shows it as unknown. It must NOT fall back to a junk sentinel (it once sent "sdk-go"):
		// the platform records the self-reported version as the plugin's installed-version fact, and a
		// non-empty junk string pollutes that fact and lights a permanent "update available" badge —
		// unknown is not the same as outdated.
		"version":   firstNonEmpty(p.version, pluginenv.Get("VERSION")),
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
		"protocol":          wireProtocol,             // the wire protocol this SDK speaks; the platform refuses one that is too old
		"sdk":               sdkIdent(),               // which SDK, for the replica list and the refusal message
	}
}

func (natsTransport) run(p *Plugin) error {
	// Everything needed to reach the broker comes from the platform over HTTP, BEFORE connecting:
	// the address, this group's own credentials, and the subject to listen on.
	//
	// Enrollment moved here too. It used to happen over the broker (sokel.enroll) after connecting,
	// which only worked while one shared secret opened the whole broker -- now that the broker
	// authorizes per group, asking it for credentials would require already having them.
	acc, err := p.resolveAccess()
	if err != nil {
		return err
	}
	if err := checkProtocol(acc); err != nil {
		return err
	}
	target := acc.URL
	// RetryOnFailedConnect: a broker that is not up yet does not abort startup, the plugin waits.
	// Disconnects reconnect forever and subscriptions restore themselves.
	// The broker reports a permissions violation asynchronously; the request that depended on it just times
	// out. Remember the last one so that timeout can say what really happened (see explainTimeout).
	var violation atomic.Value
	violation.Store("")
	lastViolation := func() string { v, _ := violation.Load().(string); return v }
	opts := []nats.Option{
		nats.UserInfo(acc.User, acc.Pass), nats.Name(p.cfg.Name), nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true), nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, derr error) {
			log.Printf("[sokel] disconnected from the platform: %v (reconnecting)", derr)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Printf("[sokel] reconnected to the platform: %s", c.ConnectedUrl()) }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, aerr error) {
			if errors.Is(aerr, nats.ErrPermissionViolation) {
				violation.Store(aerr.Error())
				log.Printf("[sokel] %v — %s", aerr, violationHint)
				return
			}
			log.Printf("[sokel] nats error: %v", aerr)
		}),
	}
	if acc.InboxPrefix != "" {
		opts = append(opts, nats.CustomInboxPrefix(acc.InboxPrefix))
	}
	// Trusting the broker's certificate, in order of preference:
	//   1. the CA the platform handed us with the credentials -- nothing to configure;
	//   2. SOKEL_NATS_CA, a local file, for setups that pin it themselves.
	// A publicly trusted certificate needs neither.
	if acc.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(acc.CA)) {
			return fmt.Errorf("the CA the platform sent is not valid PEM")
		}
		opts = append(opts, nats.Secure(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}))
	} else if ca := pluginenv.Get("NATS_CA"); ca != "" {
		opts = append(opts, nats.RootCAs(ca))
	}
	nc, err := nats.Connect(target, opts...)
	if err != nil {
		return fmt.Errorf("connecting to the platform at %q: %w", target, err)
	}
	defer nc.Drain()

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
			return "", "", nil, explainTimeout(rerr, lastViolation())
		}
		var reg struct {
			OK            bool              `json:"ok"`
			Name          string            `json:"name"`
			Subject       string            `json:"subject"`
			NotifySubject string            `json:"notify_subject"` // credential-change notifications, broadcast to the group
			Error         string            `json:"error"`
			Code          string            `json:"code"`          // sdk_too_old: rebuilding with a newer SDK is the only fix
			Credentials   []credEntry       `json:"credentials"`   // the credential subset assigned to this replica
			Credential    map[string]string `json:"credential"`    // older platforms: a single credential
			CredentialID  string            `json:"credential_id"` // its id, the event routing key
		}
		_ = json.Unmarshal(resp.Data, &reg)
		if reg.Code == "sdk_too_old" {
			return "", "", nil, fmt.Errorf("%w: %s", ErrSDKTooOld, reg.Error)
		}
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
		if errors.Is(err, ErrSDKTooOld) {
			return err // waiting does not fix this; say so and stop
		}
		log.Printf("[sokel] registration failed (%v), retrying in 8s…", err)
		time.Sleep(8 * time.Second)
		subject, name, creds, err = register()
	}

	rt := natsFiles{nc: nc, token: p.cfg.Token, violation: lastViolation} // file bytes travel in chunks over this same connection
	// QueueSubscribe: replicas of a group share one queue, so each call reaches exactly one of them.
	// A plain Subscribe was used once — every call was broadcast, every replica executed it (duplicating
	// POST-style side effects!), and whoever answered first won.
	//
	// **Each call runs in its own goroutine.** nats.go delivers to an async subscription from a single
	// goroutine per subscription (go nc.waitForMsgs(sub)) and calls the handler serially, so dispatching
	// inline made one replica serve exactly one call at a time: a 38-second PDF parse left that replica
	// mute to everything else — health checks included — and the platform reported "plugin did not
	// respond … context deadline exceeded" intermittently, curing itself the moment the parse finished
	// (reported 2026-09-23). Queueing behind a slow call is not the platform's fault to fix; it is here.
	//
	// The semaphore is acquired **on the dispatcher goroutine**, on purpose: that is the backpressure.
	// Past the limit the subscription's pending buffer holds the rest, rather than this process spawning
	// an unbounded number of goroutines each holding a document in memory.
	limit := dispatchConcurrency()
	if _, err := nc.QueueSubscribe(subject, "sokel-workers", concurrentHandler(limit, func(m *nats.Msg) {
		p.dispatchNATS(nc, m, rt, instanceID)
	})); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}
	if limit > 0 {
		log.Printf("[sokel] dispatch concurrency capped at %d (SOKEL_MAX_CONCURRENCY; 1 = serial)", limit)
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
	// Broker-move watchdog: MaxReconnects(-1) redials the address we were born with, forever.
	// If the broker moved (embedded broker restarted elsewhere, operator switch), that loyalty
	// orphans the replica for good. Track how long we have been disconnected; past the threshold
	// re-run discovery and exit with a reason when it points elsewhere -- the supervisor restart
	// reconnects us onto the fresh address (see rediscoverOutcome for the full decision table).
	// Only meaningful when the endpoint went through discovery; a literal nats:// has no better
	// source of truth to consult.
	var disconnectedSince time.Time
	// Offline bundles cannot rediscover (there is no platform to ask): on prolonged disconnects
	// keep retrying the broker address in hand, same as the legacy nats:// endpoints.
	rediscoverable := !strings.HasPrefix(strings.TrimSpace(p.cfg.Endpoint), "nats://") &&
		pluginenv.Get("ACCESS") == ""
	for {
		select {
		case <-tick.C:
			if rediscoverable {
				if st := nc.Status(); st != nats.CONNECTED {
					if disconnectedSince.IsZero() {
						disconnectedSince = time.Now()
					}
					if exit, reason := rediscoverOutcome(st, time.Since(disconnectedSince), time.Minute,
						func() (access, error) { return discoverAccess(p.cfg.Endpoint, p.cfg.Token) }, target); exit {
						return errors.New(reason)
					}
				} else {
					disconnectedSince = time.Time{}
				}
			}
			if _, _, hbCreds, err := register(); err != nil {
				log.Printf("[sokel] heartbeat renewal failed: %v", err)
			} else if supervisor != nil {
				// Reconcile against the latest assignment each beat: shard moves, credentials added or
				// removed, fields refreshed.
				supervisor.reconcile(desiredSourceCreds(hbCreds))
			}
		case sig := <-quit:
			// started_at identifies the sender: after a redeploy under the same instance id, the OLD
			// process's goodbye must not knock the replacement offline (the platform compares this
			// against the row it holds and ignores a mismatch; older platforms just ignore the field).
			bye, _ := json.Marshal(map[string]any{"token": p.cfg.Token, "instance_id": instanceID, "started_at": processStart})
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
		FileTicket   string            `json:"file_ticket"`   // the call's ticket: file transfers made while handling it carry it back
		DeadlineMS   int               `json:"deadline_ms"`   // how long the platform waits; the handler's ctx ends then
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
		sctx := SourceCtx{Context: withFileTicket(context.Background(), call.FileTicket), token: p.cfg.Token, valid: valid,
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
	base, cancel := callContext(context.Background(), call.DeadlineMS)
	defer cancel()
	ctx := natsCtx{Context: withFileTicket(context.WithValue(base, traceCtxKey{}, call.Trace), call.FileTicket), rt: rt, cred: call.Credential}

	if entry.op.Stream {
		// Streaming: publish each frame to the reply subject, then the terminator.
		sink := &natsStreamSink{nc: nc, reply: m.Reply, instance: instanceID}
		if err := entry.invoke(ctx, call.Input, sink); err != nil {
			log.Printf("[sokel] ✗ %s failed (%s)%s: %v", op, time.Since(start).Round(time.Millisecond), tag, err)
			b, _ := json.Marshal(errorFrame(err))
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
		_ = m.RespondMsg(msgWithInstance(m.Reply, instanceID, errorReply(err)))
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

// resolveAccess gets the transport address and this group's credentials from the platform.
//
// Two ways in, both over HTTP and both before any broker connection:
//
//	an access token       -> /connect-info
//	a deployment key      -> /plugins/enroll, which also mints the access token
//
// Enrollment retries forever with the same 8s cadence registration uses: a container that ships
// with the deployment may well start before the platform is ready, and a visible retry loop beats
// exiting into a crash loop that says nothing.
func (p *Plugin) resolveAccess() (access, error) {
	// Offline access (SOKEL_ACCESS): the full connection bundle exported from the platform UI,
	// for topologies where the replica cannot reach the platform's HTTP endpoint at all --
	// platform on a private network, broker published on a public one, replica somewhere third.
	// Registration, heartbeats and contract self-reports all travel over the broker, so once
	// connected nothing else needs the platform URL. The bundle carries this group's own broker
	// credentials: treat it exactly like the access token it contains.
	if raw := pluginenv.Get("ACCESS"); raw != "" {
		var acc access
		if err := json.Unmarshal([]byte(raw), &acc); err != nil {
			return access{}, fmt.Errorf("SOKEL_ACCESS is not valid JSON: %w", err)
		}
		if acc.URL == "" || acc.User == "" || acc.Pass == "" || acc.AccessToken == "" {
			return access{}, fmt.Errorf("SOKEL_ACCESS is missing url/user/pass/token -- export it again from the platform's connect dialog")
		}
		p.cfg.Token = acc.AccessToken
		acc.Token = acc.AccessToken
		return acc, nil
	}
	if p.cfg.Token != "" {
		return discoverAccess(p.cfg.Endpoint, p.cfg.Token)
	}
	key := pluginenv.Get("DEPLOY_KEY")
	if key == "" {
		return access{}, fmt.Errorf("no SOKEL_TOKEN and no SOKEL_DEPLOY_KEY: " +
			"set the access token from the plugin's access group, or a deployment key for zero-touch enrollment")
	}
	for {
		acc, err := enrollAccess(p.cfg.Endpoint, key, p.cfg.Name)
		if err == nil {
			p.cfg.Token = acc.Token
			p.managed = true // reported at registration so the replica list shows its origin
			log.Printf("[sokel] enrolled (deploy key -> access token + broker credentials)")
			return acc, nil
		}
		log.Printf("[sokel] enrollment failed (%v), retrying in 8s…", err)
		time.Sleep(8 * time.Second)
	}
}

// concurrentHandler wraps a dispatch function so each message runs in its own goroutine.
//
// limit <= 0 means no cap, and that is the default: the platform already meters calls on its side
// (per-channel concurrency and rate limits), and a plugin has no business second-guessing how many
// calls its operator wants in flight. Set SOKEL_MAX_CONCURRENCY when this process is the scarce
// resource — a handler that holds a whole document in memory, say.
//
// When a cap is set, the wait happens on the caller (the subscription's dispatcher goroutine) on
// purpose: that is the backpressure. Past the limit the rest stay in the subscription's pending
// buffer rather than this process spawning goroutines without end.
func concurrentHandler(limit int, fn func(*nats.Msg)) nats.MsgHandler {
	if limit <= 0 {
		return func(m *nats.Msg) { go fn(m) }
	}
	sem := make(chan struct{}, limit)
	return func(m *nats.Msg) {
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			fn(m)
		}()
	}
}

// dispatchConcurrency: how many calls one replica serves at once. 0 = no cap (the default).
//
// SOKEL_MAX_CONCURRENCY sets one; 1 restores the old strictly-serial behaviour, which is the escape
// hatch for a handler that is not concurrency-safe (shared state outside the handler, a library that
// insists on one call at a time). No cap by default because metering belongs to the operator: the
// platform already has per-channel concurrency and rate limits, and this process cannot know how
// much memory its container was given.
func dispatchConcurrency() int {
	v := strings.TrimSpace(EnvOr("MAX_CONCURRENCY", ""))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n
	}
	log.Printf("[sokel] SOKEL_MAX_CONCURRENCY=%q is not a non-negative integer, running without a cap", v)
	return 0
}

// callContext is the base context of one call. With deadline_ms from the platform it ends when the
// platform stops waiting, so a handler that honours ctx.Done() stops its upstream work then instead of
// finishing it for a caller that is gone (and may retry). An older platform sends none: no deadline.
func callContext(parent context.Context, deadlineMS int) (context.Context, context.CancelFunc) {
	if deadlineMS > 0 {
		return context.WithTimeout(parent, time.Duration(deadlineMS)*time.Millisecond)
	}
	return context.WithCancel(parent)
}

// violationHint is what a permissions violation almost always means for a replica: the broker only lets a group
// subscribe to its own reply prefix (per-group inboxes, platform + SDK v0.5.5), so a replica still using the global
// _INBOX either runs an older SDK or holds a connection from before the platform re-pushed its authorization.
const violationHint = "the broker refused this replica a subject; if it is the reply inbox, this replica cannot receive answers to its own requests (file upload, registration): rebuild the plugin with the current SDK, or restart the replica if the platform restarted"

// explainTimeout turns a bare request timeout into the reason, when a permissions violation was seen on this
// connection: the request itself succeeded, its reply was undeliverable. A bare "nats: timeout" once cost a day —
// the violation was in the replica's log, the timeout in the platform's error, and nobody put them together.
func explainTimeout(err error, violation string) error {
	if err == nil || violation == "" || !errors.Is(err, nats.ErrTimeout) {
		return err
	}
	return fmt.Errorf("%w (%s; %s)", err, violation, violationHint)
}
