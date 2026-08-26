// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHandlerPanicRecovered(t *testing.T) {
	p := New(Config{Endpoint: "x", Token: "t"})
	type In struct {
		N *int `sokel:"n,optional"`
	}
	type Out struct {
		V int `sokel:"v"`
	}
	Register(p, Operation{ID: "boom"}, func(_ Ctx, in In, out *Emitter[Out]) error {
		out.Vars(Out{V: *in.N}) // panics when in.N is nil
		return nil
	})
	_, err := p.invokeBuffered(natsCtx{Context: context.Background()}, "boom", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("a panic should be recovered into an error rather than crashing or passing silently")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("the error message should carry a hint of the panic, got: %v", err)
	}
}
