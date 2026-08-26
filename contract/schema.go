// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import "fmt"

// FieldSpec is a field declaration, implemented by the builders in sokel/field. The contract layer
// knows only this interface and not the builders' concrete types, which would otherwise form an
// import cycle.
type FieldSpec interface{ Field() Field }

// BuildFields turns a set of declarations into contract fields.
func BuildFields(specs []FieldSpec) []Field {
	out := make([]Field, 0, len(specs))
	for _, s := range specs {
		if s == nil {
			continue
		}
		out = append(out, s.Field())
	}
	return out
}

// Meta is an operation's metadata. Inputs and outputs are not part of it — those are Inputs/Outputs.
type Meta struct {
	ID         string
	Label      string
	Desc       string
	TimeoutSec int  // the plugin knows best how long this takes; self-reporting spares the user from guessing
	Stream     bool // streaming: a multi-frame reply
	Internal   bool // internal operations (the auth flow): never on the canvas, only the panel calls them
}

// Schema is one operation's complete declaration. **Declaration only, no implementation** — the
// generated registration function takes the implementation, with a fully concrete signature and no
// generics or any.
//
// Declaring before implementing is deliberate: a contract is a public interface. It should be
// reviewable on its own and exportable as JSON for other languages' SDKs to generate types from,
// rather than a by-product inferred from implementation code.
type Schema interface {
	Meta() Meta
	Inputs() []FieldSpec
	Outputs() []FieldSpec
}

// CredentialSchema declares this plugin's credential contract.
//
// Same path as operations and events: declare in the schema, let sokel-gen generate the types, report
// them in the registration handshake. Credentials used to rely on struct-tag reflection in package
// main, which cannot express enum candidates or defaults — that was how operations looked before
// codegen, and credentials simply had not moved across yet.
type CredentialSchema interface {
	CredentialFields() []FieldSpec
}

// AuthMeta is **how a credential is obtained**: the contract half of a collaborative auth flow.
//
// Contract only, no implementation — Start, Poll and Submit are functions that stay on the
// implementation side, taken by the generated RegisterAuth, exactly as operations pair a schema
// declaration with an OnXxx.
type AuthMeta struct {
	// Kind is the shape. Build it with auth.QR(), auth.Input() or auth.OAuth(); do not hand-write it.
	Kind AuthKind
	// Steps is which steps the plugin implements. It **follows from Kind** (see contract/auth) and
	// should not be written by hand: doing so copies "which steps a QR flow needs" a second time, and
	// nobody notices when the copy is wrong. The generated RegisterAuth's parameter list follows it, so
	// a missing step is a compile error rather than a panic at startup.
	Steps []AuthStep
	// Provider and Scopes are for kind=oauth. The scopes are **declared by the plugin** rather than
	// hard-coded in the platform, so adding another plugin for the same provider changes no platform code.
	Provider string
	Scopes   []string
}

// AuthKind is the shape of the authentication.
type AuthKind string

const (
	AuthQR    AuthKind = "qr"    // a QR code, posed by the plugin
	AuthInput AuthKind = "input" // the user types something back, e.g. an SMS code
	AuthOAuth AuthKind = "oauth" // a third-party consent page, **answered by the platform**
)

// AuthStep is one step of an auth flow.
type AuthStep string

const (
	StepStart  AuthStep = "start"
	StepPoll   AuthStep = "poll"
	StepSubmit AuthStep = "submit"
)

// AuthSchema declares how the credential is obtained. It hangs off the credential declaration (one
// more method on the same type) rather than standing alone: how a credential is obtained is **a
// property of that credential**, and splitting it in two would mean looking in two places to answer
// "where did this credential come from?".
type AuthSchema interface {
	AuthMeta() AuthMeta
}

// AuthOf extracts the authentication declaration.
func AuthOf(s AuthSchema) AuthMeta { return s.AuthMeta() }

// CredentialOf flattens a credential declaration into contract fields.
func CredentialOf(s CredentialSchema) []Field { return BuildFields(s.CredentialFields()) }

// OperationOf flattens a declaration into the wire protocol's Operation, for the handshake.
func OperationOf(s Schema) Operation {
	m := s.Meta()
	return Operation{
		ID: m.ID, Label: m.Label, Desc: m.Desc,
		Stream: m.Stream, Internal: m.Internal, TimeoutSec: m.TimeoutSec,
		Inputs:  BuildFields(s.Inputs()),
		Outputs: BuildFields(s.Outputs()),
	}
}

// Operation is an operation declaration. Leaving Inputs/Outputs empty derives them from Register's
// In/Out types by reflection. The json tags are what the registration handshake reports.
type Operation struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Desc     string `json:"desc,omitempty"`
	Stream   bool   `json:"stream,omitempty"`   // streaming: a multi-frame reply
	Internal bool   `json:"internal,omitempty"` // internal (the auth flow): never on the canvas, only the panel calls it
	// TimeoutSec is this operation's suggested timeout in seconds. The platform takes the node's
	// explicit setting first, then this, then its own 60s default. Declare it for heavy work
	// (transcription, long-form synthesis, parsing large files) or 60s cuts it short — and whoever
	// drags the node onto a canvas has no idea what to put there.
	TimeoutSec int     `json:"timeoutSec,omitempty"`
	Inputs     []Field `json:"inputs"`
	Outputs    []Field `json:"outputs"`
}

// opEntry is a registered operation plus its type-erased invocation entry point.

// RequireInputs checks inputs against the operation contract: a required field may be neither
// missing nor an empty string.
//
// This is what makes the contract **bite**. Executors no longer each write `if x == "" { return err }`
// — that knowledge and the frontend's required markers were two hand-written copies, and one audit
// found six fields the frontend had failed to mark alongside 32 scattered checks in the backend.
// Reading the same declaration, the two cannot diverge.
//
// It checks only presence, not type: type normalisation happens before the call.
func RequireInputs(op Operation, cfg map[string]any) error {
	for _, f := range op.Inputs {
		if !f.Required {
			continue
		}
		v, ok := cfg[f.Name]
		if !ok || v == nil || v == "" {
			label := f.Label
			if label == "" {
				label = f.Name
			}
			return fmt.Errorf("%q is missing the required parameter %q (%s)", op.Label, label, f.Name)
		}
	}
	return nil
}
