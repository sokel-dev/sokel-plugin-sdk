// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sokel is the Sokel plugin SDK for Go.
//
// One way to write a plugin, independent of transport: declare the operation contract in a schema
// package, emit results frame by frame with an Emitter (text, JSON, typed variables, files), and
// never touch the transport. Two settings are enough: Endpoint (the platform URL) and Token.
//
// The transport today is NATS (request-reply plus a multi-frame inbox, covering both streaming and
// non-streaming), discovered through the platform's /connect-info. Adding a transport later changes
// discovery and the SDK internals, not the author's configuration.
package sokel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"time"
)

// Transport is the current transport, reported at registration and shown per replica in the UI.
type Transport string

const (
	NATS Transport = "nats"
)

// Config is all the author configures: an endpoint and a token. How handlers are written does not
// depend on how the plugin is deployed.
type Config struct {
	Endpoint string // the platform URL (https://hub.example.com); a literal nats:// skips discovery
	Token    string // the access token, generated in the plugin admin page
	Name     string // client name; defaults to the executable name
}

// Ctx is handed to a handler: it embeds context.Context and carries the runtime handles file
// transfer needs. Ctx is plugin.Ctx — a **transport-agnostic interface**.
//
// Authors keep writing func(ctx sokel.Ctx, …) with an unchanged signature, but because it is an
// interface the same handler runs on the NATS transport (natsCtx below) and on the platform's
// in-process host.
type Ctx = plugin.Ctx

// natsCtx implements those runtime capabilities for the NATS transport.
type natsCtx struct {
	context.Context
	rt   fileRuntime
	cred map[string]string
}

// Credential returns the resolved credential fields the platform sent for this call, for the plugin
// to build its own upstream authentication.
//   - A generic auth-scheme credential carries a "_scheme" key (auth_basic, auth_bearer, auth_header,
//     auth_query…) plus that scheme's fields (username/password, token, header_name/value…).
//   - A service credential carries the fields its credentialSchema declares (api_key, base_url…).
//
// It returns nil when there is no credential. The platform is the only party that stores or encrypts
// them; a plugin never persists one.
func (c natsCtx) Credential() map[string]string { return c.cred }

type opEntry struct {
	op     Operation
	invoke func(ctx Ctx, input json.RawMessage, sink emitterCore) error
}

// Plugin is one plugin instance: it holds the configuration and the operation table, and Run()
// connects, listens and dispatches over the transport.
type Plugin struct {
	version    string // the version SetVersion declared; empty falls back to SOKEL_VERSION
	cfg        Config
	ops        []opEntry
	credFields []Field // the credential contract, reported at registration for display only
	// doc and docURL are the user-facing document (markdown or a link), reported at registration and
	// rendered in the platform's docs drawer. The document travels with the plugin code: redeploy to
	// update it — the platform offers no editor for it.
	doc    string
	docURL string
	// oauth declares that the credential is obtained through OAuth. nil means it is not.
	oauth *OAuthSpec
	// authFlow declares the collaborative auth flow. nil means the credential is typed in by hand.
	authFlow     *authFlowDecl
	events       []Event       // event contracts, reported at registration (see event.go)
	eventsCommon []Field       // fields every event carries; the platform flattens them to the top level on trigger
	sources      []sourceEntry // long-running event sources; Run() starts a goroutine per source
	// capabilities is the optional capability self-report (see capabilities.go). Whether an operation
	// exists is in operations; how far it goes is here.
	capabilities map[string]bool
	// webhookFn handles platform-relayed webhooks (see webhook.go). nil means unsupported.
	webhookFn func(WebhookCtx, *WebhookRequest) WebhookResponse
	// managed means this replica's token came from deployment-level enrollment (sokel.enroll), i.e. it
	// ships with the deployment. Reported at registration so the replica list can show its origin.
	managed bool
}

func New(cfg Config) *Plugin {
	if cfg.Name == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.Name = baseName(exe)
		} else {
			cfg.Name = "sokel-plugin"
		}
	}
	return &Plugin{cfg: cfg}
}

