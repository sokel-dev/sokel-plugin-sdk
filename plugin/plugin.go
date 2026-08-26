// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package plugin is the seam between a plugin's implementation and **its transport**.
//
// A plugin's implementation — schema declarations plus handlers — should exist once and be carried by
// either transport:
//
//	server            an in-process call   — the platform is the host itself
//	plugin-builtin    sokel over NATS      — deployed separately on another machine
//
// Only the transport differs, so an implementation should know nothing of *sokel.Plugin or sokel.Ctx —
// types that belong to the NATS side — and see only the interfaces below. Whoever implements them
// decides which route a call takes.
//
// The interfaces are deliberately small: credentials, fetching a file, storing one and reporting status
// are all the runtime capability a real plugin uses. A large interface is hard for both transports to
// implement, and most of what makes it large would serve only one of them.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// Host is where a plugin registers its operations. sokel.Plugin and the platform's in-process host
// each implement it.
type Host interface {
	Register(op contract.Operation, fn Invoke)
}

// CredentialHost is a host that can accept a credential contract; the SDK's *sokel.Plugin implements
// it.
//
// It is a separate small interface rather than part of Host because an in-process host has no such
// thing as "reporting a credential contract", while the generated code has to attach to both. That also
// keeps the generated code free of any SDK import — plugin-core depending on the SDK in return would
// form a cycle, since the kernel's own contract declarations live in plugin-core.
type CredentialHost interface {
	SetCredentialContract(fields []contract.Field)
}

// DocHost is a host that can accept a plugin's user guide.
//
// Same pattern as the credential contract — a small, optionally implemented interface: the plugin hands
// over its own documentation and the platform renders it as-is. That way "where do I get this key" and
// "what should I watch out for" travel with the plugin's code, rather than being scattered across a
// hard-coded table in the platform frontend, a credential field's placeholder and the author's memory —
// which is exactly where they live today.
type DocHost interface {
	SetDoc(markdown, url string)
}

// DeclareDoc hands over the guide when the host supports one and silently skips otherwise, so the same
// code serves the in-process and remote cases.
//
// Supply either markdown or url: write the text here, or point at an existing documentation site — a
// copy pasted in here will eventually disagree with the site.
func DeclareDoc(h Host, markdown, url string) {
	if dh, ok := h.(DocHost); ok {
		dh.SetDoc(markdown, url)
	}
}

// CapabilityHost is a host that can accept self-reported optional capabilities.
//
// Same pattern as DocHost. It answers the half of the question that "does this operation exist" leaves
// open: two implementations of the same operation can differ enormously in what they actually manage —
// storage plugins all offer keyword_query, but one backs it with a properly tokenised BM25 and another
// with an approximation by similarity. Unreported, the platform can only ignore the difference
// silently, so a user's field weighting has no effect whatsoever — which is worse than "unsupported".
type CapabilityHost interface {
	SetCapabilities(caps map[string]bool)
}

// DeclareCapabilities records the capability bits when the host supports them and silently skips
// otherwise, so the same code serves the in-process and remote cases.
func DeclareCapabilities(h Host, caps map[string]bool) {
	if ch, ok := h.(CapabilityHost); ok {
		ch.SetCapabilities(caps)
	}
}

// Invoke is one call. raw is the input JSON, which the generated code decodes into concrete types, and
// output goes through the Sink.
type Invoke func(ctx Ctx, raw json.RawMessage, out Sink) error

