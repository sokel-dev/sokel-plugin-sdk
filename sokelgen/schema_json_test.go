// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sokel.schema.json 是**格式的第二份定义**（第一份是本包的解析器）。
// 两份定义必然漂——除非有人盯着。这条测试就是那个人：
// 声明里加了字段却忘了改 schema，编辑器就不认识它（写了不报错、也不补全，
// 一直到跑 sokel-gen 才发现），而那正是「有 schema」本来要消灭的那种失效。
func TestJSONSchemaMatchesParser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "sokel.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("sokel.schema.json 不是合法 JSON: %v", err)
	}
	defs, _ := doc["$defs"].(map[string]any)

	cases := []struct {
		def string // 空 = 根对象
		typ reflect.Type
	}{
		{"", reflect.TypeOf(Manifest{})},
		{"plugin", reflect.TypeOf(PluginDecl{})},
		{"credential", reflect.TypeOf(CredentialDecl{})},
		{"auth", reflect.TypeOf(AuthDecl{})},
		{"event", reflect.TypeOf(EventDecl{})},
		{"operation", reflect.TypeOf(OperationDecl{})},
		{"codegen", reflect.TypeOf(CodegenDecl{})},
		{"field", reflect.TypeOf(Field{})},
		{"variant", reflect.TypeOf(OneOfVariant{})},
	}
	for _, tc := range cases {
		node := doc
		if tc.def != "" {
			n, ok := defs[tc.def].(map[string]any)
			if !ok {
				t.Errorf("schema 里没有 $defs.%s", tc.def)
				continue
			}
			node = n
		}
		props, _ := node["properties"].(map[string]any)
		if props == nil {
			t.Errorf("$defs.%s 没有 properties", tc.def)
			continue
		}
		tags := jsonTags(tc.typ)

		// 解析器认的键，schema 必须都有——否则编辑器把合法声明标成错的
		for name := range tags {
			if _, ok := props[name]; !ok {
				t.Errorf("%s：解析器认 %q，schema 里没有（加字段忘了改 schema）", where(tc.def), name)
			}
		}
		// schema 列的键，解析器必须认（或者是已登记的 snake_case 别名）——
		// 否则编辑器补全出一个会被 DisallowUnknownFields 当场拒掉的键
		for name := range props {
			if _, ok := tags[name]; ok {
				continue
			}
			if alias, ok := keyAliases[name]; ok {
				if _, ok := tags[alias]; ok {
					continue
				}
			}
			t.Errorf("%s：schema 列了 %q，解析器不认（删字段忘了改 schema）", where(tc.def), name)
		}
	}

	// enum 候选项的两种写法：裸字符串 / {value,label}
	opt, _ := defs["option"].(map[string]any)
	branches, _ := opt["oneOf"].([]any)
	if len(branches) != 2 {
		t.Fatalf("$defs.option 应有两种写法（字符串 / 对象），实际 %d", len(branches))
	}
	objProps, _ := branches[1].(map[string]any)["properties"].(map[string]any)
	for name := range jsonTags(reflect.TypeOf(Option{})) {
		if _, ok := objProps[name]; !ok {
			t.Errorf("$defs.option 缺 %q", name)
		}
	}
}

// 类型必须与解析器认的那张表一致——schema 少一种，编辑器就把它标红；
// 多一种，编辑器就替一个跑起来会报错的写法背书。
func TestJSONSchemaFieldTypesMatchWireTypes(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("..", "docs", "sokel.schema.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	field := defs["field"].(map[string]any)["properties"].(map[string]any)
	listed := map[string]bool{}
	for _, v := range field["type"].(map[string]any)["enum"].([]any) {
		listed[v.(string)] = true
	}
	sugar := map[string]bool{"int": true, "files": true, "ints": true, "strings": true}
	for name := range wireTypes {
		if !listed[name] {
			t.Errorf("schema 的 field.type 少了协议类型 %q", name)
		}
	}
	for name := range listed {
		if !wireTypes[name] && !sugar[name] {
			t.Errorf("schema 的 field.type 多了 %q —— 解析器不认它", name)
		}
	}
}

func jsonTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

func where(def string) string {
	if def == "" {
		return "根对象"
	}
	return "$defs." + def
}
