// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import "github.com/sokel-dev/sokel-plugin-sdk/contract"

// The **runtime shape** of collaborative authentication.
//
// It lives in plugin-core rather than the SDK so that generated registration code imports only the
// kernel — the same reason as the credential contract: plugin-core holds contract declarations of its
// own, and depending on the SDK in return would form a cycle. The SDK re-exports these as type aliases,
// sokel.AuthChallenge and sokel.AuthState, so authors never notice.

// AuthChallenge is the challenge start hands to the panel.
type AuthChallenge struct {
	// AuthID identifies this authentication attempt; poll and submit come back carrying it. Leave it
	// empty and the SDK generates one.
	AuthID string
	// Kind defaults to the declared Kind when empty. Overriding it per attempt is needed only when one
	// plugin's credentials use different shapes.
	Kind string
	// QRImage is the QR code for kind=qr, as a data URI such as "data:image/png;base64,...".
	QRImage string
	// Prompt is one sentence for the user, which also serves as the input placeholder for kind=input.
	Prompt string
	// ExpiresIn is the lifetime in seconds; 0 tells the panel nothing.
	ExpiresIn int
}

// AuthState is poll's answer.
type AuthState struct {
	// Status is one of pending, scanned (the code was scanned and awaits confirmation), confirmed or
	// expired.
	Status string
	// Session is the session credential on confirmation, handed to **the platform** to write into the
	// credential row — it never goes back to the frontend, so no browser handles the plaintext.
	//
	// It must be a JSON **object**. A string gets another layer of quotes around it — double encoding —
	// and the plugin cannot decode it back the next time it reads the credential.
	Session []byte
}

// AuthHandlers is the implementation side of an auth flow. Which of them are non-nil follows from the
// declared Steps, and the generated RegisterAuth keeps the two aligned.
type AuthHandlers struct {
	Start  func(Ctx) (*AuthChallenge, error)
	Poll   func(ctx Ctx, authID string) (*AuthState, error)
	Submit func(ctx Ctx, authID, input string) error
}

// AuthHost is a host that can accept a collaborative auth flow; the SDK's *sokel.Plugin implements it.
// As with CredentialHost it is a small, single-method interface, which is what keeps generated code
// free of any SDK import.
type AuthHost interface {
	SetAuthFlow(meta contract.AuthMeta, h AuthHandlers)
}
