package field_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/field"
)

// 用 sysinfo 的真实操作验手感：声明必须比 struct+tag 更清楚，而不是更啰嗦得没道理。
type fileDigest struct{}

func (fileDigest) Meta() contract.Meta {
	return contract.Meta{ID: "file_digest", Label: "文件摘要", Desc: "计算入参文件的 md5 与大小"}
}
func (fileDigest) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.File("file").Label("文件").Desc("任意文件，计算其 md5 与大小"),
	}
}
func (fileDigest) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("filename").Label("文件名"),
		field.String("md5").Label("MD5"),
		field.Number("size").Label("字节数"),
	}
}

func TestSchemaToOperation(t *testing.T) {
	op := contract.OperationOf(fileDigest{})
	if op.ID != "file_digest" || op.Label != "文件摘要" {
		t.Errorf("meta 没带过来: %+v", op)
	}
	if len(op.Inputs) != 1 || op.Inputs[0].Type != contract.TFile || !op.Inputs[0].Required {
		t.Errorf("入参不对（默认应必填）: %+v", op.Inputs)
	}
	if len(op.Outputs) != 3 || op.Outputs[2].Type != contract.TNumber {
		t.Errorf("出参不对: %+v", op.Outputs)
	}
}

// 声明是纯数据 —— 直接序列化即可交给其他语言的生成器（诉求：Go 声明 → JSON → 多语言）。
func TestSchemaIsSerializable(t *testing.T) {
	b, err := json.Marshal(contract.OperationOf(fileDigest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"file_digest"`, `"file"`, `"md5"`, `"number"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("导出的 JSON 缺 %s: %s", want, b)
		}
	}
}

// 各种 builder 的语义：默认必填、Default 隐含可选、枚举显示名、数组元素类型、
// oneOf 用真实类型、opaque 必须给理由。
func TestBuilderSemantics(t *testing.T) {
	f := func(b contract.FieldSpec) contract.Field { return b.Field() }

	if !f(field.String("a")).Required {
		t.Error("默认应必填——漏写 Required 不该把必填字段悄悄变可选")
	}
	if f(field.String("a").Optional()).Required {
		t.Error("Optional 应生效")
	}
	if d := f(field.Number("n").Default(3)); d.Required || d.Default != 3 {
		t.Errorf("有默认值即视为可选: %+v", d)
	}
	if o := f(field.Enum("v", field.Opt("xiaoyan", "小燕"), field.Opt("asc"))); len(o.Options) != 2 ||
		o.Options[0].Label != "小燕" || o.Options[1].Label != "" {
		t.Errorf("枚举显示名可选: %+v", o.Options)
	}
	if a := f(field.Strings("names")); a.ItemType != contract.TString {
		t.Errorf("标量元素类型应可表达（[]string vs []number）: %+v", a)
	}
	if n := f(field.Json("cfg", struct {
		Count float64 `sokel:"count"`
	}{})); len(n.Fields) != 1 || n.Fields[0].Type != contract.TNumber {
		t.Errorf("嵌套结构: %+v", n)
	}

	// opaque 必须带理由，且理由落进 desc —— 评审时看得见为什么这里放弃了结构
	op := f(field.Object("blob", "外部透传，形状由上游决定"))
	if !op.Opaque || !strings.Contains(op.Desc, "上游") {
		t.Errorf("Opaque 应记录理由: %+v", op)
	}
}

// 类型作为构造参数：结构从 Go 类型来，而不是把字段抄一遍。
// 结构定义只有一处 —— 类型改了契约自动跟着，不会出现「声明与实际类型不同步」。
func TestShapeFromType(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name" label:"系统名"`
		Arch string `sokel:"arch"`
		Cpus int    `sokel:"cpus"`
	}
	type block struct {
		Text string `sokel:"text"`
	}

	j := field.Json("os", osInfo{}).Field()
	if len(j.Fields) != 3 {
		t.Fatalf("json 应取类型的字段: %+v", j.Fields)
	}
	if j.Fields[0].Name != "name" || j.Fields[0].Label != "系统名" {
		t.Errorf("tag 语义应保留: %+v", j.Fields[0])
	}
	if j.Fields[2].Type != contract.TNumber {
		t.Errorf("类型映射: %+v", j.Fields[2])
	}

	a := field.Array("blocks", []block{}).Field()
	if a.Type != contract.TArray || len(a.Fields) != 1 || a.Fields[0].Name != "text" {
		t.Errorf("array 的元素结构: %+v", a)
	}
}

