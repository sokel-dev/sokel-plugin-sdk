// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// frameKind 产出帧类型（对齐 Dify create_*_message）。
type frameKind string

const (
	frameText frameKind = "text"      // 人类可读文本
	frameJSON frameKind = "json"      // 结构化 JSON（展示）
	frameVars frameKind = "variables" // 类型化输出变量（进下游节点，按契约 Outputs 校验）
)

// frame 一次产出。variables 帧的 Vars 会被平台并入节点输出。
type frame struct {
	Kind frameKind      `json:"kind"`
	Text string         `json:"text,omitempty"`
	JSON any            `json:"json,omitempty"`
	Vars map[string]any `json:"vars,omitempty"`
}

// emitterCore 由各传输提供的产出汇聚点：
//   - 非流式：缓冲各帧，handler 结束后合并 variables 作为单次回复；
//   - 流式：逐帧发布到回复通道 + 终止符。
type emitterCore interface {
	emit(f frame)
}

// Emitter 类型化产出器（Go 版 create_*_message）。多次调用 = 多帧（流式）；
// 非流式传输由 SDK 自动缓冲合并。
type Emitter[Out any] struct {
	core emitterCore
}

// Text 产出一条人类可读文本（展示 / tracing）。
func (e *Emitter[Out]) Text(s string) { e.core.emit(frame{Kind: frameText, Text: s}) }

// JSON 产出一段结构化 JSON（展示 / tracing）。
func (e *Emitter[Out]) JSON(v any) { e.core.emit(frame{Kind: frameJSON, JSON: v}) }

// Vars 产出类型化输出变量（进下游节点）。字段按 sokel tag 落为输出名；可多次调用（后帧覆盖同名字段）。
func (e *Emitter[Out]) Vars(o Out) {
	if m := structToVars(o); len(m) > 0 {
		e.core.emit(frame{Kind: frameVars, Vars: m})
	}
}
