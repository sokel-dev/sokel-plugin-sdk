// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// oneOf 用包装类型而不是 any（用户定案）：any 意味着 handler 里 doc.(map[string]any)
// 这种断言，写错要么 panic 要么静默失败。
//
// 形态是 raw + 类型化访问器，而不是「多字段指针」：后者要在 unmarshal 那一刻就判别分支，
// 而 oneOf 的运行值**不带 discriminator**（协议如此），按结构猜容易猜错。
// 让 handler 按自己的顺序试，每次尝试都是确定类型的。
func TestRenderTypesOneOfWrapper(t *testing.T) {
	ops := []OpIO{{
		OpID: "ingest", InType: "IngestIn", OutType: "IngestOut",
		Inputs: []Field{{
			Name: "doc", Type: "json",
			OneOf: []OneOfVariant{
				{Name: "DocObject", Type: "json", Fields: []Field{{Name: "title", Type: "string"}}},
				{Name: "Blocks", Type: "array", Fields: []Field{{Name: "text", Type: "string"}}},
			},
		}},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}}

	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_types.go", src, 0); err != nil {
		t.Fatalf("生成的代码语法不合法: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		`Doc\s+IngestInDoc`, // 字段用包装类型，不是 any
		`type IngestInDoc struct`,
		`func \(u \*IngestInDoc\) UnmarshalJSON`, // 收下原始字节，不在此刻判别
		`func \(u IngestInDoc\) AsDocObject\(\) \(\*schema\.DocObject, bool\)`,
		`func \(u IngestInDoc\) AsBlocks\(\) \(\[\]schema\.Blocks, bool\)`, // 数组分支
		`func \(u IngestInDoc\) Raw\(\) json\.RawMessage`,                  // 逃生舱：都不匹配时自己处理
		`json\.Unmarshal`,
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("缺片段 %q\n---\n%s", want, src)
		}
	}
	// 不该再退化成 any
	if regexp.MustCompile(`Doc\s+any`).MatchString(src) {
		t.Errorf("oneOf 不该是 any\n---\n%s", src)
	}
	// opaque 是 map[string]any —— 它是对象，只是键值不定；与 oneOf（形状可能完全不同）不是一回事
	src2, _ := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, []OpIO{{
		OpID: "o", InType: "OIn", OutType: "OOut",
		Inputs: []Field{{Name: "blob", Type: "json", Opaque: true}},
	}})
	if !strings.Contains(src2, "Blob map[string]any") {
		t.Errorf("opaque 应为 map[string]any（是对象，只是键值不定）\n---\n%s", src2)
	}
}
