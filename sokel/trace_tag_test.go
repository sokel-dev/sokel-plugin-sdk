// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// traceTag has to recognise trace_id, the request-level trace of a model or plugin call that the
// platform has sent since 0137: the platform logs [tr_x] and the plugin logs [tr=tr_x], so the two sides
// can be reconciled by the same id.
import "testing"

func TestTraceTagCarriesTraceID(t *testing.T) {
	got := traceTag(map[string]string{"run_id": "run_1", "trace_id": "tr_abc"})
	want := " [run=run_1 tr=tr_abc]"
	if got != want {
		t.Errorf("traceTag = %q, want %q", got, want)
	}
	if traceTag(nil) != "" {
		t.Error("an empty context should give an empty string")
	}
}
