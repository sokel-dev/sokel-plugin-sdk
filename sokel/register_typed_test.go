// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"strings"
	"testing"
)

type capSink struct{ frames []frame }

func (c *capSink) emit(f frame) { c.frames = append(c.frames, f) }

// RegisterOp is where generated code lands: no generics, with the caller supplying the whole contract
// from the schema declaration.
func TestRegisterOpInvoke(t *testing.T) {
	p := &Plugin{}
	op := Operation{ID: "echo", Inputs: []Field{{Name: "msg", Type: TString}}}

	RegisterOp(p, op, func(ctx Ctx, raw json.RawMessage, out Sink) error {
		var in struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		out.Vars(struct {
			Reply string `sokel:"reply"`
		}{Reply: "got:" + in.Msg})
		return nil
	})

	if len(p.ops) != 1 || p.ops[0].op.ID != "echo" {
		t.Fatalf("one operation should be registered: %+v", p.ops)
	}
	sink := &capSink{}
	if err := p.ops[0].invoke(natsCtx{}, json.RawMessage(`{"msg":"hi"}`), sink); err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	if len(sink.frames) != 1 || sink.frames[0].Vars["reply"] != "got:hi" {
		t.Errorf("wrong output: %+v", sink.frames)
	}
}

// No outputs must not be reported as null, so neither the platform nor the frontend's contract view
// guards against null.
func TestRegisterOpNilContract(t *testing.T) {
	p := &Plugin{}
	RegisterOp(p, Operation{ID: "noop"}, func(Ctx, json.RawMessage, Sink) error { return nil })
	if p.ops[0].op.Inputs == nil || p.ops[0].op.Outputs == nil {
		t.Errorf("an empty contract should be an empty array rather than null: %+v", p.ops[0].op)
	}
}

// A panicking handler must not take down the whole plugin process; it becomes a readable error.
func TestRegisterOpPanicRecovered(t *testing.T) {
	p := &Plugin{}
	RegisterOp(p, Operation{ID: "boom"}, func(Ctx, json.RawMessage, Sink) error {
		panic("boom")
	})
	err := p.ops[0].invoke(natsCtx{}, nil, &capSink{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a panic should become an error: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("the error should name the operation: %v", err)
	}
}
