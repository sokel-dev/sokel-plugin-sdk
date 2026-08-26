// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// The credential output has to give two things: a Cred for typed reads, and **the declared fields
// verbatim** for reporting. With only Cred, to be reflected over later, the enum options and defaults
// would be lost at the moment of generation — which is exactly what credentials lacked while they stayed
// on the reflection route.
func TestRenderCredentialKeepsDeclaredFields(t *testing.T) {
	fields := []Field{
		{Name: "api_key", Label: "Key", Type: "secret", Required: true},
		{Name: "region", Label: "Region", Type: "select", Default: "cn",
			Options: []Option{{Value: "cn", Label: "China"}, {Value: "us", Label: "United States"}}},
		{Name: "note", Label: "Note", Type: "text"},
	}
	src, err := RenderCredential("main", SchemaRef{}, fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type Cred struct",
		"APIKey string `sokel:\"api_key\"",           // required, so no optional; the acronym is capitalised per Go convention
		"`sokel:\"note,optional\"",                   // optional, so it carries optional
		"secret:\"true\"",                            // secret has to be recognisable to CredentialAs
		"func credentialContract() []contract.Field", // what is reported is the declared fields verbatim
		"Options: []contract.Option{",                // reflection cannot produce this, which is why it moved
		"Default: \"cn\"",
		"plugin.CredentialHost", // the output imports no SDK; declarations inside plugin-core would form a cycle
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the output is missing %q\n---\n%s", want, src)
		}
	}
	if strings.Contains(src, "go-sdk/sokel") {
		t.Errorf("the output should not import the SDK:\n%s", src)
	}
}

// A credential's type vocabulary is **form controls**, not the string/number set that operation fields
// use. Letting a wrong one through means declaring a dropdown and getting a text box, and the author
// will most likely blame the frontend.
func TestCredentialTypeVocabularyIsChecked(t *testing.T) {
	err := checkCredTypes([]Field{{Name: "region", Type: "enum"}})
	if err == nil || !strings.Contains(err.Error(), "field.Select") {
		t.Errorf("enum should be refused with a pointer to field.Select, got %v", err)
	}
	for _, ok := range []string{"text", "secret", "select", "url", "secret-file", "header-list"} {
		if err := checkCredTypes([]Field{{Name: "f", Type: ok}}); err != nil {
			t.Errorf("%q is a valid credential field type: %v", ok, err)
		}
	}
}

// The generated RegisterAuth's parameter list is the declared steps. That is this route's real advantage
// over a hand-written WithAuth: **one handler short is a compile error** rather than a panic at startup —
// and an earlier form did not even panic, leaving the panel quietly stuck on the missing step.
func TestRenderAuthSignatureFollowsSteps(t *testing.T) {
	qr, err := RenderAuth("main", AuthMeta{Kind: "qr", Steps: []string{"start", "poll"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"contract.AuthQR", // a constant of a named type, not a bare literal
		"[]contract.AuthStep{contract.StepStart, contract.StepPoll}",
		"start func(plugin.Ctx) (*plugin.AuthChallenge, error)",
		"poll func(ctx plugin.Ctx, authID string) (*plugin.AuthState, error)",
		"plugin.AuthHandlers{Start: start, Poll: poll}",
	} {
		if !strings.Contains(qr, want) {
			t.Errorf("the qr output is missing %q\n---\n%s", want, qr)
		}
	}
	if strings.Contains(qr, "submit") {
		t.Error("an undeclared submit should not appear in the parameter list")
	}

	// oauth: the platform answers throughout, so not one handler is needed
	oauth, err := RenderAuth("main", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(oauth, "func RegisterAuth(h plugin.AuthHost) {") {
		t.Errorf("oauth should need no handler:\n%s", oauth)
	}
	if !strings.Contains(oauth, `[]string{"s"}`) { // gofmt aligns field names, so pin only the value
		t.Errorf("the scopes must reach the output; the platform does not hard-code them:\n%s", oauth)
	}
	// The output imports only the kernel: plugin-core holds contract declarations too, and depending on
	// the SDK would form a cycle
	for _, src := range []string{qr, oauth} {
		if strings.Contains(src, "go-sdk/sokel") {
			t.Errorf("the output should not import the SDK:\n%s", src)
		}
	}
}

// The declaration's internal consistency: a wrong combination is caught at generation time rather than
// left to run time.
func TestCheckAuthMeta(t *testing.T) {
	cases := []struct {
		name string
		meta AuthMeta
		bad  string // keyword expected in the error; empty means it should pass
	}{
		{"valid qr", AuthMeta{Kind: "qr", Steps: []string{"start", "poll"}}, ""},
		{"valid oauth", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}}, ""},
		{"unknown kind", AuthMeta{Kind: "sms"}, "unknown authentication kind"},
		{"unknown step", AuthMeta{Kind: "qr", Steps: []string{"start", "poll", "confirm"}}, "unknown authentication step"},
		// Declaring steps for oauth promises an implementation that will never be called (the previous
		// version was exactly that empty shell)
		{"oauth must not declare steps", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}, Steps: []string{"start"}}, "must not declare Steps"},
		{"oauth without scopes", AuthMeta{Kind: "oauth", Provider: "google"}, "Scopes"},
		// But not every provider has scopes: one grants permissions by ticking pages on the consent page
		{"a scopeless provider needs none", AuthMeta{Kind: "oauth", Provider: "notion"}, ""},
		{"oauth without a provider", AuthMeta{Kind: "oauth", Scopes: []string{"s"}}, "Provider"},
		// One step short leaves the panel stuck on it
		{"qr without poll", AuthMeta{Kind: "qr", Steps: []string{"start"}}, "both the start and poll steps"},
	}
	for _, c := range cases {
		err := checkAuthMeta(c.meta)
		if c.bad == "" {
			if err != nil {
				t.Errorf("%s: should not have failed, got %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.bad) {
			t.Errorf("%s: expected the error to contain %q, got %v", c.name, c.bad, err)
		}
	}
}
