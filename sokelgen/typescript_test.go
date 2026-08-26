// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// The TS execution contract has to carry itemType, the array element type: dropping it here cuts off the
// source of the frontend's arity check (a file list vs a single file, web docs/type-system.md §11).
// RenderTS used to emit only name, type and required, so an itemType that the Go contract always had was
// thrown away on the way to TS.
func TestRenderTSKeepsItemType(t *testing.T) {
	ops := []OpIO{{
		OpID:  "send",
		Label: "Send",
		Inputs: []Field{
			{Name: "files", Type: "array", ItemType: "file", Required: true},
			{Name: "note", Type: "string"},
		},
	}}
	got, err := RenderTS("test/pkg", ops)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `itemType: "file"`) {
		t.Errorf("an array parameter's itemType did not reach the TS contract:\n%s", got)
	}
	if strings.Contains(got, `"note", type: "string", required: false, itemType`) {
		t.Errorf("a parameter without an itemType should not carry the key:\n%s", got)
	}
	if !strings.Contains(got, "itemType?: string") {
		t.Errorf("the KernelParam interface has no itemType field:\n%s", got)
	}
}
