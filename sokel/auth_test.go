// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/auth"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// An auth flow is **declared**, not recognised by a handful of special operation names. These two pin
// that boundary down: declaring one reports auth_flow plus internal operations under reserved ids, and
// business operations never touch the reserved namespace.

func TestAuthFlowDeclaresStepsAndReservedOps(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.Input(), plugin.AuthHandlers{
		Start:  func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{QRImage: "data:image/png;base64,x"}, nil },
		Poll:   func(Ctx, string) (*AuthState, error) { return &AuthState{Status: AuthPending}, nil },
		Submit: func(Ctx, string, string) error { return nil },
	})
	if p.authFlow == nil || p.authFlow.Kind != AuthInput {
		t.Fatalf("an input auth flow should be declared: %+v", p.authFlow)
	}
	if got := strings.Join(p.authFlow.Steps, ","); got != "start,poll,submit" {
		t.Errorf("steps should faithfully reflect which handlers were supplied, got %q", got)
	}
	// The operation ids are a transport detail and live in the reserved namespace; a business id may not
	// contain a dot, so they cannot collide
	for _, want := range []string{"auth.start", "auth.poll", "auth.submit"} {
		if p.find(want) == nil {
			t.Errorf("the reserved operation %q was not registered", want)
		}
	}
	// And all of them are internal: they must never appear on the canvas
	for _, e := range p.ops {
		if !e.op.Internal {
			t.Errorf("the auth flow operation %q must be internal", e.op.ID)
		}
	}
}

// The platform answers OAuth throughout, so a plugin **should not** write a handler that is never called
// merely to make the button appear. auth.OAuth() therefore has no steps, and the generated RegisterAuth
// takes no handler.
func TestOAuthFlowNeedsNoHandlers(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.OAuth("google", "s"), plugin.AuthHandlers{})
	p.SetDoc("# Usage\nWhere to get the credential...", "https://docs.example.com/p")
	p.SetCapabilities(map[string]bool{CapRecency: false, CapTimeRange: true})
	if p.authFlow == nil || p.authFlow.Kind != AuthOAuth {
		t.Fatalf("oauth should produce an oauth auth flow: %+v", p.authFlow)
	}
	if len(p.authFlow.Steps) != 0 {
		t.Errorf("the platform answers oauth, so no steps should be declared: %v", p.authFlow.Steps)
	}
	if len(p.ops) != 0 {
		t.Errorf("oauth should register no operations, got %d", len(p.ops))
	}
	if p.oauth == nil || p.oauth.Provider != "google" || len(p.oauth.Scopes) != 1 {
		t.Fatalf("an oauth declaration must carry its provider and scopes: %+v", p.oauth)
	}

	// A provider without scopes — one that grants permissions by ticking pages on the consent page, with no
	// scope parameter in the request — still has to be reported. Dropping it leaves the platform with an
	// empty oauth, the Authorise button on the credential row never appears, and the logs say nothing at
	// all.
	q := New(Config{Name: "t"})
	q.SetAuthFlow(auth.OAuth("notion"), plugin.AuthHandlers{})
	if q.oauth == nil || q.oauth.Provider != "notion" {
		t.Fatalf("a scopeless provider must still report its oauth declaration: %+v", q.oauth)
	}
}

// start's challenge shape is **a platform contract**: the panel renders by kind and polls by auth_id.
// When a plugin supplies no auth_id the SDK has to fill one in, or poll has no handle and the panel can
// only spin.
func TestAuthStartFillsDefaults(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.QR(), plugin.AuthHandlers{
		Start: func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{QRImage: "qr", ExpiresIn: 60}, nil },
		Poll:  func(Ctx, string) (*AuthState, error) { return nil, nil },
	})
	out, err := p.invokeBuffered(natsCtx{}, "auth.start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := out["auth_id"].(string); s == "" {
		t.Error("the SDK must supply an auth_id when the plugin does not")
	}
	ch, _ := out["challenge"].(map[string]any)
	if ch["kind"] != "qr" || ch["qr_image"] != "qr" {
		t.Errorf("the challenge should carry its kind and the QR code: %v", ch)
	}
}