// Register registers a typed operation. In and Out are the input/output structs (annotated with
// sokel tags); the contract is derived by reflection unless op.Inputs/Outputs are already given. The
// handler emits results through Emitter[Out].
func Register[In any, Out any](p *Plugin, op Operation, h func(Ctx, In, *Emitter[Out]) error) {
	mustBusinessOpID(op.ID)
	registerTyped(p, op, h)
}

// registerTyped is the registration itself, without id validation. Business operations arrive
// through Register (validated first); operations on the platform's reserved ids (the auth flow)
// arrive through registerReserved.
func registerTyped[In any, Out any](p *Plugin, op Operation, h func(Ctx, In, *Emitter[Out]) error) {
	if op.Inputs == nil {
		op.Inputs = deriveFields(reflect.TypeOf(new(In)).Elem())
	}
	if op.Outputs == nil {
		op.Outputs = deriveFields(reflect.TypeOf(new(Out)).Elem())
	}
	// An operation without inputs or outputs reports an empty array rather than null, so nothing
	// downstream dereferences a null.
	if op.Inputs == nil {
		op.Inputs = []Field{}
	}
	if op.Outputs == nil {
		op.Outputs = []Field{}
	}
	p.ops = append(p.ops, opEntry{
		op: op,
		invoke: func(ctx Ctx, input json.RawMessage, sink emitterCore) (err error) {
			var in In
			if err := bindInput(input, &in); err != nil {
				return fmt.Errorf("binding the input failed: %w", err)
			}
			// A panicking handler becomes an error frame (the node turns red with a readable message)
			// rather than taking the whole plugin process down over one bad call.
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("operation %q panicked: %v\n%s", op.ID, r, debug.Stack())
				}
			}()
			return h(ctx, in, &Emitter[Out]{core: sink})
		},
	})
}

// mustBusinessOpID enforces ^[a-z][a-z0-9_]*$ for business operation ids.
//
// The rule buys two things at once: **a dotted id becomes the platform's reserved namespace** (the
// auth flow's auth.start and friends live there, and a business id structurally cannot collide), and
// spellings that read badly downstream — uppercase, hyphens — are rejected on the spot.
//
// It panics at startup rather than silently renaming: once published, an operation id is a reference
// path inside canvas graphs, so renaming it breaks the links.
func mustBusinessOpID(id string) {
	if id == "" {
		panic("sokel: an operation id must not be empty")
	}
	for i, r := range id {
		ok := (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			if r == '.' {
				panic(fmt.Sprintf("sokel: operation id %q is in the platform's reserved namespace (it contains a dot) — declare auth flows with sokel.WithAuth", id))
			}
			panic(fmt.Sprintf("sokel: operation id %q is invalid; use lowercase letters, digits and underscores (^[a-z][a-z0-9_]*$)", id))
		}
	}
	// The three names of the old convention. The auth flow used to be recognised by sniffing for them
	// and is declarative now (see the top of auth.go). Without this check, upgrading the SDK would make
	// an older plugin's button silently disappear, leaving the author to debug "nothing happened".
	switch id {
	case "auth_start", "auth_submit", "auth_poll":
		panic(fmt.Sprintf("sokel: operation id %q is the old auth-flow convention; it is declarative now: sokel.WithAuth(p, sokel.AuthFlow{…})", id))
	}
}

// registerReserved registers an internal operation on a platform-reserved id (the auth flow). It
// goes through registerTyped to skip id validation, but reflection and the panic guard are **the
// same implementation** business operations use.
func registerReserved[In any, Out any](p *Plugin, op Operation, h func(Ctx, In) (Out, error)) {
	registerTyped(p, op, func(ctx Ctx, in In, out *Emitter[Out]) error {
		res, err := h(ctx, in)
		if err != nil {
			return err
		}
		out.Vars(res)
		return nil
	})
}

