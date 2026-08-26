// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import "encoding/json"

// ExportContract exports the declarations as **language-neutral** contract JSON (the Operation shape
// from §5 of the protocol).
//
// SchemaType, InType and OutType are deliberately left out: they are internal to Go code generation,
// whereas this JSON feeds the Python, TS and Rust generators, which would only be confused by Go type
// names. goType inside Field is likewise a generation hint that other languages may ignore (as the
// protocol notes).
func ExportContract(ops []OpIO) ([]byte, error) {
	type operation struct {
		ID      string  `json:"id"`
		Label   string  `json:"label,omitempty"`
		Desc    string  `json:"desc,omitempty"`
		Stream  bool    `json:"stream,omitempty"`
		Inputs  []Field `json:"inputs"`
		Outputs []Field `json:"outputs"`
	}
	out := make([]operation, 0, len(ops))
	for _, o := range ops {
		in, outs := o.Inputs, o.Outputs
		if in == nil {
			in = []Field{} // an empty array rather than null, so downstream generators need not guard against null
		}
		if outs == nil {
			outs = []Field{}
		}
		out = append(out, operation{
			ID: o.OpID, Label: o.Label, Desc: o.Desc, Stream: o.Stream,
			Inputs: in, Outputs: outs,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}