// 这套 API 的要点是**漏不掉**：需要的信息都在参数位，编译器替你把关。
// 链式版本的漏洞是 `field.Json("os")` 什么都不加也能编译，悄悄产出无结构 json；
// 现在想声明一个 json 就必须交出类型，想走弱类型就必须走 Opaque 并写理由。
func TestTypeIsMandatory(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name"`
	}

	// json 必须给类型 → 结构一定在
	j := field.Json("os", osInfo{}).Field()
	if len(j.Fields) == 0 || j.Opaque {
		t.Errorf("Json 应带结构且非 opaque: %+v", j)
	}

	// 数组：一个参数同时覆盖标量与结构元素，不必记两个方法
	if s := field.Array("hosts", []string{}).Field(); s.ItemType != contract.TString || len(s.Fields) != 0 {
		t.Errorf("标量元素落 ItemType: %+v", s)
	}
	if o := field.Array("rows", []osInfo{}).Field(); len(o.Fields) != 1 || o.ItemType != "" {
		t.Errorf("结构元素落 Fields: %+v", o)
	}
	// []map[string]any 确实没有结构 —— 据实标 opaque，而不是留个空壳
	if m := field.Array("blobs", []map[string]any{}).Field(); !m.Opaque {
		t.Errorf("[]map[string]any 应标 opaque: %+v", m)
	}

	// 走弱类型的唯一入口，理由在参数位，漏不掉
	if op := field.Object("blob", "上游透传").Field(); !op.Opaque || op.Desc != "上游透传" {
		t.Errorf("Opaque 理由必填: %+v", op)
	}

	// oneOf 变体是真实类型，名字从类型来（写错类型名 = 编译失败）
	type docObject struct {
		Title string `sokel:"title"`
	}
	type blocks []osInfo
	u := field.OneOf("doc", docObject{}, blocks{}).Field()
	if len(u.OneOf) != 2 {
		t.Fatalf("oneOf 应有 2 个分支: %+v", u.OneOf)
	}
	if u.OneOf[0].Name != "docObject" || u.OneOf[0].Type != contract.TJSON {
		t.Errorf("分支0: %+v", u.OneOf[0])
	}
	if u.OneOf[1].Type != contract.TArray || len(u.OneOf[1].Fields) != 1 {
		t.Errorf("具名切片分支应为 array 并展开元素: %+v", u.OneOf[1])
	}
}

// 声明时给出的 Go 类型名必须留在契约里：生成 In/Out struct 时要**复用**该类型，
// 而不是照 Fields 重造一个形状相同的——两个等价类型互转正是要杜绝的运行时转换。
func TestGoTypeRecorded(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name"`
	}
	if j := field.Json("os", osInfo{}).Field(); j.GoType != "osInfo" {
		t.Errorf("json 应记住类型名: %+v", j)
	}
	if a := field.Array("rows", []osInfo{}).Field(); a.GoType != "osInfo" {
		t.Errorf("array 元素也应记住类型名: %+v", a)
	}
	// 匿名 struct 没有名字，生成时只能内联展开 —— 留空而不是编一个
	anon := field.Json("cfg", struct {
		X int `sokel:"x"`
	}{}).Field()
	if anon.GoType != "" {
		t.Errorf("匿名 struct 不该有类型名: %+v", anon)
	}
	// 标量数组无所谓类型名
	if s := field.Strings("hosts").Field(); s.GoType != "" {
		t.Errorf("标量数组不该有类型名: %+v", s)
	}
}

