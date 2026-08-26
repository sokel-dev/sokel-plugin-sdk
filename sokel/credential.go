// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import "reflect"

// CredentialAs binds the credential map the platform sent for this call into a typed struct T,
// following the sokel tags.
//
// It replaces bare ctx.Credential()["key"] access: the struct is the contract, field names are
// checked at compile time, and there is one source of truth — the same sokel tags and reflection
// used for operation inputs and outputs.
//
//	type Cred struct {
//	    BaseURL   string `sokel:"base_url"   label:"Service base URL"`
//	    XSuperUID string `sokel:"x_super_uid" label:"x-super-uid header" default:"7"`
//	}
//	cred := sokel.CredentialAs[Cred](ctx)   // cred.BaseURL / cred.XSuperUID
//
// Rules: the field name comes from `sokel:"name"` (defaulting to the snake_case field name); only
// string fields are bound, because credential values are always strings; a missing or empty value
// falls back to the `default:"..."` tag, and to the zero value if there is none.
func CredentialAs[T any](ctx Ctx) T {
	var out T
	bindCredential(ctx.Credential(), &out)
	return out
}

// SourceCredentialAs is the typed read on the event-source side (the same as CredentialAs).
//
// A source gets a SourceCtx rather than a Ctx, and used to be stuck with bare-key access — even
// though sources are where credentials are read and written most (cursors, sessions), and a
// misspelled key there binds nothing at all.
func SourceCredentialAs[T any](ctx SourceCtx) T {
	var out T
	bindCredential(ctx.Credential(), &out)
	return out
}

// BindCredential is the pointer form of CredentialAs: it binds into a struct the caller already has.
func BindCredential(ctx Ctx, dst any) { bindCredential(ctx.Credential(), dst) }

// bindCredential reflects over dst and fills its string fields from the credential map by sokel tag
// name; missing or empty values go through the `default:"..."` tag, reusing applyDefaultTag so this
// matches input binding.
func bindCredential(cred map[string]string, dst any) {
	v := reflect.ValueOf(dst).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _ := parseSokelTag(sf)
		if name == "-" {
			continue
		}
		fv := v.Field(i)
		if val, ok := cred[name]; ok && val != "" && fv.Kind() == reflect.String && fv.CanSet() {
			fv.SetString(val)
			continue
		}
		applyDefaultTag(fv, sf) // missing or empty -> the default tag, else the zero value
	}
}

// SetCredentialContract implements plugin.CredentialHost: it takes an **already declared** credential
// contract as-is.
//
// This is what the generated RegisterCredential uses. A schema declaration can express enum
// candidates and defaults that a struct tag cannot, so this path does no reflection at all.
func (p *Plugin) SetCredentialContract(fields []Field) { p.credFields = fields }

// SetDoc implements plugin.DocHost: it takes the user-facing document (markdown or a URL; one of
// them is enough).
//
// Write the document as a real .md file and //go:embed it. A raw string full of code fences and
// backticks turns "fix one sentence" into a puzzle about how to close the literal.
func (p *Plugin) SetDoc(markdown, url string) { p.doc, p.docURL = markdown, url }

// OAuthSpec declares that this plugin's credential is obtained through an OAuth provider.
type OAuthSpec struct {
	Provider string   `json:"provider"` // "google" today
	Scopes   []string `json:"scopes"`   // the scopes to request, **declared by the plugin** rather than hard-coded in the platform
}
