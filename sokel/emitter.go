// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// frameKind is the kind of one emitted frame.
type frameKind string

const (
	frameText frameKind = "text"      // human-readable text
	frameJSON frameKind = "json"      // structured JSON, for display
	frameVars frameKind = "variables" // typed output variables: they flow downstream and are checked against Outputs
)

// frame is one emission. The Vars of a variables frame are merged into the node's output.
type frame struct {
	Kind frameKind      `json:"kind"`
	Text string         `json:"text,omitempty"`
	JSON any            `json:"json,omitempty"`
	Vars map[string]any `json:"vars,omitempty"`
}

// emitterCore is the sink each transport provides:
//   - non-streaming: buffer the frames and merge the variables into one reply when the handler returns;
//   - streaming: publish each frame to the reply subject, then a terminator.
type emitterCore interface {
	emit(f frame)
}

// Emitter is the typed emitter. Each call is one frame (streaming); for a non-streaming transport
// the SDK buffers and merges them.
type Emitter[Out any] struct {
	core emitterCore
}

// Text emits human-readable text (display / tracing).
func (e *Emitter[Out]) Text(s string) { e.core.emit(frame{Kind: frameText, Text: s}) }

// JSON emits structured JSON (display / tracing).
func (e *Emitter[Out]) JSON(v any) { e.core.emit(frame{Kind: frameJSON, JSON: v}) }

// Vars emits typed output variables. Field names come from the sokel tag; call it repeatedly and a
// later frame overwrites same-named fields.
func (e *Emitter[Out]) Vars(o Out) {
	if m := structToVars(o); len(m) > 0 {
		e.core.emit(frame{Kind: frameVars, Vars: m})
	}
}
