package sokelgen

import (
	"encoding/json"
	"testing"
)

// sokel-gen 的核心：从**源码**（AST）推导契约，而不是运行时反射。
// 第一条底线是与反射版本等价——tag 语义、类型映射、必填判定必须一字不差，
// 否则换成 codegen 就是一次静默的行为变更（docs/plugin-sdk-multilang.md §1）。
const src = `package main

type slot struct {
	Path string ` + "`sokel:\"path\"`" + `
}

// SendMessageIn 发送消息的入参。
type SendMessageIn struct {
	// 目标对话 id。
	ChatID string ` + "`sokel:\"chat_id\" label:\"对话 ID\"`" + `
	Text   string ` + "`sokel:\"text\" desc:\"正文\"`" + `
	Parse  string ` + "`sokel:\"parse_mode,optional\" enum:\"HTML,Markdown\"`" + `
	Count  int    ` + "`sokel:\"count\" default:\"3\"`" + `
	Silent bool   ` + "`sokel:\"silent,optional\"`" + `
	Slots  map[string]slot ` + "`sokel:\"slots\"`" + `
	Loose  map[string]any  ` + "`sokel:\"loose\"`" + `
	Nested slot            ` + "`sokel:\"nested\"`" + `
	Skip   string          ` + "`sokel:\"-\"`" + `
	hidden string
}
`

func TestParseStructFields(t *testing.T) {
	fields, err := ParseStructFields(src, "SendMessageIn")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	idx := map[string]Field{}
	for _, f := range fields {
		idx[f.Name] = f
	}
	dump, _ := json.Marshal(fields)

	// sokel tag 的对外名 + label
	if f := idx["chat_id"]; f.Label != "对话 ID" || f.Type != "string" {
		t.Errorf("chat_id 应取 sokel 名与 label: %s", dump)
	}
	// 未导出字段与 sokel:"-" 一律不出现
	if _, ok := idx["hidden"]; ok {
		t.Errorf("未导出字段不该进契约: %s", dump)
	}
	if _, ok := idx["skip"]; ok {
		t.Errorf("sokel:\"-\" 应跳过: %s", dump)
	}
	// enum tag：类型改 enum + 候选值
	if f := idx["parse_mode"]; f.Type != "enum" || len(f.Options) != 2 || f.Options[0].Value != "HTML" {
		t.Errorf("enum tag 应产出候选值: %s", dump)
	}
	// 必填判定与反射同规则：非 optional、无 default、非指针
	if !idx["chat_id"].Required {
		t.Errorf("chat_id 应必填: %s", dump)
	}
	if idx["parse_mode"].Required {
		t.Errorf("optional 不该必填: %s", dump)
	}
	if idx["count"].Required {
		t.Errorf("有 default 不该必填: %s", dump)
	}
	// 类型映射
	if idx["count"].Type != "number" || idx["silent"].Type != "boolean" {
		t.Errorf("标量类型映射错: %s", dump)
	}
	// map[string]T → valueType 递归；map[string]any → opaque
	if vt := idx["slots"].ValueType; vt == nil || len(vt.Fields) != 1 || vt.Fields[0].Name != "path" {
		t.Errorf("map[string]T 应展开 valueType: %s", dump)
	}
	if !idx["loose"].Opaque {
		t.Errorf("map[string]any 应标 opaque: %s", dump)
	}
	// struct → 递归展开 fields
	if len(idx["nested"].Fields) != 1 {
		t.Errorf("struct 应递归展开: %s", dump)
	}

	// —— AST 独有：字段上方的注释即 desc（反射永远拿不到）——
	if idx["chat_id"].Desc != "目标对话 id。" {
		t.Errorf("字段注释应成为 desc: %q", idx["chat_id"].Desc)
	}
	// 显式 desc tag 优先于注释
	if idx["text"].Desc != "正文" {
		t.Errorf("desc tag 应优先: %q", idx["text"].Desc)
	}
}

// 数组元素无结构时，array 本身也该标 opaque —— 否则「[]map[string]any」和
// 「[]SomeStruct 但恰好没有导出字段」在契约上分不清，平台无从判断该不该做结构校验。
// 触发点：report-pipeline 的 `type tag = map[string]any` + `[]tag`。
func TestArrayOpaqueFromElement(t *testing.T) {
	src := `package main

type row struct {
	Name string ` + "`sokel:\"name\"`" + `
}
type loose = map[string]any

type In struct {
	Rows   []row    ` + "`sokel:\"rows\"`" + `   // 元素有结构 → 不是 opaque
	Blobs  []loose  ` + "`sokel:\"blobs\"`" + `  // 元素是裸 map → opaque
	Anys   []any    ` + "`sokel:\"anys\"`" + `   // 元素是 any → opaque
	Names  []string ` + "`sokel:\"names\"`" + `  // 标量元素：类型明确，不该被当成 opaque
}
`
	fields, err := ParseStructFields(src, "In")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	idx := map[string]Field{}
	for _, f := range fields {
		idx[f.Name] = f
	}
	if idx["rows"].Opaque || len(idx["rows"].Fields) != 1 {
		t.Errorf("元素有结构的数组不该 opaque: %+v", idx["rows"])
	}
	if !idx["blobs"].Opaque {
		t.Errorf("[]map[string]any 应标 opaque: %+v", idx["blobs"])
	}
	if !idx["anys"].Opaque {
		t.Errorf("[]any 应标 opaque: %+v", idx["anys"])
	}
	if idx["names"].Opaque {
		t.Errorf("标量元素的数组不是 opaque（元素类型是明确的）: %+v", idx["names"])
	}
}

// 整数识别必须两条路径一致：反射侧早就记了宽度，AST 侧（反向迁移用）此前漏了 ——
// 于是旧代码里的 `ContentID int` 迁过来变成 float64，实现里凭空多一道转换。
func TestParseIntKind(t *testing.T) {
	src := `package main

type In struct {
	N     int     ` + "`sokel:\"n\"`" + `
	Big   int64   ` + "`sokel:\"big\"`" + `
	Ratio float64 ` + "`sokel:\"ratio\"`" + `
	Sizes []int   ` + "`sokel:\"sizes\"`" + `
}
`
	idx := map[string]Field{}
	fs, err := ParseStructFields(src, "In")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		idx[f.Name] = f
	}
	if idx["n"].GoType != "int" {
		t.Errorf("int 字段应记住整数: %+v", idx["n"])
	}
	if idx["big"].GoType != "int64" {
		t.Errorf("宽度要保留（降成 int 会丢精度）: %+v", idx["big"])
	}
	if idx["ratio"].GoType != "" {
		t.Errorf("浮点不该带整数提示: %+v", idx["ratio"])
	}
	if idx["sizes"].GoType != "int" {
		t.Errorf("整数数组的元素类型: %+v", idx["sizes"])
	}
}