// newAuthID is the id of one authentication attempt, for when the plugin supplies none. Uniqueness
// within this process is enough: the platform hands it back as an opaque string and never parses it.
func newAuthID() string { return fmt.Sprintf("auth_%d", time.Now().UnixNano()) }

// contract returns the operation contracts reported in the registration handshake.
func (p *Plugin) contract() []Operation {
	out := make([]Operation, len(p.ops))
	for i, e := range p.ops {
		out[i] = e.op
	}
	return out
}

// Register implements plugin.Host: it registers an operation on this plugin (NATS transport).
//
// The platform's in-process host implements the same interface, so **one implementation** runs on
// either side. The only difference is the transport, and the implementation knows nothing of NATS.
func (p *Plugin) Register(op contract.Operation, fn plugin.Invoke) {
	// Reuse RegisterOp: the panic guard and the empty-array normalisation live there, and should not
	// exist twice.
	RegisterOp(p, op, func(ctx Ctx, raw json.RawMessage, out Sink) error { return fn(ctx, raw, out) })
}

func (p *Plugin) find(opID string) *opEntry {
	for i := range p.ops {
		if p.ops[i].op.ID == opID {
			return &p.ops[i]
		}
	}
	if opID == "" && len(p.ops) == 1 {
		return &p.ops[0] // single-operation plugin: a missing `operation` means the only one
	}
	return nil
}

// invokeBuffered runs a non-streaming call: find the operation, bind the input, buffer the frames,
// and merge the variables into one output object.
func (p *Plugin) invokeBuffered(ctx Ctx, operation string, input json.RawMessage) (map[string]any, error) {
	entry := p.find(operation)
	if entry == nil {
		return nil, fmt.Errorf("unknown operation %q", operation)
	}
	sink := &bufferSink{}
	if err := entry.invoke(ctx, input, sink); err != nil {
		return nil, err
	}
	if sink.vars == nil {
		sink.vars = map[string]any{}
	}
	return sink.vars, nil
}

// Run blocks: it connects to the platform (discovering the transport address through /connect-info),
// registers by reporting the contract, heartbeats, and dispatches calls.
func (p *Plugin) Run() error {
	return (&natsTransport{}).run(p)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Compile-time confirmation that sokel is one transport implementation (NATS) of the plugin
// interfaces. The platform's in-process host implements the same set, which is why one plugin
// implementation runs on either.
var (
	_ plugin.Host           = (*Plugin)(nil)
	_ plugin.CredentialHost = (*Plugin)(nil)
	_ plugin.Ctx            = natsCtx{}
	_ plugin.Sink           = Sink{}
)

// Env reads one of the plugin's environment variables; the name carries no prefix, so Env("TOKEN")
// reads SOKEL_TOKEN.
//
// Always read through it rather than os.Getenv: these variables are part of the contract between a
// plugin and the platform, and keeping the prefix in one place is what makes renaming one survivable.
func Env(name string) string { return pluginenv.Get(name) }

// EnvOr is Env with a fallback when the variable is unset.
func EnvOr(name, def string) string {
	if v := pluginenv.Get(name); v != "" {
		return v
	}
	return def
}

// traceCtxKey is where the platform's tracing context sits in the context.
type traceCtxKey struct{}

// TraceValue reads the tracing context the platform sent (run_id / workflow_id / node_id).
//
// Calls outside a workflow (console tests, health checks) have none of these and get "" back.
// **Treat "" as "no retry semantics"**, never as a constant key — doing the latter would deduplicate
// two independent calls into one.
func TraceValue(ctx context.Context, key string) string {
	t, _ := ctx.Value(traceCtxKey{}).(map[string]string)
	return t[key]
}

// SetVersion declares the plugin version, reported at registration and shown in the replica list.
// Without it the SDK falls back to the SOKEL_VERSION environment variable (easiest to inject when
// building a release image), and then to "sdk-go".
func (p *Plugin) SetVersion(v string) { p.version = v }
