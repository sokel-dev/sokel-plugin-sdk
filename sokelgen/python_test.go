// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Python 渲染器：同一份 IR → pydantic 模型。这也是**验证 IR 够不够用**的手段——
// 趁存量插件还没锁死它，现在发现缺字段还能改。
func TestRenderPython(t *testing.T) {
	osInfo := []Field{{Name: "name", Type: "string"}, {Name: "arch", Type: "string"}}
	ops := []OpIO{{
		OpID: "file_digest", Label: "文件摘要",
		Inputs: []Field{
			{Name: "file", Type: "file", Required: true},
			{Name: "algo", Type: "enum", Default: "md5", Options: []Option{{Value: "md5"}, {Value: "sha256", Label: "SHA-256"}}},
			{Name: "tags", Type: "array", ItemType: "string"},
			{Name: "blob", Type: "json", Opaque: true},
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{{Name: "A", Type: "json"}}},
		},
		Outputs: []Field{
			{Name: "sum", Type: "string", Required: true},
			{Name: "os", Type: "json", GoType: "OSInfo", Fields: osInfo},
			{Name: "hosts", Type: "array", GoType: "OSInfo", Fields: osInfo}, // 同名类型复用，不该生成两遍
		},
	}}

	src, err := RenderPython(ops)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}

	// 语法必须合法 —— 拼串生成 Python 最容易栽在缩进上
	dir := t.TempDir()
	f := filepath.Join(dir, "models.py")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "-c",
		"import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", f).CombinedOutput(); err != nil {
		t.Fatalf("生成的 Python 语法不合法: %v\n%s\n---\n%s", err, out, src)
	}

	for _, want := range []string{
		`class OSInfo\(BaseModel\)`,
		`class FileDigestIn\(BaseModel\)`,
		`class FileDigestOut\(BaseModel\)`,
		`file: `,
		`algo: str = "md5"`,      // 默认值带过来
		`tags: list\[str\]`,      // 标量元素
		`blob: dict\[str, Any\]`, // opaque
		`doc: Any`,               // oneOf
		`os: OSInfo`,             // 命名类型复用（GoType 在这里的作用：给结构一个名字）
		`hosts: list\[OSInfo\]`,  // 数组元素也复用
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("缺片段 %q\n---\n%s", want, src)
		}
	}
	// 同一个命名类型只该定义一次
	if strings.Count(src, "class OSInfo(BaseModel)") != 1 {
		t.Errorf("OSInfo 应只生成一次\n---\n%s", src)
	}
	// 必填在前、带默认值在后 —— 否则 Python 直接 SyntaxError（上面 compile 已覆盖，这里说明意图）
	if strings.Index(src, "sum: str") > strings.Index(src, "os: OSInfo") {
		t.Errorf("字段顺序应保持声明序\n---\n%s", src)
	}
}

// oneOf 的 Python 表达：Python 有真联合类型，不该退化成 Any。
// 数组元素联合 → list[A | B]，字段级联合 → A | B；两者的分支都要生成出模型。
func TestPythonUnion(t *testing.T) {
	ops := []OpIO{{
		OpID: "chat", InType: "ChatIn", OutType: "ChatOut",
		Inputs: []Field{
			{Name: "parts", Type: "array", Required: true, OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string", Required: true}}},
				{Name: "ImagePart", GoType: "ImagePart", Type: "json",
					Fields: []Field{{Name: "url", Type: "string", Required: true}}},
			}},
			{Name: "doc", Type: "json", Required: true, OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string", Required: true}}},
			}},
		},
	}}
	src, err := RenderPython(ops)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "parts: list[TextPart | ImagePart]") {
		t.Errorf("数组元素联合应是 list[A | B]:\n%s", src)
	}
	if !strings.Contains(src, "doc: TextPart") {
		t.Errorf("字段级联合应是联合类型:\n%s", src)
	}
	// 分支类型本身要有模型，且同名只定义一次（两处都引用了 TextPart）
	if n := strings.Count(src, "class TextPart(BaseModel)"); n != 1 {
		t.Errorf("TextPart 应恰好定义一次，实际 %d:\n%s", n, src)
	}
	if !strings.Contains(src, "class ImagePart(BaseModel)") {
		t.Errorf("缺 ImagePart 模型:\n%s", src)
	}
}
