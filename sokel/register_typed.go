// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// typedSink is what an operation handler emits through, seen from outside as plugin.Sink.
//
// The inner emitterCore has unexported methods and cannot be implemented outside this package, so
// this exposes only "how to emit". It takes any: type safety belongs to the generated OnXxx one
// layer out, and the library itself needs no generics at all.
type typedSink struct{ core emitterCore }

// Vars emits typed output variables. Field names come from the sokel tag.
func (s typedSink) Vars(v any) {
	if m := structToVars(v); len(m) > 0 {
		s.core.emit(frame{Kind: frameVars, Vars: m})
	}
}

// Text emits human-readable text (display / tracing).
func (s typedSink) Text(str string) { s.core.emit(frame{Kind: frameText, Text: str}) }

// JSON emits structured JSON (display / tracing).
func (s typedSink) JSON(v any) { s.core.emit(frame{Kind: frameJSON, JSON: v}) }

// RegisterOp registers an operation. **The caller supplies the whole contract**, from the schema
// declaration rather than reflection.
//
// Unlike the older Register, this has no generics and no reflection. Type safety lives in the
// generated OnXxx: it decodes raw into a concrete In, calls a handler with a concrete signature, and
// hands the Out to the Sink.
func RegisterOp(p *Plugin, op contract.Operation, inv plugin.Invoke) {
	// The same rule Register applies; generated registrations come here too. A capability-slot
	// operation is exempt: its wire id is derived by the platform from the slot and carries the
	// capability path (rowstore.query), so the dot is expected there.
	if op.Capability == "" {
		mustBusinessOpID(op.ID)
	}
	if op.Inputs == nil {
		op.Inputs = []contract.Field{} // an empty array rather than null, so nothing downstream guards against null
	}
	if op.Outputs == nil {
		op.Outputs = []contract.Field{}
	}
	p.ops = append(p.ops, opEntry{
		op: op,
		invoke: func(ctx Ctx, input json.RawMessage, sink emitterCore) (err error) {
			// A panicking handler becomes an error frame rather than taking the plugin process down.
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("operation %q panicked: %v\n%s", op.ID, r, debug.Stack())
				}
			}()
			return inv(ctx, input, typedSink{core: sink})
		},
	})
}