// Ctx is the runtime capability available during one call.
//
// Each transport implements it differently but with identical semantics — Upload, for instance, streams
// chunks back to the platform over NATS and writes straight to the storage layer in-process. A handler
// need not know where it is running.
type Ctx interface {
	context.Context
	// Credential is this call's credential fields, resolved and sent by the platform; nil when there is
	// no credential.
	Credential() map[string]string
	// Upload stores bytes in the platform's file layer and returns a file reference, which may be handed
	// downstream as an output directly.
	Upload(name, mime string, data []byte) (*File, error)
	// UploadReader does the same but **streams while reading**: memory use is one chunk (1 MiB),
	// independent of the file's size.
	//
	// Anything above a few hundred MB — a video on a NAS, an archive — must go this way: Upload requires
	// the whole file in memory first, which is not "a bit slower" but the plugin process bursting.
	UploadReader(name, mime string, r io.Reader) (*File, error)
	// Fetch retrieves a file's bytes. File.Blob is the method form of it, and plugins usually write
	// f.Blob(ctx).
	Fetch(f *File) ([]byte, error)
}

// Sink is the output. Calling it repeatedly produces multiple frames (streaming); for a non-streaming
// operation the transport buffers and merges them.
type Sink interface {
	// Vars emits typed output variables for downstream nodes, named by their sokel tags.
	Vars(v any)
	// Text emits human-readable text for display and tracing; it does not become a downstream variable.
	Text(s string)
	// JSON emits a structured display value.
	JSON(v any)
}

// File is a platform file reference. It is **only data** — reading the bytes goes through Ctx, since
// that depends on the transport.
//
// The json tags line up with the platform's file value shape, so it can be handed out as an output
// field directly.
type File struct {
	ID   string `json:"id,omitempty"`   // the platform file id (f_...)
	URL  string `json:"url,omitempty"`  // the platform download path (/api/v1/files/<id>)
	Name string `json:"name,omitempty"` // the file name
	Mime string `json:"mime,omitempty"` // the MIME type
	// Size must not be omitempty: zero bytes is a value that **has to be visible**. It used to disappear
	// entirely when size=0, leaving a downstream empty-file gate of "file.size > 0" with nothing to
	// reference — as observed, an empty download's output had no size key at all, so a user copying the
	// condition from a successful sample that did have one could never make it match.
	Size int64  `json:"size"`           // the byte count
	Data []byte `json:"data,omitempty"` // inline bytes as a fallback (small files and tests; empty on the normal path)
}

// FileRef implements contract.FileRef, letting the contract package recognise this as a file field.
func (f *File) FileRef() {}

// Blob reads a file's bytes, preferring inline Data and otherwise asking the transport to pull them
// from the platform's file layer. Being a method is merely convenient — the work is ctx's, because
// reading bytes depends on the transport.
func (f *File) Blob(ctx Ctx) ([]byte, error) {
	if f == nil {
		return nil, errors.New("nil file")
	}
	if len(f.Data) > 0 {
		return f.Data, nil
	}
	return ctx.Fetch(f)
}

// —— event sources ——
//
// Events work like operations: the implementation declares only which events exist and how to push
// them, and who carries them is the transport's business.

// EventHost is the host an event contract is declared to.
type EventHost interface {
	// DeclareEvent declares one event.
	DeclareEvent(e contract.Event)
	// DeclareEventsCommon declares the fields every event shares; the platform flattens them to the top
	// level of the trigger input.
	DeclareEventsCommon(fields []contract.Field, names []string)
}

// SourceCtx is the runtime capability available to a long-running event source: Ctx plus two more,
// pushing an event and updating the credential.
type SourceCtx interface {
	Ctx
	// Trigger pushes one event. eventID is what the platform deduplicates on, so pushing the same
	// upstream message twice triggers once.
	//
	// payload takes any rather than a map: the transport expands a struct into contract names by its sokel
	// tags, the same machinery as Sink.Vars, and type safety is guaranteed one layer out by the generated
	// TriggerXxx — narrowing it again here would only add a conversion to the generated code.
	Trigger(event, eventID string, payload any) error
	// UpdateCredential writes credential fields back, a refreshed token for instance.
	UpdateCredential(patch map[string]string) error
	// ReportStatus reports this source's status, carried back on the heartbeat and visible in the
	// credential list.
	ReportStatus(status, msg string)
}
