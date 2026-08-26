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
		out.Vars(Out{V: *in.N}) // in.N 为 nil 时 panic
		return nil
	})
	_, err := p.invokeBuffered(natsCtx{Context: context.Background()}, "boom", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("panic 应被 recover 成 error，而非崩溃/静默")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("错误信息应含 panic 线索，got: %v", err)
	}
}
