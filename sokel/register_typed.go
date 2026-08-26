// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
)

// Sink：产出汇聚点的导出视图，供**生成的注册函数**使用。
//
// 内层的 emitterCore 有未导出方法（包外实现不了），这里只把「怎么产出」暴露出去。
// 注意它收 any——类型安全由生成的 OnXxx 在外层保证，库里不需要泛型
// （docs/plugin-sdk-multilang.md §1「不需要泛型」）。
type Sink struct{ core emitterCore }

// Vars 产出类型化输出变量（进下游节点）。字段按 sokel tag 落为输出名。
func (s Sink) Vars(v any) {
	if m := structToVars(v); len(m) > 0 {
		s.core.emit(frame{Kind: frameVars, Vars: m})
	}
}

// Text 产出人类可读文本（展示 / tracing）。
func (s Sink) Text(str string) { s.core.emit(frame{Kind: frameText, Text: str}) }

// JSON 产出结构化 JSON（展示 / tracing）。
func (s Sink) JSON(v any) { s.core.emit(frame{Kind: frameJSON, JSON: v}) }

// Invoke：一次操作调用。raw 是平台传来的入参 JSON，由生成的代码解到具体类型。
type Invoke func(ctx Ctx, raw json.RawMessage, out Sink) error

// RegisterOp 注册一个操作。**契约由调用方给全**（来自 schema 声明，非反射推导）。
//
// 与旧的 Register 的区别：这里零泛型、零反射推导。类型安全在生成的 OnXxx 里，
// 它把 raw 解到具体的 In、调用具体签名的 handler、再把 Out 交给 Sink。
func RegisterOp(p *Plugin, op Operation, inv Invoke) {
	mustBusinessOpID(op.ID) // 与 Register 同一把尺子（codegen 生成的注册也走这里）
	if op.Inputs == nil {
		op.Inputs = []Field{} // 空数组而非 null：下游（平台/前端契约视图）不必防 null
	}
	if op.Outputs == nil {
		op.Outputs = []Field{}
	}
	p.ops = append(p.ops, opEntry{
		op: op,
		invoke: func(ctx Ctx, input json.RawMessage, sink emitterCore) (err error) {
			// handler panic 兜底：转成 error 帧，不让单次坏调用崩掉整个插件进程。
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("操作 %q 内部 panic: %v\n%s", op.ID, r, debug.Stack())
				}
			}()
			return inv(ctx, input, Sink{core: sink})
		},
	})
}