// poll returning nil counts as pending, which is how plugins commonly write it — return nil, nil until a
// terminal state — and the session comes out only on confirmation: handing it over earlier would have
// the platform rewrite the credential row over and over.
func TestAuthPollNilIsPendingAndSessionOnlyOnConfirmed(t *testing.T) {
	cases := []struct {
		name        string
		state       *AuthState
		wantStatus  string
		wantSession bool
	}{
		{"nil counts as pending", nil, AuthPending, false},
		{"unconfirmed carries no session", &AuthState{Status: AuthScanned, Session: json.RawMessage(`{"a":1}`)}, AuthScanned, false},
		{"only confirmed carries the session", &AuthState{Status: AuthConfirmed, Session: json.RawMessage(`{"a":1}`)}, AuthConfirmed, true},
	}
	for _, c := range cases {
		p := New(Config{Name: "t"})
		st := c.state
		p.SetAuthFlow(auth.QR(), plugin.AuthHandlers{
			Start: func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{}, nil },
			Poll:  func(Ctx, string) (*AuthState, error) { return st, nil },
		})
		out, err := p.invokeBuffered(natsCtx{}, "auth.poll", json.RawMessage(`{"auth_id":"a1"}`))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out["status"] != c.wantStatus {
			t.Errorf("%s: status = %v, want %q", c.name, out["status"], c.wantStatus)
		}
		if _, has := out["session"]; has != c.wantSession {
			t.Errorf("%s: session present = %v, want %v", c.name, has, c.wantSession)
		}
	}
}

// The yardstick for a business operation id: the reserved namespace (anything with a dot) and the three
// old conventional names are refused on the spot. Letting them through silently once made a Log in
// button sprout on the credential row out of nowhere.
func TestBusinessOpIDRejectsReservedNames(t *testing.T) {
	bad := []string{"auth.start", "auth_start", "auth_poll", "auth_submit", "SendText", "send-text", "", "2fa"}
	for _, id := range bad {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("the operation id %q should be refused", id)
				}
			}()
			mustBusinessOpID(id)
		}()
	}
	for _, id := range []string{"send_text", "gmail_list", "a", "get2"} {
		mustBusinessOpID(id) // valid: must not panic
	}
}

// Whatever is declared must be **reported** — the classic silent failure of any self-reporting
// mechanism: everything looks fine on the plugin side, nothing happens on the platform side, and the
// author is left debugging an unresponsive screen. auth_flow fell into it on the day it shipped: the
// declaration was written and the handshake payload was missing a line.
func TestRegisterBodyCarriesDeclarations(t *testing.T) {
	p := New(Config{Name: "t", Token: "skp_x"})
	p.SetCredentialContract([]Field{{Name: "tok", Type: contract.TString}})
	p.SetAuthFlow(auth.OAuth("google", "s"), plugin.AuthHandlers{})
	p.SetDoc("# 用法\n凭证去哪申请…", "https://docs.example.com/p")
	p.SetCapabilities(map[string]bool{CapRecency: false, CapTimeRange: true})
	Register(p, Operation{ID: "send_text"}, func(Ctx, struct{}, *Emitter[struct{}]) error { return nil })

	var body map[string]any
	raw, _ := json.Marshal(p.registerBody("inst", "host", p.contract()))
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"operations", "credential_schema", "oauth", "auth_flow", "token", "instance_id", "doc", "doc_url", "capabilities"} {
		if body[k] == nil {
			t.Errorf("the handshake payload is missing %q: declared but not reported is, to the platform, not declared at all", k)
		}
	}
	flow, _ := body["auth_flow"].(map[string]any)
	if flow["kind"] != "oauth" {
		t.Errorf("auth_flow should carry its kind: %v", body["auth_flow"])
	}
	// Self-reported capabilities: **false has to be reported too**. "Declared as unsupported" and "not
	// declared" are different things — the platform should warn the user about the first and fall back to
	// the old behaviour for the second. One missing false quietly turns the warning off.
	caps, _ := body["capabilities"].(map[string]any)
	if caps[CapRecency] != false || caps[CapTimeRange] != true {
		t.Errorf("the self-reported capabilities were not reported faithfully: %v", body["capabilities"])
	}
}

// A plugin that never declared capabilities reports null, and the platform treats that as "not declared"
// and keeps the old behaviour. Doing the reverse — treating anything unlisted as unsupported — would
// have every existing plugin lose its capabilities on the day it upgraded the SDK.
func TestCapabilitiesAbsentWhenUndeclared(t *testing.T) {
	p := New(Config{Name: "t", Token: "skp_x"})
	var body map[string]any
	raw, _ := json.Marshal(p.registerBody("inst", "host", p.contract()))
	_ = json.Unmarshal(raw, &body)
	if body["capabilities"] != nil {
		t.Errorf("undeclared should be null, got %v", body["capabilities"])
	}
}
