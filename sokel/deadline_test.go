// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"testing"
	"time"
)

// The platform stops waiting after deadline_ms (and may retry). The handler's context must end at the
// same moment; otherwise the plugin finishes the upstream request anyway and a retry repeats its side
// effects (a second message sent, a second order placed) (F-327).
func TestCallContextFollowsTheDeadline(t *testing.T) {
	ctx, cancel := callContext(context.Background(), 50)
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > 50*time.Millisecond {
		t.Fatalf("handler context must end with the platform's deadline, got %v %v", dl, ok)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("handler context was not cancelled at the deadline")
	}

	ctx, cancel = callContext(context.Background(), 0) // an older platform sends none
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("no deadline_ms must mean no deadline")
	}
}
