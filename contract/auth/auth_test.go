// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// The steps follow from the shape and should not be transcribed by the caller — a wrong transcription
// goes unnoticed: one step too many promises an implementation that is never called, and one too few
// leaves the panel stuck on the missing step.
func TestStepsFollowFromKind(t *testing.T) {
	cases := []struct {
		name  string
		meta  contract.AuthMeta
		kind  contract.AuthKind
		steps []contract.AuthStep
	}{
		{"qr", QR(), contract.AuthQR,
			[]contract.AuthStep{contract.StepStart, contract.StepPoll}},
		// Input has one step more than QR, submit — and that step is the entire point of the shape
		{"input", Input(), contract.AuthInput,
			[]contract.AuthStep{contract.StepStart, contract.StepPoll, contract.StepSubmit}},
		// OAuth has none: the client secret is the platform's, and it answers start and poll throughout
		{"OAuth", OAuth("google", "s1", "s2"), contract.AuthOAuth, nil},
	}
	for _, c := range cases {
		if c.meta.Kind != c.kind {
			t.Errorf("%s: kind = %q, want %q", c.name, c.meta.Kind, c.kind)
		}
		if len(c.meta.Steps) != len(c.steps) {
			t.Fatalf("%s: steps = %v, want %v", c.name, c.meta.Steps, c.steps)
		}
		for i, s := range c.steps {
			if c.meta.Steps[i] != s {
				t.Errorf("%s: steps[%d] = %q, want %q", c.name, i, c.meta.Steps[i], s)
			}
		}
	}
}

// Scopes are a required argument: this is where OAuth's least privilege lives, and leaving them empty
// asks for a consent page that is certain to be refused.
func TestOAuthCarriesProviderAndScopes(t *testing.T) {
	m := OAuth("google", "https://www.googleapis.com/auth/gmail.readonly")
	if m.Provider != "google" || len(m.Scopes) != 1 {
		t.Fatalf("provider and scopes did not come through: %+v", m)
	}
}
