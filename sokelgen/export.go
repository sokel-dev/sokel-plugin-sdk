// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import "encoding/json"

// ExportContract 把声明导出为**语言中立**的契约 JSON（协议 §5 的 Operation 形状）。
//
// 刻意不带 SchemaType / InType / OutType —— 那是 Go 代码生成的内部信息，
// 而这份 JSON 是给 Python / TS / Rust 的生成器吃的，塞 Go 类型名只会让它们困惑。
// Field 里的 goType 同理是生成提示，其他语言忽略即可（已在协议里注明）。
func ExportContract(ops []OpIO) ([]byte, error) {
	type operation struct {
		ID      string  `json:"id"`
		Label   string  `json:"label,omitempty"`
		Desc    string  `json:"desc,omitempty"`
		Stream  bool    `json:"stream,omitempty"`
		Inputs  []Field `json:"inputs"`
		Outputs []Field `json:"outputs"`
	}
	out := make([]operation, 0, len(ops))
	for _, o := range ops {
		in, outs := o.Inputs, o.Outputs
		if in == nil {
			in = []Field{} // 空数组而非 null：下游生成器不必防 null
		}
		if outs == nil {
			outs = []Field{}
		}
		out = append(out, operation{
			ID: o.OpID, Label: o.Label, Desc: o.Desc, Stream: o.Stream,
			Inputs: in, Outputs: outs,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}