// OneOf 一个概念覆盖两种联合：标量传 ParamType，结构传 Go 类型，混用也行。
// 作者不必分辨「这算 Types 还是 OneOf」——那个区别只是契约层为了让 UI 少猜一次。
func TestOneOfUnifiesScalarAndStruct(t *testing.T) {
	type docObject struct {
		Title string `sokel:"title"`
	}

	// 标量联合 → 落 Types，首个是主类型
	sc := field.OneOf("chat_id", contract.TNumber, contract.TString).Field()
	if sc.Type != contract.TNumber || len(sc.Types) != 2 || len(sc.OneOf) != 0 {
		t.Errorf("标量联合应落 Types: %+v", sc)
	}

	// 结构联合 → 落 OneOf，分支名从真实类型来
	st := field.OneOf("doc", docObject{}, []docObject{}).Field()
	if len(st.OneOf) != 2 || len(st.Types) != 0 {
		t.Fatalf("结构联合应落 OneOf: %+v", st)
	}
	if st.OneOf[0].Name != "docObject" || st.OneOf[0].Type != contract.TJSON {
		t.Errorf("对象分支: %+v", st.OneOf[0])
	}
	if st.OneOf[1].Type != contract.TArray || len(st.OneOf[1].Fields) != 1 {
		t.Errorf("切片分支应为 array 并展开元素: %+v", st.OneOf[1])
	}

	// 混用：各归各位
	mx := field.OneOf("x", contract.TString, docObject{}).Field()
	if len(mx.Types) != 1 || len(mx.OneOf) != 1 {
		t.Errorf("混用应各归各位: types=%v oneOf=%v", mx.Types, mx.OneOf)
	}
}

// 整数：契约类型仍是 number（线协议不变、平台不受影响），靠 GoType 带一个「其实是整数」
// 的生成提示。这样 runtime.NumCPU() 这种 int 不必在实现里 float64(...) 转一道。
// 刻意只在插件侧区分：平台的类型系统没有 int 这个概念，为它改线协议不值当。
func TestIntStaysNumberInContract(t *testing.T) {
	i := field.Int("count").Field()
	if i.Type != contract.TNumber {
		t.Errorf("契约类型应仍是 number（平台不认 int）: %+v", i)
	}
	if i.GoType != "int" {
		t.Errorf("应带整数提示: %+v", i)
	}
	if s := field.Ints("sizes").Field(); s.Type != contract.TArray || s.ItemType != contract.TNumber || s.GoType != "int" {
		t.Errorf("整数数组: %+v", s)
	}
	// 浮点不带提示，保持原样
	if f := field.Number("ratio").Field(); f.GoType != "" {
		t.Errorf("浮点不该带整数提示: %+v", f)
	}
}

// 从 Go 类型推导时同样要认出整数字段——Shape/Json 走的是反射那条路。
func TestDeriveIntFromStruct(t *testing.T) {
	type row struct {
		N     int     `sokel:"n"`
		Big   int64   `sokel:"big"`
		Ratio float64 `sokel:"ratio"`
	}
	fs := contract.DeriveFields(reflect.TypeOf(row{}))
	idx := map[string]contract.Field{}
	for _, f := range fs {
		idx[f.Name] = f
	}
	if idx["n"].Type != contract.TNumber || idx["n"].GoType != "int" {
		t.Errorf("int 字段: %+v", idx["n"])
	}
	if idx["big"].GoType != "int64" {
		t.Errorf("int64 应保留宽度（生成时不该降成 int 丢精度）: %+v", idx["big"])
	}
	if idx["ratio"].GoType != "" {
		t.Errorf("float64 不该带整数提示: %+v", idx["ratio"])
	}
}

// field.Json 传 map[string]T 是很自然的写法（「字段名 → 权重」这类），
// 它是 valueType 而非 fields。不认的话只能退回 opaque —— 白白丢掉已知的值类型。
func TestJsonWithMapShape(t *testing.T) {
	m := field.Json("field_boosts", map[string]float64{}).Field()
	if m.ValueType == nil || m.ValueType.Type != contract.TNumber {
		t.Errorf("map[string]float64 应落 valueType(number): %+v", m)
	}
	if m.Opaque {
		t.Errorf("值类型已知就不该是 opaque: %+v", m)
	}
	// map[string]any 确实没有结构
	if a := field.Json("blob", map[string]any{}).Field(); !a.Opaque {
		t.Errorf("map[string]any 应为 opaque: %+v", a)
	}
	// 值是结构时递归展开
	type row struct {
		N string `sokel:"n"`
	}
	r := field.Json("rows", map[string]row{}).Field()
	if r.ValueType == nil || len(r.ValueType.Fields) != 1 {
		t.Errorf("map[string]struct 应递归展开: %+v", r.ValueType)
	}
}

