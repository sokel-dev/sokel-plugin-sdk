package sokel

import (
	"encoding/json"
	"strings"
	"testing"
)

type capSink struct{ frames []frame }

func (c *capSink) emit(f frame) { c.frames = append(c.frames, f) }

// RegisterOp 是生成代码的落点：零泛型、契约由调用方给全（来自 schema 声明）。
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
		t.Fatalf("应注册一个操作: %+v", p.ops)
	}
	sink := &capSink{}
	if err := p.ops[0].invoke(natsCtx{}, json.RawMessage(`{"msg":"hi"}`), sink); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(sink.frames) != 1 || sink.frames[0].Vars["reply"] != "got:hi" {
		t.Errorf("产出不对: %+v", sink.frames)
	}
}

// 出参为空时不该上报 null —— 平台/前端契约视图不必防 null。
func TestRegisterOpNilContract(t *testing.T) {
	p := &Plugin{}
	RegisterOp(p, Operation{ID: "noop"}, func(Ctx, json.RawMessage, Sink) error { return nil })
	if p.ops[0].op.Inputs == nil || p.ops[0].op.Outputs == nil {
		t.Errorf("空契约应为空数组而非 null: %+v", p.ops[0].op)
	}
}

// handler panic 不该崩掉整个插件进程，转成可读 error。
func TestRegisterOpPanicRecovered(t *testing.T) {
	p := &Plugin{}
	RegisterOp(p, Operation{ID: "boom"}, func(Ctx, json.RawMessage, Sink) error {
		panic("炸了")
	})
	err := p.ops[0].invoke(natsCtx{}, nil, &capSink{})
	if err == nil || !strings.Contains(err.Error(), "炸了") {
		t.Errorf("panic 应转成 error: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("报错应指名是哪个操作: %v", err)
	}
}
