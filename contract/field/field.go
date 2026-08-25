// Package field 提供声明插件契约的 builder。
//
// 为什么不用 struct tag（docs/plugin-sdk-multilang.md）：
//   - tag 是字符串，`lable:"..."` 拼错时**编译器一声不吭**；builder 写错方法名直接编译失败
//   - 结构化信息塞进字符串已经在吃力——enum 的显示名要发明 `=` 分隔，oneof 的类型名只能写字符串
//   - `oneof` 在这里是**真实类型引用**，类型改名/删除立刻编译失败，不必等生成期
//   - 无结构的 json 必须 .Opaque(理由) 才能声明：API 里压根没有"随手 map"这个省事选项
//
// 声明是纯数据，可直接序列化为 JSON 供其他语言 SDK 生成对应类型。
// 与契约同模块：builder 只用契约类型，没有一点传输的东西，故不该压在 SDK 里——
// 压在那里的话，内核（httpcore/llmcore）想声明自己的契约就得反过来依赖 SDK。
package field

import (
	"reflect"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// B：字段 builder。实现 contract.FieldSpec，可直接放进 Inputs()/Outputs() 返回值。
type B struct{ f contract.Field }

// Field 交出构造好的契约字段（实现 contract.FieldSpec）。
func (b *B) Field() contract.Field { return b.f }

func mk(name string, t contract.ParamType) *B {
	// 默认必填：与既有 tag 语义一致（非 optional、无 default 即必填），
	// 迁移时不易出错——漏写 .Required() 不会把必填字段悄悄变成可选。
	return &B{f: contract.Field{Name: name, Type: t, Required: true}}
}

// —— 构造函数：需要的信息一律作**必填参数**，而不是可选的链式调用 ——
//
// 链式的问题是漏得掉：`field.Json("os")` 什么都不加也能编译，悄悄产出一个无结构 json。
// 把类型放进参数位，编译器就替你把关了。

func String(name string) *B { return mk(name, contract.TString) }
func Text(name string) *B   { return mk(name, "text") }
func Number(name string) *B { return mk(name, contract.TNumber) }

// Int 整数。**契约类型仍是 number**——线协议没有 int，平台也不认；
// 靠 GoType 带一个「其实是整数」的生成提示，让 Go/Python 侧生成 int 而不是 float64。
// 只在插件侧区分：为它改线协议不值当，而实现里每个数字字段都 float64(...) 转一道太难看。
func Int(name string) *B  { b := mk(name, contract.TNumber); b.f.GoType = "int"; return b }
func Bool(name string) *B { return mk(name, contract.TBool) }
func File(name string) *B { return mk(name, contract.TFile) }

// Files 文件列表（array<file>——文件列表的唯一表达，web docs/type-system.md §12）。
func Files(name string) *B {
	b := mk(name, contract.TArray)
	b.f.ItemType = contract.TFile
	return b
}

// Secret 密文字段（**凭证专用**）：表单打码、平台加密存储。
//
// 与 String 的分别不在 Go 类型（都是 string），而在平台怎么对待它——
// 所以必须是独立的构造函数，不能靠调用方记得加一个可选链式调用。
func Secret(name string) *B { return mk(name, "secret") }

// Select 下拉选择（**凭证专用**）：候选值作必填参数。
//
// 类型名是 "select" 而不是 Enum 的 "enum"——凭证表单认的是前者。
// 两个名字确实别扭，但改任何一边都要动存量契约，而这里只需要选对。
func Select(name string, options ...string) *B {
	b := mk(name, "select")
	for _, o := range options {
		b.f.Options = append(b.f.Options, contract.Option{Value: o, Label: o})
	}
	return b
}

// Enum 枚举；候选值作必填参数——空枚举没有意义。
func Enum(name string, opts ...contract.Option) *B {
	b := mk(name, contract.TEnum)
	b.f.Options = opts
	return b
}

// Json 对象，**结构由 Go 类型给出**：
//
//	field.Json("os", OSInfo{})
//
// 结构定义只有一处——类型改了契约自动跟着，不会出现「声明与实际不同步」。
// 确实没有结构可言时用 Object（是对象、键不定）或 Any（连类别都不定）。
func Json(name string, shape any) *B {
	b := mk(name, contract.TJSON)
	t := reflect.TypeOf(shape)
	// map[string]T：键运行期才知道、值类型统一 —— 这是 valueType 而非 fields。
	// 传 map[string]float64{} 是很自然的写法（如「字段名 → 权重」），不认它就只能退回 opaque。
	if t != nil && t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
		el := t.Elem()
		if el.Kind() != reflect.Interface {
			vt := &contract.Field{Type: scalarType(el), Fields: contract.DeriveFields(el), GoType: goTypeName(el)}
			if el.Kind() == reflect.Struct {
				vt.Type = contract.TJSON
			}
			if n := intKindOf(el); n != "" {
				vt.GoType = n
			}
			b.f.ValueType = vt
			return b
		}
		b.f.Opaque = true // map[string]any：确实没有结构
		return b
	}
	b.f.Fields = contract.DeriveFields(t)
	b.f.GoType = goTypeName(t) // 生成时复用该类型，不重造等价结构
	return b
}