// 数组元素是联合类型：`[]OneOf<文本, 图片>`。
// 多模态消息的 parts 就是这个形状——一条消息里逐段可能是文本也可能是图片。
// 契约上不必加新字段：type=array + oneOf 即「元素是联合」，
// 与 type=json + oneOf（字段本身是联合）区分开。
func TestArrayOfUnion(t *testing.T) {
	type textPart struct {
		Text string `sokel:"text"`
	}
	type imagePart struct {
		URL string `sokel:"url"`
	}

	f := field.ArrayOf("parts", textPart{}, imagePart{}).Label("片段").Field()
	if f.Type != contract.TArray {
		t.Fatalf("应是数组: %+v", f)
	}
	if len(f.OneOf) != 2 {
		t.Fatalf("元素应有 2 个分支: %+v", f.OneOf)
	}
	if f.OneOf[0].Name != "textPart" || len(f.OneOf[0].Fields) != 1 {
		t.Errorf("分支0 应展开结构: %+v", f.OneOf[0])
	}
	if f.OneOf[1].GoType != "imagePart" {
		t.Errorf("分支应记住 Go 类型名（生成时要用）: %+v", f.OneOf[1])
	}
	// 元素有结构声明 → 不是 opaque
	if f.Opaque {
		t.Errorf("元素声明了形状就不该 opaque: %+v", f)
	}
	// 与「字段本身是联合」区分：那个是 json
	if u := field.OneOf("doc", textPart{}).Field(); u.Type != contract.TJSON {
		t.Errorf("字段级联合仍是 json: %+v", u)
	}
}

// 「任意 JSON 值」：http 的请求体/响应体就是这个形状——json 模式是对象、
// raw 模式是字符串、也可能是数组。Opaque 表达不了它（那是「对象但键不定」）。
//
// 用已有的联合类型表达最自然：any 就是所有类型的联合。于是
// 校验放行任何值、生成 any、审计仍看得见理由——三件事都不必新造机制。
func TestAny(t *testing.T) {
	f := field.Any("body", "请求体形状由目标接口决定，平台不介入").Label("请求体").Field()
	if f.Type != contract.TJSON {
		t.Errorf("主类型仍是 json（画布按 json 渲染）: %v", f.Type)
	}
	if len(f.Types) < 4 {
		t.Errorf("应展开成各类型的联合: %v", f.Types)
	}
	var hasString, hasArray bool
	for _, ty := range f.Types {
		hasString = hasString || ty == contract.TString
		hasArray = hasArray || ty == contract.TArray
	}
	if !hasString || !hasArray {
		t.Errorf("字符串与数组都得在联合里（raw 模式发字符串）: %v", f.Types)
	}
	if !f.Opaque || f.Desc == "" {
		t.Errorf("仍是不透明字段，理由必填——否则与「忘了声明」分不开: %+v", f)
	}
}

// field.Array 的第二个参数是**元素形状**，不是描述文字。
//
// 传一句描述会静默产出无结构数组——下游拿到 messages[0] 不知道里面有什么，
// 变量选择器展不开、引用也没有校验。gmail 就这么错了一版，而且没有任何地方报错。
func TestArrayShapeVsDescription(t *testing.T) {
	type Item struct {
		ID      string `sokel:"id"`
		Subject string `sokel:"subject"`
	}
	withShape := field.Array("messages", []Item{}).Field()
	if len(withShape.Fields) != 2 || withShape.Fields[0].Name != "id" {
		t.Fatalf("给了元素形状就该有元素字段: %+v", withShape.Fields)
	}
	// 传描述文字：产不出结构（这正是要在别处拦住的写法）
	withDesc := field.Array("messages", "邮件列表").Field()
	if len(withDesc.Fields) != 0 {
		t.Errorf("非切片不该猜出结构: %+v", withDesc.Fields)
	}
	// 标量元素：记 ItemType，下游知道是字符串数组而不是对象数组
	strs := field.Array("label_ids", []string{}).Field()
	if strs.ItemType != contract.TString {
		t.Errorf("标量元素要记 ItemType, got %q", strs.ItemType)
	}
	// []map[string]any / []any 确实没有结构 → opaque（有理由的无结构，不是漏声明）
	opaque := field.Array("blocks", []map[string]any{}).Field()
	if !opaque.Opaque {
		t.Error("[]map[string]any 应标记 opaque")
	}
}
