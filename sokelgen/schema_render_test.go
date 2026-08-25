package sokelgen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// 反向迁移：旧的 struct+tag 契约 → 新的 schema 声明代码。
// 生成的是**起点**——机器判断不了的地方要留下明确的人工提示，而不是猜一个填上。
func TestRenderSchema(t *testing.T) {
	ops := []OpIO{{
		OpID: "send", Label: "发送", Desc: "发一条消息",
		Inputs: []Field{
			{Name: "chat_id", Type: "string", Required: true, Label: "对话 ID"},
			{Name: "mode", Type: "enum", Options: []Option{{Value: "HTML"}, {Value: "MD", Label: "Markdown"}}},
			{Name: "count", Type: "number", Default: float64(3)},
			{Name: "files", Type: "array", ItemType: "file"},
			{Name: "tags", Type: "array", ItemType: "string"},
			{Name: "rows", Type: "array", GoType: "Row", Fields: []Field{{Name: "x", Type: "string"}}},
			{Name: "doc", Type: "json", GoType: "DocObj", Fields: []Field{{Name: "t", Type: "string"}}},
			{Name: "blob", Type: "json"}, // 旧契约里的无结构 json
		},
		Outputs: []Field{{Name: "ok", Type: "boolean", Required: true}},
	}}

	src, err := RenderSchema("schema", ops)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "schema.go", src, 0); err != nil {
		t.Fatalf("生成的代码语法不合法: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		`type Send struct\{\}`,
		`func \(Send\) Meta\(\) sokel\.Meta`,
		`ID: "send", Label: "发送", Desc: "发一条消息"`,
		`field\.String\("chat_id"\)\.Label\("对话 ID"\)`,
		`field\.Enum\("mode", field\.Opt\("HTML"\), field\.Opt\("MD", "Markdown"\)\)`,
		`field\.Number\("count"\)\.Default\(3\)`,
		`field\.Files\("files"\)`,
		`field\.Array\("tags", \[\]string\{\}\)`,
		`field\.Array\("rows", \[\]Row\{\}\)`, // 引用插件里已有的类型，不重造
		`field\.Json\("doc", DocObj\{\}\)`,
		`field\.Object\("blob", "待补理由"\)`, // 逼人来判断：补结构还是写清理由
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("缺片段 %q\n---\n%s", want, src)
		}
	}

	// 默认必填，可选才显式 —— 与 builder 的语义一致
	if !strings.Contains(src, `field.Enum("mode", field.Opt("HTML"), field.Opt("MD", "Markdown")).Optional()`) {
		t.Errorf("非必填应显式 .Optional()\n---\n%s", src)
	}
	// 有默认值的不再叠加 .Optional()（默认值本身就说明可选）
	if strings.Contains(src, `.Default(3).Optional()`) {
		t.Errorf("有默认值不该再叠 .Optional()\n---\n%s", src)
	}
	// 人工检查清单 + 待挪动的类型
	if !strings.Contains(src, "迁移起点") || !strings.Contains(src, "待挪动：DocObj, Row") {
		t.Errorf("应留下人工检查清单与待挪类型\n---\n%s", src)
	}
}