// intKindOf：整数类型名（与 sokel 侧同规则），非整数返回空。
func intKindOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return t.Kind().String()
	}
	return ""
}

// Array 数组，**元素类型由切片给出**：
//
//	field.Array("hosts", []string{})   // 标量元素
//	field.Array("blocks", []Block{})   // 结构元素
//
// 一个参数同时覆盖两种情况：标量落 ItemType，结构落 Fields。
func Array(name string, shape any) *B {
	b := mk(name, contract.TArray)
	t := reflect.TypeOf(shape)
	if t == nil || (t.Kind() != reflect.Slice && t.Kind() != reflect.Array) {
		// 传了非切片：留空而不是猜。生成期跑 schema 时就会暴露契约不对。
		return b
	}
	el := t.Elem()
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.Struct:
		b.f.Fields = contract.DeriveFields(el)
		b.f.GoType = goTypeName(el)
	case reflect.Map, reflect.Interface:
		b.f.Opaque = true // []map[string]any / []any：确实没有结构
	default:
		b.f.ItemType = scalarType(el)
	}
	return b
}

// 常见标量数组的快捷方式。
func Strings(name string) *B { return Array(name, []string{}) }
func Numbers(name string) *B { return Array(name, []float64{}) }

// Ints 整数数组。
func Ints(name string) *B  { b := Array(name, []int{}); b.f.GoType = "int"; return b }
func Bools(name string) *B { return Array(name, []bool{}) }

// OneOf 声明「这个字段接受多种可能」。**标量类型与 Go 类型都能传，自动分流**：
//
//	field.OneOf("chat_id", contract.TNumber, contract.TString)   // 标量联合：值是数字或字符串
//	field.OneOf("doc", DocObject{}, BlocksArray{})     // 结构联合：形状不同，各有字段
//	field.OneOf("x", contract.TString, DocObject{})         // 混用也行
//
// 为什么声明层只有一个概念、契约层却是两个字段（Types / OneOf）：
// 前端对二者的**渲染方式不同**——标量联合是一个输入框接受多种类型，结构联合是分段
// 选择器先选形状再渲染该分支的字段。契约里分开存，UI 不必靠"分支有没有 fields"去猜；
// 而作者不该为这个区别分心，所以声明层合并。
//
// 结构分支用**真实类型**：类型改名/删除立刻编译失败，不必等生成期。
func OneOf(name string, variants ...any) *B {
	b := mk(name, contract.TJSON)
	for _, v := range variants {
		// 标量类型：进 Types（首个作主类型，与既有语义一致）
		if pt, ok := v.(contract.ParamType); ok {
			if len(b.f.Types) == 0 {
				b.f.Type = pt
			}
			b.f.Types = append(b.f.Types, pt)
			continue
		}
		t := reflect.TypeOf(v)
		if t == nil {
			continue
		}
		b.f.OneOf = append(b.f.OneOf, variantOf(t))
	}
	return b
}

// ArrayOf 声明「数组，且元素是联合类型」：[]OneOf<A, B, …>。
//
// 多模态消息的 parts 就是这个形状——一条消息里逐段可能是文本、也可能是图片。
// 没有它就只能退化成单一元素类型（丢掉其余形状）或 Opaque（正是要避免的）。
//
// 契约上不加新字段：type=array + oneOf 即「**元素**是联合」，
// 与 OneOf 的 type=json + oneOf（**字段本身**是联合）区分开。
func ArrayOf(name string, variants ...any) *B {
	b := mk(name, contract.TArray)
	for _, v := range variants {
		t := reflect.TypeOf(v)
		if t == nil {
			continue
		}
		b.f.OneOf = append(b.f.OneOf, variantOf(t))
	}
	return b
}

