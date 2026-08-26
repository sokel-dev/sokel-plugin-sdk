// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package auth builds declarations of how a credential is obtained.
//
// Why constructors rather than a struct to fill in: **the steps follow from the shape**. A QR flow is
// always start+poll; OAuth always has none, because the platform answers it. Writing Steps out again
// would copy the same fact a second time, and nobody notices when the copy is wrong: one step too
// many promises an implementation that will never be called, one too few leaves the panel stuck on
// the missing step.
//
// Same approach as contract/field: whatever a constructor can pin down is not left to the caller.
package auth

import "github.com/sokel-dev/sokel-plugin-sdk/contract"

// QR is scan-to-log-in: the plugin poses the challenge (a QR image), the platform relays it, and the
// panel polls until confirmed.
func QR() contract.AuthMeta {
	return contract.AuthMeta{
		Kind:  contract.AuthQR,
		Steps: []contract.AuthStep{contract.StepStart, contract.StepPoll},
	}
}

// Input is the user typing something back (an SMS code, say): one more step than QR, submit — and
// that step is the entire point of this shape.
func Input() contract.AuthMeta {
	return contract.AuthMeta{
		Kind:  contract.AuthInput,
		Steps: []contract.AuthStep{contract.StepStart, contract.StepPoll, contract.StepSubmit},
	}
}

// OAuth is a third-party consent page. It has **no steps**: the client secret lives with the
// platform, so the plugin can neither build the consent URL nor should it ever handle a refresh
// token — the platform answers start and poll on its behalf.
//
// Scopes are a parameter because that is where OAuth's least privilege is expressed; leaving them
// empty for a provider that requires them asks for a consent page that will certainly be refused.
// But **not every provider has scopes** — one grants permissions by having the user tick pages on the
// consent screen, with no scope parameter at all — so this does not force a non-empty value. Whether
// they are required is the platform's call, per provider.
func OAuth(provider string, scopes ...string) contract.AuthMeta {
	return contract.AuthMeta{Kind: contract.AuthOAuth, Provider: provider, Scopes: scopes}
}
