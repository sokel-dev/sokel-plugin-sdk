package sokelgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// oneOf 是 sokel-gen 存在的首要理由：反射拿不到「类型名字符串 → 类型」的映射，
// Go 也没有联合类型，所以结构联合在运行时反射下**根本产不出**。AST 可以：
// 直接在包内定位 tag 里点名的类型并展开（docs/plugin-sdk-multilang.md §1）。
const oneofSrc = `package main

// DocObject 中立文档对象。
type DocObject struct {
	Title  string ` + "`sokel:\"title\"`" + `
	Blocks []Block ` + "`sokel:\"blocks\"`" + `
}

type Block struct {
	Text string ` + "`sokel:\"text\"`" + `
}

// BlocksArray 裸块数组。
type BlocksArray []Block

type IngestIn struct {
	Doc any ` + "`sokel:\"document\" oneof:\"DocObject,BlocksArray\" label:\"结构化文档\"`" + `
}
`

func TestParseOneOf(t *testing.T) {
	fields, err := ParseStructFields(oneofSrc, "IngestIn")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("应有 1 个入参: %+v", fields)
	}
	f := fields[0]
	dump, _ := json.Marshal(f)

	if len(f.OneOf) != 2 {
		t.Fatalf("oneof 应展开 2 个分支: %s", dump)
	}
	// 分支①：struct → json + 递归展开的子字段
	v0 := f.OneOf[0]
	if v0.Name != "DocObject" || v0.Type != "json" {
		t.Errorf("分支0 应为 json: %s", dump)
	}
	if len(v0.Fields) != 2 {
		t.Errorf("分支0 应展开 title/blocks: %s", dump)
	}
	// 类型上的注释可以当分支标签用（AST 独有）
	if v0.Label == "" {
		t.Errorf("分支应带可读标签: %s", dump)
	}
	// 分支②：具名切片 → array + 元素结构
	v1 := f.OneOf[1]
	if v1.Name != "BlocksArray" || v1.Type != "array" {
		t.Errorf("分支1 应为 array: %s", dump)
	}
	if len(v1.Fields) != 1 || v1.Fields[0].Name != "text" {
		t.Errorf("分支1 应展开元素结构: %s", dump)
	}
	// 声明了 oneof 的字段本身不该再被当成 opaque —— 它有结构，只是有多种
	if f.Opaque {
		t.Errorf("有 oneof 的字段不该标 opaque: %s", dump)
	}
}

// oneof 点名了不存在的类型 → **生成期**报错。
// 这正是 codegen 相对反射的价值之一：这类错误此前要等插件启动、注册握手时才在平台侧炸。
func TestParseOneOfUnknownType(t *testing.T) {
	src := `package main

type In struct {
	Doc any ` + "`sokel:\"document\" oneof:\"Ghost\"`" + `
}
`
	_, err := ParseStructFields(src, "In")
	if err == nil {
		t.Fatal("引用不存在的类型应报错")
	}
	if !strings.Contains(err.Error(), "Ghost") {
		t.Errorf("报错应指名是哪个类型: %v", err)
	}
}
