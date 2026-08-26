// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// The weak-typing audit: every opaque should be a conscious decision, and the ones without a stated
// reason have to be visible. The aim is to keep opaque fields to a minimum rather than let them pile up
// quietly.
func TestAuditOpaque(t *testing.T) {
	ops := []OpIO{{
		OpID: "upsert",
		Inputs: []Field{
			{Name: "blob", Type: "json", Opaque: true},                                                   // no reason
			{Name: "meta", Type: "json", Opaque: true, Desc: "the keys depend on this library's config"}, // has a reason
			{Name: "chunks", Type: "array", Fields: []Field{
				{Name: "fields", Type: "json", Opaque: true}, // nested ones must be caught too
			}},
			{Name: "boosts", Type: "json", ValueType: &Field{Type: "json", Opaque: true}}, // inside a valueType
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{
				{Name: "A", Type: "json", Fields: []Field{{Name: "x", Type: "json", Opaque: true}}}, // inside a oneOf branch
			}},
		},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}}

	rs := AuditOpaque(ops)
	if len(rs) != 5 {
		t.Fatalf("expected 5 opaque fields, nesting, valueType and oneOf branches included: %+v", rs)
	}
	paths := map[string]string{}
	for _, r := range rs {
		paths[r.Path] = r.Reason
	}
	for _, want := range []string{
		"upsert.in.blob", "upsert.in.meta",
		"upsert.in.chunks.fields",  // nested
		"upsert.in.boosts.<value>", // inside a valueType
		"upsert.in.doc(A).x",       // inside a oneOf branch
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("missed %s: %+v", want, rs)
		}
	}

	w := FormatOpaqueWarnings(rs)
	if !strings.Contains(w, "4 field(s)") {
		t.Errorf("the one with a reason should not be counted in the warning: %s", w)
	}
	if strings.Contains(w, "upsert.in.meta") {
		t.Errorf("the one with a reason should not appear in the warning: %s", w)
	}
	// A warning has to offer a way out rather than merely announce a problem
	if !strings.Contains(w, "field.Object") || !strings.Contains(w, "Fill in the structure") {
		t.Errorf("the warning should say what to do: %s", w)
	}

	// Say nothing when every one of them has a reason
	if FormatOpaqueWarnings([]OpaqueReport{{Path: "a", Reason: "genuinely dynamic"}}) != "" {
		t.Error("no warning is due when every one has a reason")
	}
}

// The audit distinguishes two levels: Object (an object with open-ended keys) and Any (not even the
// kind is known). Any is weaker and deserves the harder question — reported together, "anything at all"
// and "an object with open-ended keys" would look equally serious, and they are not.
func TestAuditDistinguishesAnyFromObject(t *testing.T) {
	ops := []OpIO{{OpID: "req", Inputs: []Field{
		{Name: "meta", Type: "json", Opaque: true, Desc: "metadata passed through from upstream"},
		{Name: "body", Type: "json", Opaque: true, Types: []string{"json", "array", "string"}, Desc: "raw mode yields a string"},
	}}}
	reports := AuditOpaque(ops)
	if len(reports) != 2 {
		t.Fatalf("both should be reported: %+v", reports)
	}
	byPath := map[string]OpaqueReport{}
	for _, r := range reports {
		byPath[r.Path] = r
	}
	if byPath["req.in.meta"].AnyValue {
		t.Error("meta is an object and should not be flagged as any")
	}
	if !byPath["req.in.body"].AnyValue {
		t.Error("body declares several types and should be flagged as any")
	}
}

// An array that omits its element shape does so **silently**: field.Array's shape argument is any, so
// field.Array("messages", "the mail list") compiles and yields a structureless array, leaving
// downstream with no idea what messages[0] contains. One version of the gmail plugin shipped exactly
// that.
func TestAuditArrays(t *testing.T) {
	ops := []OpIO{{
		OpID: "gmail_list",
		Outputs: []Field{
			{Name: "shapeless", Type: "array", Desc: "the mail list"},                       // <- missing
			{Name: "objects", Type: "array", Fields: []Field{{Name: "id", Type: "string"}}}, // object elements
			{Name: "scalars", Type: "array", ItemType: "string"},                            // scalar elements
			{Name: "opaque", Type: "array", Opaque: true},                                   // an explicit admission
			{Name: "union", Type: "array", OneOf: []OneOfVariant{{Name: "A"}}},              // one of several shapes
			{Name: "notArray", Type: "string"},
		},
	}}
	got := AuditArrays(ops)
	if len(got) != 1 || got[0].Path != "gmail_list.out.shapeless" {
		t.Fatalf("only the one missing a declaration should be reported: %+v", got)
	}
	// The report carries the description, which is usually the very sentence that belonged in Desc but
	// was passed as the shape argument
	if got[0].Desc != "the mail list" {
		t.Errorf("the description should be carried so the field is recognisable: %+v", got[0])
	}
	w := FormatArrayWarnings(got)
	if w == "" || !strings.Contains(w, "field.Array(name, []Item{})") {
		t.Errorf("the warning should say how to fix it: %q", w)
	}
	if FormatArrayWarnings(nil) != "" {
		t.Error("nothing should be emitted when there is no problem")
	}
}

// Nested arrays are audited too: a structureless array inside an object field has identical symptoms.
func TestAuditArraysWalksNested(t *testing.T) {
	ops := []OpIO{{
		OpID: "x",
		Inputs: []Field{{Name: "payload", Type: "json", Fields: []Field{
			{Name: "items", Type: "array"},
		}}},
	}}
	if got := AuditArrays(ops); len(got) != 1 || got[0].Path != "x.in.payload.items" {
		t.Errorf("nested arrays must be reported too: %+v", got)
	}
}