// Any 声明「任意 JSON 值」：连是不是对象都不定。
//
// 与 Object 的区别值得记住：Object 是「**是**对象，但键不定」（→ map[string]any，
// 校验要求必须是对象）；Any 是「可能是对象、数组、字符串、数字、布尔」（→ any，校验放行一切）。
// http 的请求体/响应体就是后者：json 模式给对象，raw 模式给字符串。
//
// 实现上用已有的联合类型表达——any 本就是所有类型的联合。于是校验放行任何值、
// 生成 any、审计仍看得见理由，三件事都不必新造机制。理由同样必填。
func Any(name, reason string) *B {
	b := mk(name, contract.TJSON)
	b.f.Types = []contract.ParamType{contract.TJSON, contract.TArray, contract.TString, contract.TNumber, contract.TBool}
	b.f.Opaque = true
	b.f.Desc = reason
	return b
}

// Object 声明「是一个对象，但键不由本插件决定」——如上游原样透传的元数据。
//
// 与 Json 的区别是结构从哪来：Json 的结构由 Go 类型给出，Object 压根没有可给的结构。
// 与 Any 的区别见 Any。
//
// **理由必填**。拦住随手使用的不是名字而是这个参数：想省事也得先写清楚为什么省不掉。
// 这类字段（Object 与 Any）在契约里统称不透明字段，审计会逐一列出。
func Object(name, reason string) *B {
	b := mk(name, contract.TJSON)
	b.f.Opaque = true
	b.f.Desc = reason
	return b
}

// variantOf 把一个 Go 类型转成 oneOf 分支。
// 数组分支的 GoType 取**元素**类型：匿名切片本身没有名字（[]Block{} 的 Name() 是空串），
// 而生成代码要的正是元素类型（[]schema.Block）。显示名则退而取「元素名 + List」。
func variantOf(t reflect.Type) contract.OneOfVariant {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		el := t.Elem()
		for el.Kind() == reflect.Pointer {
			el = el.Elem()
		}
		name := t.Name() // 具名切片（type BlocksArray []Block）有名字
		if name == "" {
			name = el.Name() + "List"
		}
		return contract.OneOfVariant{Name: name, GoType: el.Name(), Type: contract.TArray, Fields: contract.DeriveFields(el)}
	}
	return contract.OneOfVariant{Name: t.Name(), GoType: t.Name(), Type: contract.TJSON, Fields: contract.DeriveFields(t)}
}

// goTypeName：具名类型取其名字；匿名 struct 没有名字可用（生成时只能内联展开）。
func goTypeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// scalarType：Go 标量 kind → 契约类型。
func scalarType(t reflect.Type) contract.ParamType {
	switch t.Kind() {
	case reflect.Bool:
		return contract.TBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return contract.TNumber
	}
	return contract.TString
}

func (b *B) Label(s string) *B { b.f.Label = s; return b }
func (b *B) Desc(s string) *B  { b.f.Desc = s; return b }
func (b *B) Required() *B      { b.f.Required = true; return b }

// Opaque 显式承认「这里没有可声明的结构」，并说明为什么。
//
// 与 field.Object/Any 的区别只是**位置**：那两个是构造时就知道没结构；
// 这个用于构造函数已经定了形状（如 Array）、但元素结构确实说不出来的情况——
// 数组操作的 output 就是：元素形状随上游数组而定，运行期才知道。
// 理由必填：不写理由的无结构字段会被 sokel-gen 的审计追着报，那是故意的。
func (b *B) Opaque(reason string) *B {
	b.f.Opaque = true
	if b.f.Desc == "" {
		b.f.Desc = reason
	}
	return b
}
func (b *B) Optional() *B { b.f.Required = false; return b }

// Default 默认值；有默认值即视为可选（调用方不传也能跑）。
func (b *B) Default(v any) *B { b.f.Default = v; b.f.Required = false; return b }

// Opt 构造一个候选项：值本身可读时只给值，是代码时补显示名。
func Opt(value string, label ...string) contract.Option {
	o := contract.Option{Value: value}
	if len(label) > 0 {
		o.Label = label[0]
	}
	return o
}

// Types 顶层联合（number|string 这类标量联合，不是结构联合——那是 OneOf）。
func (b *B) Types(ts ...contract.ParamType) *B {
	if len(ts) > 0 {
		b.f.Type = ts[0]
		b.f.Types = ts
	}
	return b
}
