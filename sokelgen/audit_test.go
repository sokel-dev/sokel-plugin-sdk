package sokelgen

import (
	"strings"
	"testing"
)

// 弱类型审计：每处 opaque 都该是有意识的决定。没写理由的要被看见——
// 目标是把不透明字段压到最少，而不是让它们悄悄堆积。
func TestAuditOpaque(t *testing.T) {
	ops := []OpIO{{
		OpID: "upsert",
		Inputs: []Field{
			{Name: "blob", Type: "json", Opaque: true},                   // 没理由
			{Name: "meta", Type: "json", Opaque: true, Desc: "键由本库配置决定"}, // 有理由
			{Name: "chunks", Type: "array", Fields: []Field{
				{Name: "fields", Type: "json", Opaque: true}, // 嵌套的也要抓到
			}},
			{Name: "boosts", Type: "json", ValueType: &Field{Type: "json", Opaque: true}}, // valueType 里的
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{
				{Name: "A", Type: "json", Fields: []Field{{Name: "x", Type: "json", Opaque: true}}}, // oneOf 分支里的
			}},
		},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}}

	rs := AuditOpaque(ops)
	if len(rs) != 5 {
		t.Fatalf("应找到 5 处 opaque（含嵌套/valueType/oneOf 分支）: %+v", rs)
	}
	paths := map[string]string{}
	for _, r := range rs {
		paths[r.Path] = r.Reason
	}
	for _, want := range []string{
		"upsert.in.blob", "upsert.in.meta",
		"upsert.in.chunks.fields",  // 嵌套
		"upsert.in.boosts.<value>", // valueType 内
		"upsert.in.doc(A).x",       // oneOf 分支内
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("漏了 %s: %+v", want, rs)
		}
	}

	w := FormatOpaqueWarnings(rs)
	if !strings.Contains(w, "4 处") {
		t.Errorf("有理由的那处不该计入警告: %s", w)
	}
	if strings.Contains(w, "upsert.in.meta") {
		t.Errorf("有理由的不该出现在警告里: %s", w)
	}
	// 警告要给出路，而不是只说「有问题」
	if !strings.Contains(w, "field.Object") || !strings.Contains(w, "能补出结构就补") {
		t.Errorf("警告应告诉人怎么办: %s", w)
	}

	// 全都有理由时不打扰
	if FormatOpaqueWarnings([]OpaqueReport{{Path: "a", Reason: "确实动态"}}) != "" {
		t.Error("全部有理由时不该警告")
	}
}

// 审计要区分两档：Object（是对象、键不定）与 Any（连类别都不定）。
// Any 更弱，更该被追问——混在一起报的话，一处「什么都可能」和一处
// 「对象但键不定」看起来一样重，而它们不一样。
func TestAuditDistinguishesAnyFromObject(t *testing.T) {
	ops := []OpIO{{OpID: "req", Inputs: []Field{
		{Name: "meta", Type: "json", Opaque: true, Desc: "上游原样返回的元数据"},
		{Name: "body", Type: "json", Opaque: true, Types: []string{"json", "array", "string"}, Desc: "raw 模式给字符串"},
	}}}
	reports := AuditOpaque(ops)
	if len(reports) != 2 {
		t.Fatalf("两处都要报: %+v", reports)
	}
	byPath := map[string]OpaqueReport{}
	for _, r := range reports {
		byPath[r.Path] = r
	}
	if byPath["req.in.meta"].AnyValue {
		t.Error("meta 是对象，不该标成 any")
	}
	if !byPath["req.in.body"].AnyValue {
		t.Error("body 声明了多种类型，应标成 any")
	}
}

// 数组漏声明元素形状是**静默**的：field.Array 的形状参数是 any，
// `field.Array("messages", "邮件列表")` 编译得过，产出一个无结构数组，
// 下游拿到 messages[0] 不知道里面有什么。gmail 就这么错了一版。
func TestAuditArrays(t *testing.T) {
	ops := []OpIO{{
		OpID: "gmail_list",
		Outputs: []Field{
			{Name: "shapeless", Type: "array", Desc: "邮件列表"},                                // ← 漏了
			{Name: "objects", Type: "array", Fields: []Field{{Name: "id", Type: "string"}}}, // 对象元素
			{Name: "scalars", Type: "array", ItemType: "string"},                            // 标量元素
			{Name: "opaque", Type: "array", Opaque: true},                                   // 明确承认没结构
			{Name: "union", Type: "array", OneOf: []OneOfVariant{{Name: "A"}}},              // 几种形状之一
			{Name: "notArray", Type: "string"},
		},
	}}
	got := AuditArrays(ops)
	if len(got) != 1 || got[0].Path != "gmail_list.out.shapeless" {
		t.Fatalf("只该报漏声明的那一个: %+v", got)
	}
	// 报错要带上那句描述——它多半就是本该写在 Desc 里、却被塞进形状参数的那句话
	if got[0].Desc != "邮件列表" {
		t.Errorf("要带上说明帮人认出是哪处: %+v", got[0])
	}
	w := FormatArrayWarnings(got)
	if w == "" || !strings.Contains(w, "field.Array(名, []Item{})") {
		t.Errorf("警告要给出改法: %q", w)
	}
	if FormatArrayWarnings(nil) != "" {
		t.Error("没问题时不该有输出")
	}
}

// 嵌套里的数组同样要审：对象字段里套一个无结构数组，症状一模一样。
func TestAuditArraysWalksNested(t *testing.T) {
	ops := []OpIO{{
		OpID: "x",
		Inputs: []Field{{Name: "payload", Type: "json", Fields: []Field{
			{Name: "items", Type: "array"},
		}}},
	}}
	if got := AuditArrays(ops); len(got) != 1 || got[0].Path != "x.in.payload.items" {
		t.Errorf("嵌套数组也要报: %+v", got)
	}
}
