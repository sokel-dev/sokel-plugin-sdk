// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

// enum tag：string 字段 + `enum:"a,b,c"` → Type=enum + Options，且 default 保留。
func TestDeriveEnumField(t *testing.T) {
	type In struct {
		Mode string `sokel:"mode,optional" label:"模式" enum:"html, markdown ,llm_text" default:"markdown"`
		Kind string `sokel:"kind"`
	}
	fields := deriveFields(reflect.TypeOf(new(In)).Elem())
	if len(fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(fields))
	}
	mode := fields[0]
	if mode.Type != TEnum {
		t.Errorf("mode.Type = %q, want enum", mode.Type)
	}
	if len(mode.Options) != 3 || mode.Options[0].Value != "html" || mode.Options[2].Value != "llm_text" {
		t.Errorf("options 解析错误（应去空格）: %v", mode.Options)
	}
	if mode.Default != "markdown" {
		t.Errorf("default = %v", mode.Default)
	}
	if mode.Required {
		t.Error("有默认值不应必填")
	}
	// 无 enum tag 的普通 string 字段不受影响。
	if fields[1].Type != TString || len(fields[1].Options) != 0 {
		t.Errorf("kind 应为普通 string: %+v", fields[1])
	}
}

// type tag 显式覆盖反射类型（如 any 语义是 number）。
func TestFieldTypeOverride(t *testing.T) {
	type In struct {
		ChatID any `sokel:"chat_id" type:"number"` // 反射本会得 json，显式覆盖为 number
		Raw    any `sokel:"raw"`                   // 无 override → json
		N      int `sokel:"n" type:"string"`       // number 反射 → 覆盖 string
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	got := map[string]ParamType{}
	for _, f := range fs {
		got[f.Name] = f.Type
	}
	if got["chat_id"] != TNumber {
		t.Errorf("chat_id 应被 type tag 覆盖为 number, got %s", got["chat_id"])
	}
	if got["raw"] != TJSON {
		t.Errorf("raw 无 override 应为 json, got %s", got["raw"])
	}
	if got["n"] != TString {
		t.Errorf("n 应被覆盖为 string, got %s", got["n"])
	}
}

// 文件数组契约：[]*testFile / []File → array<file>（文件列表的唯一表达，web docs/type-system.md §12）——
// 变量选择器识别 itemType=file（可整组绑给多模态图片块/文件列表参数），子字段由前端按 FileValue 展开。
// 曾报泛型 array（无 ItemType）：图片数组不被识别为文件型，用户被迫绕道数组节点；
// 也曾用历史形态 file+multiple，2026-08-05 全栈废除。
func TestDeriveFileSliceField(t *testing.T) {
	type Out struct {
		Images []*testFile `sokel:"images"`
		Single *testFile   `sokel:"voice"`
	}
	fs := deriveFields(reflect.TypeOf(Out{}))
	if fs[0].Type != TArray || fs[0].ItemType != TFile {
		t.Fatalf("images 应为 array<file>: %+v", fs[0])
	}
	if fs[0].Opaque {
		t.Errorf("array<file> 元素类型明确，不是 opaque: %+v", fs[0])
	}
	if len(fs[0].Fields) != 0 {
		t.Errorf("文件列表不带子字段（前端按 FileValue 展开）: %+v", fs[0].Fields)
	}
	if fs[1].Type != TFile || fs[1].ItemType != "" {
		t.Errorf("单文件应为 file 且无 ItemType: %+v", fs[1])
	}
}

// 文件必填语义：文件参数只能用指针 *testFile（值类型会被当 json struct），但「指针→自动可选」
// 规则会让文件永远无法声明必填。文件是例外：按 ,optional 显式判定（默认必填，标 optional 才可选）。
func TestFileRequiredSemantics(t *testing.T) {
	type In struct {
		File   *testFile   `sokel:"file"`           // 无 optional → 必填
		Cover  *testFile   `sokel:"cover,optional"` // 显式 optional → 可选
		Attach []*testFile `sokel:"attach"`         // 多文件无 optional → 必填
		Note   *string     `sokel:"note"`           // 非文件指针 → 仍走「指针=可选」旧规则
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	req := map[string]bool{}
	for _, f := range fs {
		req[f.Name] = f.Required
	}
	if !req["file"] {
		t.Error("file 无 optional 应必填")
	}
	if req["cover"] {
		t.Error("cover 带 optional 应可选")
	}
	if !req["attach"] {
		t.Error("attach 多文件无 optional 应必填")
	}
	if req["note"] {
		t.Error("非文件指针应保持「指针=可选」旧语义")
	}
}

// toSnake 缩略词：连续大写作为一个词（ID→id，URL→url，ChatID→chat_id，HTTPCode→http_code）。
func TestToSnakeAcronyms(t *testing.T) {
	cases := map[string]string{"ID": "id", "URL": "url", "ChatID": "chat_id", "HTTPCode": "http_code", "Name": "name", "MessageID": "message_id"}
	for in, want := range cases {
		if got := toSnake(in); got != want {
			t.Errorf("toSnake(%q)=%q want %q", in, got, want)
		}
	}
}

// handler panic 不应崩溃整个进程：invoke 内 recover → 转成 error 帧（节点标红，插件继续服务）。
// 复现：某个真实插件对可选指针 *StartPage 无判空解引用，未传时 nil deref 曾把进程带崩。

// 递归/自引用 struct（树形，如 Block{ Blocks []*Block }）：契约推导必须终止，不能栈溢出。
// 复现：某个真实插件的 Block 含子块 []*Block，启动 Register 推导契约时无限递归崩溃。
func TestDeriveRecursiveStruct(t *testing.T) {
	type Block struct {
		Text   string   `sokel:"text"`
		Blocks []*Block `sokel:"blocks"` // 自引用：子块与父同类型
	}
	type Out struct {
		Root Block `sokel:"root"`
	}
	fs := deriveFields(reflect.TypeOf(Out{})) // 不崩即通过
	root := fs[0]
	if root.Type != TJSON {
		t.Fatalf("root 应为 json: %+v", root)
	}
	// root 下应有 text + blocks 两个子字段；blocks 是环点 → array 且不再往下钻（无子字段）。
	var blocks *Field
	for i := range root.Fields {
		if root.Fields[i].Name == "blocks" {
			blocks = &root.Fields[i]
		}
	}
	if blocks == nil || blocks.Type != TArray {
		t.Fatalf("blocks 应为 array: %+v", root.Fields)
	}
	if len(blocks.Fields) != 0 {
		t.Errorf("环点数组不应继续展开子字段（避免无限递归）: %+v", blocks.Fields)
	}
}

// 兄弟分支的同类型不应被环检测误伤（路径级去重，非全局）：两个 Bbox 字段都应完整展开。
func TestDeriveSiblingSameTypeNotDeduped(t *testing.T) {
	type Bbox struct {
		X float64 `sokel:"x"`
	}
	type In struct {
		A Bbox `sokel:"a"`
		B Bbox `sokel:"b"`
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	for _, f := range fs {
		if len(f.Fields) != 1 || f.Fields[0].Name != "x" {
			t.Errorf("兄弟同类型应各自完整展开: %s -> %+v", f.Name, f.Fields)
		}
	}
}

// map 的结构推导：map[string]any 是 opaque（不产 valueType），map[string]T 递归展开为 valueType。
// 这让 Go 语言层面天然对应类型系统的 typed/opaque 二分（docs/type-system.md §3），
// 而不是像从前那样把所有 map 一律降级成无结构的 json。
func TestDeriveMapValueType(t *testing.T) {
	type slot struct {
		Source string `sokel:"source" enum:"item,fixed,var"`
		Path   string `sokel:"path,optional"`
	}
	type in struct {
		Loose  map[string]any    `sokel:"loose"`   // opaque：值无结构可言
		Typed  map[string]slot   `sokel:"typed"`   // 动态键 + 定型值
		Scalar map[string]string `sokel:"scalar"`  // 值是标量也算
		BadKey map[int]slot      `sokel:"bad_key"` // 非 string 键：JSON 对象表达不了，按 opaque
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}

	if f := got["loose"]; f.Type != TJSON || f.ValueType != nil {
		t.Errorf("map[string]any 应为无 valueType 的 json，got type=%s valueType=%+v", f.Type, f.ValueType)
	}
	if f := got["bad_key"]; f.ValueType != nil {
		t.Errorf("非 string 键不应产 valueType，got %+v", f.ValueType)
	}
	if f := got["scalar"]; f.ValueType == nil || f.ValueType.Type != TString {
		t.Errorf("map[string]string 的 valueType 应为 string，got %+v", f.ValueType)
	}

	vt := got["typed"].ValueType
	if vt == nil || vt.Type != TJSON {
		t.Fatalf("map[string]slot 的 valueType 应为 json，got %+v", vt)
	}
	if len(vt.Fields) != 2 {
		t.Fatalf("valueType 应递归展开出 2 个子字段，got %d: %+v", len(vt.Fields), vt.Fields)
	}
	// 子字段的 tag 语义（enum/optional）在递归里同样生效，不是只报个类型名。
	if vt.Fields[0].Type != TEnum || len(vt.Fields[0].Options) != 3 {
		t.Errorf("valueType 子字段应保留 enum 候选值，got %+v", vt.Fields[0])
	}
	if vt.Fields[1].Required {
		t.Errorf("valueType 子字段的 optional 应生效，got %+v", vt.Fields[1])
	}
}

// 弱类型要成为**看得见的决定**，而不是默认路径（docs/type-system.md §3）。
// 裸 map[string]any / any 字段在契约里显式标 opaque，UI 据此标注「无结构约束」，
// 平台侧也据此跳过结构校验——否则「没声明结构」和「声明了但恰好为空」分不清。
func TestDeriveOpaqueMarking(t *testing.T) {
	type slot struct {
		Path string `sokel:"path"`
	}
	type in struct {
		Loose    map[string]any  `sokel:"loose"`    // 裸 map → opaque
		Anything any             `sokel:"anything"` // 裸 any → opaque
		Typed    map[string]slot `sokel:"typed"`    // 定型 map → 不是 opaque（有 valueType）
		Obj      slot            `sokel:"obj"`      // struct → 不是 opaque（有 fields）
		Name     string          `sokel:"name"`     // 标量与 opaque 无关
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if !got["loose"].Opaque {
		t.Errorf("map[string]any 应标 opaque: %+v", got["loose"])
	}
	if !got["anything"].Opaque {
		t.Errorf("裸 any 应标 opaque: %+v", got["anything"])
	}
	if got["typed"].Opaque {
		t.Errorf("有 valueType 的 map 不该标 opaque: %+v", got["typed"])
	}
	if got["obj"].Opaque {
		t.Errorf("有 fields 的 struct 不该标 opaque: %+v", got["obj"])
	}
	if got["name"].Opaque {
		t.Errorf("标量字段与 opaque 无关: %+v", got["name"])
	}
}

// enum 选项支持显示名：发音人 xiaoyan / aisjiuxu 这类值是代码，用户在下拉里看不懂。
// 语法 `enum:"值=显示名"`，混用不带显示名的旧写法（`enum:"a,b"`）必须继续有效。
// 用 = 而不是 : 分隔——枚举值里出现冒号（URL、时间）比出现等号常见得多。
func TestDeriveEnumWithLabels(t *testing.T) {
	type in struct {
		Voice string `sokel:"voice" enum:"xiaoyan=小燕（女声）,aisjiuxu=许久（男声）"`
		Order string `sokel:"order" enum:"asc,desc"`
		Mixed string `sokel:"mixed" enum:"a,b=乙"`
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}

	v := got["voice"]
	if v.Type != TEnum || len(v.Options) != 2 {
		t.Fatalf("voice 应为 2 项 enum: %+v", v)
	}
	if v.Options[0].Value != "xiaoyan" || v.Options[0].Label != "小燕（女声）" {
		t.Errorf("值与显示名应分开: %+v", v.Options[0])
	}

	// 旧写法：无显示名时 Label 留空，前端回退显示 value（不在契约里塞冗余）
	o := got["order"]
	if len(o.Options) != 2 || o.Options[0].Value != "asc" || o.Options[0].Label != "" {
		t.Errorf("无显示名的旧写法应照常: %+v", o.Options)
	}

	m := got["mixed"]
	if len(m.Options) != 2 || m.Options[0].Value != "a" || m.Options[1].Label != "乙" {
		t.Errorf("混用应各归各位: %+v", m.Options)
	}
}

// 数组元素无结构时，array 本身也该标 opaque。触发点：report-pipeline 的
// `type tag = map[string]any` + `[]tag` —— 看着像结构体切片，实为 []map[string]any。
// AST 生成器与运行时反射必须给出同样的判定，否则切换时行为悄悄变了。
func TestDeriveArrayOpaque(t *testing.T) {
	type row struct {
		Name string `sokel:"name"`
	}
	type in struct {
		Rows  []row            `sokel:"rows"`
		Blobs []map[string]any `sokel:"blobs"`
		Anys  []any            `sokel:"anys"`
		Names []string         `sokel:"names"`
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if got["rows"].Opaque || len(got["rows"].Fields) != 1 {
		t.Errorf("元素有结构的数组不该 opaque: %+v", got["rows"])
	}
	if !got["blobs"].Opaque {
		t.Errorf("[]map[string]any 应标 opaque: %+v", got["blobs"])
	}
	if !got["anys"].Opaque {
		t.Errorf("[]any 应标 opaque: %+v", got["anys"])
	}
	if got["names"].Opaque {
		t.Errorf("标量元素的数组不是 opaque: %+v", got["names"])
	}
}

// 结构里的 map[string]any 也要能说明「为什么没有结构」——
// 否则只有 builder 的 field.Object 能写理由，类型定义里的就成了没人解释的黑洞。
func TestOpaqueTagReason(t *testing.T) {
	type in struct {
		Meta  map[string]any `sokel:"meta" opaque:"键与值类型由本库声明的元数据字段决定"`
		Plain map[string]any `sokel:"plain"` // 没写理由：仍是 opaque，但生成器会警告
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if m := got["meta"]; !m.Opaque || m.Desc == "" {
		t.Errorf("opaque tag 应记录理由: %+v", m)
	}
	if p := got["plain"]; !p.Opaque || p.Desc != "" {
		t.Errorf("没写理由的仍是 opaque 但 Desc 为空（供生成器警告）: %+v", p)
	}
}

// 显式 type tag 会改写推导出的类型，opaque 判定必须看**最终**类型。
// `any` + `type:"number,string"` 是标量联合（id 可能是数字或字符串），不是无结构。
// 这个顺序错误是 opaque 审计工具第一次跑就抓到的。
func TestTypeTagClearsOpaque(t *testing.T) {
	type in struct {
		ID   any `sokel:"id" type:"number,string"`
		Blob any `sokel:"blob"` // 没有 type tag：确实无结构
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if got["id"].Opaque {
		t.Errorf("标量联合不该是 opaque: %+v", got["id"])
	}
	if got["id"].Type != TNumber || len(got["id"].Types) != 2 {
		t.Errorf("type tag 应生效: %+v", got["id"])
	}
	if !got["blob"].Opaque {
		t.Errorf("裸 any 仍是 opaque: %+v", got["blob"])
	}
}

// 入参绑定必须**递归**认 sokel tag。
// 出参那侧（structToVars）一直是递归的，入参却只认顶层——于是嵌套结构里
// snake_case 的字段静默绑空：Go 的 json 大小写不敏感匹配跨不过下划线，
// `doc_id` 落不进 `DocID`，而且不报错。
func TestBindInputNested(t *testing.T) {
	type chunk struct {
		ID       string `sokel:"id"`
		DocID    string `sokel:"doc_id"`
		ParentID string `sokel:"parent_id"`
	}
	type inner struct {
		HeapAllocBytes uint64 `sokel:"heap_alloc_bytes"`
	}
	type in struct {
		KbID   string           `sokel:"kb_id"`
		Chunks []chunk          `sokel:"chunks"`
		Mem    inner            `sokel:"mem"`
		ByName map[string]chunk `sokel:"by_name"`
		Ptr    *chunk           `sokel:"ptr"`
	}

	var got in
	raw := []byte(`{
		"kb_id":"kb1",
		"chunks":[{"id":"c1","doc_id":"d1","parent_id":"p1"}],
		"mem":{"heap_alloc_bytes":42},
		"by_name":{"a":{"id":"c2","doc_id":"d2"}},
		"ptr":{"id":"c3","doc_id":"d3"}
	}`)
	if err := BindInput(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.KbID != "kb1" {
		t.Errorf("顶层: %+v", got)
	}
	if len(got.Chunks) != 1 || got.Chunks[0].DocID != "d1" || got.Chunks[0].ParentID != "p1" {
		t.Errorf("数组元素里的 snake_case 字段应绑上: %+v", got.Chunks)
	}
	if got.Mem.HeapAllocBytes != 42 {
		t.Errorf("嵌套结构: %+v", got.Mem)
	}
	if got.ByName["a"].DocID != "d2" {
		t.Errorf("map 值里的嵌套: %+v", got.ByName)
	}
	if got.Ptr == nil || got.Ptr.DocID != "d3" {
		t.Errorf("指针字段: %+v", got.Ptr)
	}
}

// 出参与入参必须互为逆运算：同一个结构 emit 出去再绑回来，字段不能丢。
func TestBindInputRoundTripsVars(t *testing.T) {
	type item struct {
		DocID string `sokel:"doc_id"`
		N     int    `sokel:"n"`
	}
	type payload struct {
		Items []item `sokel:"items"`
		Note  string `sokel:"note"`
	}
	src := payload{Items: []item{{DocID: "d1", N: 7}}, Note: "hi"}
	vars := StructToVars(src)
	raw, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	var back payload
	if err := BindInput(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != 1 || back.Items[0].DocID != "d1" || back.Items[0].N != 7 || back.Note != "hi" {
		t.Errorf("往返丢字段: %+v ← %s", back, raw)
	}
}

// testFile：实现 FileRef 的桩。契约包不认识平台文件的运行时（取字节/上传），
// 只需要认出「这是文件字段」——用一个标记就够，这正是解耦点。
type testFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (*testFile) FileRef() {}

// `opaque:"理由"` 标签必须**压过**自动判定。
//
// 自动判定只认「裸 map / any / 元素无结构」几种形状，认不出递归：[]*Block 里又有 []*Block，
// 环检测让子字段为空，但它并非没有结构，只是契约表达不了。早先这里是无条件赋值，
// 把标签连同它写的理由一起抹掉——于是作者明明解释过，审计还在追着报。
func TestOpaqueTagWinsOverInference(t *testing.T) {
	type Block struct {
		Text   string   `sokel:"text"`
		Blocks []*Block `sokel:"blocks" opaque:"块可无限嵌套，递归结构契约表达不了"`
	}
	fs := DeriveFields(reflect.TypeOf(Block{}))
	var blocks *Field
	for i := range fs {
		if fs[i].Name == "blocks" {
			blocks = &fs[i]
		}
	}
	if blocks == nil {
		t.Fatal("没推出 blocks 字段")
	}
	if !blocks.Opaque {
		t.Error("标了 opaque 就该是 opaque，别被自动判定覆盖")
	}
	if blocks.Desc == "" {
		t.Error("理由要留在 Desc 里，否则审计还是不知道为什么")
	}
}

// 标量元素的数组记 ItemType（[]string→string），不是「没有结构」。
// 漏记的话下游分不清「字符串数组」和真正无结构的 []any。
func TestScalarArrayRecordsItemType(t *testing.T) {
	type X struct {
		Tags  []string         `sokel:"tags"`
		Sizes []int            `sokel:"sizes"`
		Blobs []map[string]any `sokel:"blobs"`
	}
	byName := map[string]Field{}
	for _, f := range DeriveFields(reflect.TypeOf(X{})) {
		byName[f.Name] = f
	}
	if byName["tags"].ItemType != TString || byName["tags"].Opaque {
		t.Errorf("[]string 应记 ItemType=string 且非 opaque: %+v", byName["tags"])
	}
	if byName["sizes"].ItemType != TNumber {
		t.Errorf("[]int 应记 ItemType=number: %+v", byName["sizes"])
	}
	if byName["blobs"].ItemType != "" || !byName["blobs"].Opaque {
		t.Errorf("[]map[string]any 确实没有结构，应 opaque 且无 ItemType: %+v", byName["blobs"])
	}
}
