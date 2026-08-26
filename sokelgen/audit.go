// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import "fmt"

// OpaqueReport is one structureless field.
type OpaqueReport struct {
	Path   string // e.g. chunks_upsert.in.chunks.fields
	Reason string // empty means no reason was given for giving up on structure
	// AnyValue distinguishes Any (not even known to be an object) from Object (an object with
	// open-ended keys). The two are reported separately: Any is the weaker one and deserves the harder
	// question, and lumping them together would make them look equally serious, which they are not.
	AnyValue bool
}

// AuditOpaque lists every structureless field in the contract, nested ones included.
//
// The aim is to keep weak typing to a minimum: every opaque should be **a conscious decision** rather
// than the lazy default. Ones with no stated reason get a generator warning — not a hard stop (some
// genuinely cannot be described), but visible.
func AuditOpaque(ops []OpIO) []OpaqueReport {
	var out []OpaqueReport
	var walk func(fs []Field, path string)
	walk = func(fs []Field, path string) {
		for _, f := range fs {
			p := path + "." + f.Name
			if f.Opaque {
				out = append(out, OpaqueReport{Path: p, Reason: f.Desc, AnyValue: len(f.Types) > 1})
			}
			walk(f.Fields, p)
			if f.ValueType != nil {
				vt := *f.ValueType
				vt.Name = "<value>"
				walk([]Field{vt}, p)
			}
			for _, v := range f.OneOf {
				walk(v.Fields, p+"("+v.Name+")")
			}
		}
	}
	for _, op := range ops {
		walk(op.Inputs, op.OpID+".in")
		walk(op.Outputs, op.OpID+".out")
	}
	return out
}

// FormatOpaqueWarnings renders the unexplained structureless fields as warning text, returning an
// empty string when every one of them has a reason.
func FormatOpaqueWarnings(reports []OpaqueReport) string {
	var bad []OpaqueReport
	for _, r := range reports {
		if r.Reason == "" {
			bad = append(bad, r)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	s := fmt.Sprintf("sokel-gen: %d field(s) declare no structure and give no reason:\n", len(bad))
	for _, r := range bad {
		kind := "an object with open-ended keys"
		if r.AnyValue {
			kind = "any value — not even known to be an object"
		}
		s += "  " + r.Path + " (" + kind + ")\n"
	}
	s += "  -> Fill in the structure where you can (usually you can); where you truly cannot, use\n"
	s += "     field.Object(name, reason) or field.Any(name, reason) — the latter is weaker, so make sure\n     the kind really is undecidable before reaching for it.\n"
	return s
}

// ShapelessArray is one array that **fails to state the shape of its elements**.
type ShapelessArray struct {
	Path string
	Desc string // the field's description (often the very sentence that belonged in Desc but was passed as the shape argument)
}

// AuditArrays lists every array field whose element shape is unclear.
//
// The test: no sub-fields (object elements), no ItemType (scalar elements), no OneOf (one of several
// shapes) and no Opaque (**an explicit admission** of no structure) — with all four absent, the
// declaration is simply missing.
//
// Why this deserves its own audit: field.Array's second argument is the element shape and its type is
// any, so passing a description instead — field.Array("messages", "the mail list") — **compiles**, and
// silently yields a structureless array. Downstream, messages[0] is a mystery: the variable picker
// cannot expand it, references go unchecked, and paths have to be typed by hand. One version of the
// gmail plugin shipped exactly that, and nobody noticed until a user reported it.
func AuditArrays(ops []OpIO) []ShapelessArray {
	var out []ShapelessArray
	var walk func(fs []Field, path string)
	walk = func(fs []Field, path string) {
		for _, f := range fs {
			p := path + "." + f.Name
			if f.Type == "array" && len(f.Fields) == 0 && f.ItemType == "" && len(f.OneOf) == 0 && !f.Opaque {
				out = append(out, ShapelessArray{Path: p, Desc: f.Desc})
			}
			walk(f.Fields, p)
			if f.ValueType != nil {
				vt := *f.ValueType
				vt.Name = "<value>"
				walk([]Field{vt}, p)
			}
			for _, v := range f.OneOf {
				walk(v.Fields, p+"("+v.Name+")")
			}
		}
	}
	for _, op := range ops {
		walk(op.Inputs, op.OpID+".in")
		walk(op.Outputs, op.OpID+".out")
	}
	return out
}

// FormatArrayWarnings renders arrays with an unclear element shape as warning text, returning an
// empty string when they are all clear.
func FormatArrayWarnings(reports []ShapelessArray) string {
	if len(reports) == 0 {
		return ""
	}
	s := fmt.Sprintf("sokel-gen: %d array(s) do not state their element shape:\n", len(reports))
	for _, r := range reports {
		line := "  " + r.Path
		if r.Desc != "" {
			line += " (" + r.Desc + ")"
		}
		s += line + "\n"
	}
	s += "  -> Object elements: field.Array(name, []Item{}); scalars: []string{} / []int{};\n"
	s += "     one of several shapes: field.ArrayOf(name, A{}, B{}); genuinely no structure:\n     []map[string]any{} (recorded as opaque).\n"
	s += "     Note that the second argument is **the element shape**, not a description — descriptions go\n     in .Desc(...), and passing one here silently drops the structure.\n"
	return s
}
