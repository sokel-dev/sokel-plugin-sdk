// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// Collaborative credential authentication: some credentials **cannot be typed in** — a chat session
// needs a QR scan, a verification code has to be typed back, a Google account goes through a consent
// page. The panel and the plugin produce them together:
//
//	the panel's "log in" button -> start returns a challenge -> (scan / type back / consent page)
//	-> poll every 2s -> confirmed
//
// The plugin declares the flow in its schema with auth.QR(), auth.Input() or auth.OAuth(), and
// sokel-gen generates a RegisterAuth that takes the implementation. **Do not** register an operation
// named auth_start yourself.
//
// Why a declaration rather than "register operations with particular names":
//   - The platform used to sniff operation ids to decide whether to show the login button. Those
//     three names were never reserved and never validated, so any plugin with a business operation
//     called auth_start grew a button out of nowhere — and clicking it called that business operation.
//   - It also forced empty shells into existence: an OAuth plugin whose authorization the platform
//     answers end to end still had to declare an auth_start that would never be called, purely to
//     make the button appear.
//
// Now the capability lives in the declaration and the operation id is a transport detail: auth.start
// and friends are reserved, and business ids may not contain a dot (see mustBusinessOpID), so they
// cannot collide.

import (
	"encoding/json"
	"fmt"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// AuthKind is the shape of the challenge and decides how the panel renders it. It is **defined in
// contract** so the declaration side and the implementation side share one definition; this is only
// an alias. Defining it twice would eventually produce conversion noise between two types that hold
// the same values.
type AuthKind = contract.AuthKind

const (
	AuthQR    = contract.AuthQR    // a QR code, posed by the plugin
	AuthInput = contract.AuthInput // the user types something back, e.g. an SMS code
	AuthOAuth = contract.AuthOAuth // a third-party consent page, **answered by the platform**
)

// Reserved operation ids. They contain a dot, which a business id cannot produce.
const (
	opAuthStart  = "auth.start"
	opAuthPoll   = "auth.poll"
	opAuthSubmit = "auth.submit"
)

// AuthChallenge and AuthState are the runtime shapes of the auth flow. They are **defined in the
// plugin package** and aliased here, so generated registration code imports only that package and
// does not depend on the SDK (which would otherwise form an import cycle).
type (
	AuthChallenge = plugin.AuthChallenge
	AuthState     = plugin.AuthState
)

const (
	// Status constants: a misspelled string raises nothing, it just leaves the panel spinning.
	AuthPending   = "pending"
	AuthScanned   = "scanned"
	AuthConfirmed = "confirmed"
	AuthExpired   = "expired"
)

// authFlowDecl is what the registration handshake reports; the panel decides from it whether the
// credential row gets a login button.
type authFlowDecl struct {
	Kind  AuthKind `json:"kind"`
	Steps []string `json:"steps"` // start / poll / submit; the platform routes /credentials/{id}/auth/{step} by it
}

// SetAuthFlow implements plugin.AuthHost: it takes the declaration plus the implementation and hangs
// the handlers off the reserved operation ids.
//
// The declaration side (the schema's AuthMeta) and the implementation side (the handlers) meet here,
// and this is the only place they are wired together.
func (p *Plugin) SetAuthFlow(meta contract.AuthMeta, h plugin.AuthHandlers) {
	decl := authFlowDecl{Kind: meta.Kind}
	if h.Start != nil {
		decl.Steps = append(decl.Steps, "start")
		registerReserved(p, Operation{ID: opAuthStart, Label: "Start authentication", Internal: true},
			func(ctx Ctx, _ authStartIn) (authStartOut, error) {
				ch, err := h.Start(ctx)
				if err != nil {
					return authStartOut{}, err
				}
				if ch == nil {
					return authStartOut{}, fmt.Errorf("the auth flow's Start returned no challenge")
				}
				kind := ch.Kind
				if kind == "" {
					kind = string(meta.Kind)
				}
				authID := ch.AuthID
				if authID == "" {
					authID = newAuthID()
				}
				return authStartOut{
					AuthID: authID,
					Challenge: map[string]any{
						"kind": kind, "qr_image": ch.QRImage, "prompt": ch.Prompt,
					},
					ExpiresIn: ch.ExpiresIn,
				}, nil
			})
	}
	if h.Poll != nil {
		decl.Steps = append(decl.Steps, "poll")
		registerReserved(p, Operation{ID: opAuthPoll, Label: "Poll authentication", Internal: true},
			func(ctx Ctx, in authPollIn) (authPollOut, error) {
				st, err := h.Poll(ctx, in.AuthID)
				if err != nil {
					return authPollOut{}, err
				}
				if st == nil {
					return authPollOut{Status: AuthPending}, nil
				}
				out := authPollOut{Status: st.Status}
				// Only carry the session once confirmed: handing it over earlier makes the platform
				// rewrite the credential row over and over
				if st.Status == AuthConfirmed && len(st.Session) > 0 {
					out.Session = json.RawMessage(st.Session)
				}
				return out, nil
			})
	}
	if h.Submit != nil {
		decl.Steps = append(decl.Steps, "submit")
		registerReserved(p, Operation{ID: opAuthSubmit, Label: "Submit authentication input", Internal: true},
			func(ctx Ctx, in authSubmitIn) (authSubmitOut, error) {
				if err := h.Submit(ctx, in.AuthID, in.Input); err != nil {
					return authSubmitOut{}, err
				}
				return authSubmitOut{OK: true}, nil
			})
	}
	p.authFlow = &decl
	// The OAuth provider and scopes belong to this same declaration: how a credential is obtained is
	// one thing, not two.
	//
	// The test looks only at provider, because **not every vendor has scopes** — one provider grants
	// permissions by having the user tick pages on the consent screen, with no scope parameter in the
	// request at all. Requiring scopes too would silently drop that declaration here: the platform
	// would receive an empty oauth, the authorize button would never appear on the credential row, and
	// not one line of log would say why.
	if meta.Provider != "" {
		p.oauth = &OAuthSpec{Provider: meta.Provider, Scopes: meta.Scopes}
	}
}

// —— I/O of the reserved operations, matching the shape of /credentials/{id}/auth/{step} ——

type authStartIn struct{}

type authStartOut struct {
	AuthID    string         `sokel:"auth_id" label:"Auth ID"`
	Challenge map[string]any `sokel:"challenge" label:"Challenge"`
	ExpiresIn int            `sokel:"expires_in,optional" label:"Expires in (s)"`
}

type authPollIn struct {
	AuthID string `sokel:"auth_id" label:"Auth ID"`
}

type authPollOut struct {
	Status string `sokel:"status" label:"Status" desc:"pending / scanned / confirmed / expired"`
	// The platform writes this into the credential row and strips it from the response, so it never
	// reaches the frontend.
	//
	// Declared as any rather than json.RawMessage: a nil interface field is left out entirely, while a
	// nil []byte would emit session:null — and that would make the platform rewrite the credential row
	// even while still pending.
	Session any `sokel:"session,optional" label:"Session"`
}

type authSubmitIn struct {
	AuthID string `sokel:"auth_id" label:"Auth ID"`
	Input  string `sokel:"input" label:"User input"`
}

type authSubmitOut struct {
	OK bool `sokel:"ok" label:"Submitted"`
}
