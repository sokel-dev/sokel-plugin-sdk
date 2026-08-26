// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import "fmt"

// OpaqueReport 一处无结构字段。
type OpaqueReport struct {
	Path   string // 如 chunks_upsert.in.chunks.fields
	Reason string // 为空 = 没说明为什么放弃结构
	// AnyValue：是 Any（连是不是对象都不定）还是 Object（是对象、键不定）。
	// 两档分开报：Any 更弱，更该被追问；混在一起会显得一样重，而它们不一样。
	AnyValue bool
}

// AuditOpaque 列出契约里所有无结构字段（含嵌套）。
//
// 目标是把弱类型压到最少：每处 opaque 都该是**有意识的决定**，而不是图省事的默认值。
// 没写理由的会被生成器警告——不阻断（有些确实说不清），但要让人看见。
func AuditOpaque(ops []OpIO) []OpaqueReport {
	var out []OpaqueReport
	var walk func(fs []Field, path string)
	walk = func(fs []Field, path string) {
		for _, f := range fs {
			p := path + "." + f.Name
			if f.Opaque {
				out = append(out, OpaqueReport{Path: p, Reason: f.Desc, AnyValue: len(f.Types) > 1})
			}
			walk(f.Fields, p)
			if f.ValueType != nil {
				vt := *f.ValueType
				vt.Name = "<value>"
				walk([]Field{vt}, p)
			}
			for _, v := range f.OneOf {
				walk(v.Fields, p+"("+v.Name+")")
			}
		}
	}
	for _, op := range ops {
		walk(op.Inputs, op.OpID+".in")
		walk(op.Outputs, op.OpID+".out")
	}
	return out
}

// FormatOpaqueWarnings 把没说明理由的无结构字段格式化成警告文本；全都有理由时返回空串。
func FormatOpaqueWarnings(reports []OpaqueReport) string {
	var bad []OpaqueReport
	for _, r := range reports {
		if r.Reason == "" {
			bad = append(bad, r)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	s := fmt.Sprintf("sokel-gen: 有 %d 处字段没有结构声明，也没说明理由：\n", len(bad))
	for _, r := range bad {
		kind := "对象但键不定"
		if r.AnyValue {
			kind = "任意值，连是不是对象都不定"
		}
		s += "  " + r.Path + "（" + kind + "）\n"
	}
	s += "  → 能补出结构就补（多数情况可以）；确实说不清的，用 field.Object(名, 理由)\n"
	s += "    或 field.Any(名, 理由)——后者更弱，用之前先确认真的连类别都定不了。\n"
	return s
}

// ShapelessArray 一处**没说清元素形状**的数组。
type ShapelessArray struct {
	Path string
	Desc string // 该字段的说明（多半就是本该写在 Desc 里、却被塞进形状参数的那句话）
}

// AuditArrays 列出所有「元素形状不明」的数组字段。
//
// 判据：既没有子字段（对象元素）、也没有 ItemType（标量元素）、没有 OneOf（几种形状之一）、
// 也没有 Opaque（**明确承认**没有结构）——四样全无，就是漏声明。
//
// 为什么要专门审这一条：field.Array 的第二个参数是元素形状，而它的类型是 any，
// 于是 `field.Array("messages", "邮件列表")` 这种把描述当形状传的写法**编译得过**，
// 静默产出一个无结构数组。下游拿到 messages[0] 不知道里面有什么：变量选择器展不开、
// 引用没有校验、只能手写路径。gmail 就这么错了一版，直到用户报上来才发现。
func AuditArrays(ops []OpIO) []ShapelessArray {
	var out []ShapelessArray
	var walk func(fs []Field, path string)
	walk = func(fs []Field, path string) {
		for _, f := range fs {
			p := path + "." + f.Name
			if f.Type == "array" && len(f.Fields) == 0 && f.ItemType == "" && len(f.OneOf) == 0 && !f.Opaque {
				out = append(out, ShapelessArray{Path: p, Desc: f.Desc})
			}
			walk(f.Fields, p)
			if f.ValueType != nil {
				vt := *f.ValueType
				vt.Name = "<value>"
				walk([]Field{vt}, p)
			}
			for _, v := range f.OneOf {
				walk(v.Fields, p+"("+v.Name+")")
			}
		}
	}
	for _, op := range ops {
		walk(op.Inputs, op.OpID+".in")
		walk(op.Outputs, op.OpID+".out")
	}
	return out
}

// FormatArrayWarnings 把元素形状不明的数组格式化成警告文本；都说清了就返回空串。
func FormatArrayWarnings(reports []ShapelessArray) string {
	if len(reports) == 0 {
		return ""
	}
	s := fmt.Sprintf("sokel-gen: 有 %d 个数组没说清元素形状：\n", len(reports))
	for _, r := range reports {
		line := "  " + r.Path
		if r.Desc != "" {
			line += "（" + r.Desc + "）"
		}
		s += line + "\n"
	}
	s += "  → 对象元素：field.Array(名, []Item{})；标量：[]string{} / []int{}；\n"
	s += "    几种形状之一：field.ArrayOf(名, A{}, B{})；确实没有结构：[]map[string]any{}（会记为 opaque）。\n"
	s += "    注意第二个参数是**元素形状**不是描述——描述写 .Desc(…)，传进去只会静默丢掉结构。\n"
	return s
}
