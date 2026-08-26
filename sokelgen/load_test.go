// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// 端到端：从 schema 包源码 → 找出 Schema 类型 → 生成临时程序运行 → 拿到完整声明。
// 用真实包（internal/demoschema）而不是字符串样例——builder 是可执行代码，
// 只有真跑一遍才知道链式调用、默认值、嵌套结构是不是都对。
func TestLoadDeclarations(t *testing.T) {
	pkg, err := LoadDir("internal/demoschema")
	if err != nil {
		t.Fatalf("加载包失败: %v", err)
	}
	types := pkg.SchemaTypes()
	if len(types) != 2 {
		t.Fatalf("应找到 2 个 Schema: %v", types)
	}

	ops, err := LoadDeclarations("internal/demoschema", "github.com/sokel-dev/sokel-plugin-sdk/sokelgen/internal/demoschema", types)
	if err != nil {
		t.Fatalf("取声明失败: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("应取到 2 个操作: %+v", ops)
	}

	byID := map[string]OpIO{}
	for _, o := range ops {
		byID[o.OpID] = o
	}

	fd := byID["file_digest"]
	if len(fd.Inputs) != 3 {
		t.Fatalf("file_digest 应有 3 个入参: %+v", fd.Inputs)
	}
	if fd.Inputs[0].Type != "file" || !fd.Inputs[0].Required {
		t.Errorf("file 入参（默认必填）: %+v", fd.Inputs[0])
	}
	// enum 的显示名与「有默认值即可选」都要如实带过来
	algo := fd.Inputs[1]
	if algo.Type != "enum" || len(algo.Options) != 2 || algo.Options[1].Label != "SHA-256" {
		t.Errorf("enum 选项: %+v", algo)
	}
	if algo.Required || algo.Default != "md5" {
		t.Errorf("有默认值应视为可选: %+v", algo)
	}
	// opaque 必须带着理由到达契约——评审时看得见为什么这里放弃了结构
	extra := fd.Inputs[2]
	if !extra.Opaque || extra.Desc == "" {
		t.Errorf("opaque 应记录理由: %+v", extra)
	}

	si := byID["system_info"]
	if len(si.Inputs) != 1 || si.Inputs[0].ItemType != "string" {
		t.Errorf("数组的标量元素类型应可表达: %+v", si.Inputs)
	}
	if len(si.Outputs) != 1 || len(si.Outputs[0].Fields) != 2 {
		t.Errorf("嵌套结构应展开: %+v", si.Outputs)
	}
	// 类型名要穿过「运行取声明」这一层活下来 —— 生成 Out struct 时要复用 OSInfo，
	// 而不是照 Fields 重造一个等价结构（两个类型互转正是要杜绝的运行时转换）。
	if si.Outputs[0].GoType != "OSInfo" {
		t.Errorf("Go 类型名应保留到 IR: %+v", si.Outputs[0])
	}

	// 生成的类型名与 OnXxx 注册函数配套；SchemaType 让注册代码能指回声明本身
	if fd.InType != "FileDigestIn" || fd.OutType != "FileDigestOut" || fd.SchemaType != "FileDigest" {
		t.Errorf("类型名: %+v", fd)
	}
}

// 生成物的命名必须只有一个来源：操作 ID。
// 曾经 In/Out 取自 schema 类型名而 On/Emitter 取自操作 ID——两者同名时看不出问题，
// 一旦不同名（type Stream + id "egress_stream"）生成的代码直接不编译。
func TestOpTypeNamesFollowOpID(t *testing.T) {
	ops := []OpIO{{OpID: "egress_stream", SchemaType: "Stream", InType: "EgressStreamIn", OutType: "EgressStreamOut", Stream: true}}
	src, err := RenderRegister("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	// 注册函数、产出器、入参类型三者必须指同一套名字
	for _, want := range []string{
		"func OnEgressStream(", "type EgressStreamEmitter struct",
		"*EgressStreamIn", "*EgressStreamOut",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("缺 %q:\n%s", want, src)
		}
	}
	// 契约仍按 schema 类型取（那是声明所在）
	if !strings.Contains(src, "contract.OperationOf(&schema.Stream{})") {
		t.Errorf("契约应取自 schema 类型:\n%s", src)
	}
}
