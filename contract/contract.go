// Package contract：插件契约的**唯一定义** —— 字段类型、声明、与 Go 类型之间的绑定。
//
// 为什么单独一个包（而不是留在 go-sdk）：契约有两个消费者——
//   - go-sdk：插件作者声明契约、按契约绑定入参/产出出参；
//   - server：平台按契约做类型归一与运行前校验、画布按契约渲染。
//
// 此前两边**各定义了一份**：SDK 的 Field 是全量的，平台那份只有 name/type/fields/valueType。
// 于是 SDK 声明了而平台那份没有的东西（联合类型、枚举、必填、oneOf、multiple），
// 平台就看不见。这类「同一件事两处定义」的漂移今天已经栽过一次
// （入参绑定两侧不对称，嵌套 snake_case 字段静默绑空）。
//
// 本包不依赖传输，也不依赖平台类型：契约是数据，取字节/发请求都不在这里。
package contract

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// ParamType 与平台/前端一致的字段类型（画布据此渲染入/出参绑定）。
type ParamType string

const (
	TString ParamType = "string"
	TNumber ParamType = "number"
	TBool   ParamType = "boolean"
	TFile   ParamType = "file"
	TJSON   ParamType = "json"
	TArray  ParamType = "array"
	TEnum   ParamType = "enum"
)

// Field 操作的一个入/出参契约项（形态对齐前端 ParamSpec）。
type Field struct {
	Name     string      `json:"name"`
	Label    string      `json:"label,omitempty"`
	Type     ParamType   `json:"type"`
	Types    []ParamType `json:"types,omitempty"` // 联合类型（如 number|string）：变量绑定/校验接受其中任一；Type 为主类型
	Required bool        `json:"required,omitempty"`
	Default  any         `json:"default,omitempty"`
	Desc     string      `json:"desc,omitempty"`
	Options  []Option    `json:"options,omitempty"` // enum 候选值（`enum:"a,b"` 或带显示名 `enum:"a=甲,b=乙"`）
	Fields   []Field     `json:"fields,omitempty"`  // json 的子字段 / array 的元素字段

	// OneOf：结构联合，该字段接受列出的几种结构之一。
	// 注意：**运行时反射产不出它**——Go 没有联合类型，且反射拿不到「类型名字符串 → 类型」的映射。
	// 它由 sokel-gen 的 AST 解析从 `oneof:"TypeA,TypeB"` tag 产出（docs/plugin-sdk-multilang.md §1）。
	OneOf []OneOfVariant `json:"oneOf,omitempty"`
	// ValueType：动态键（JSON Schema 的 additionalProperties），键运行期才知道、值类型统一。
	// 由 map[string]T 推导：T 是 any → 不产出（opaque）；T 是具体类型 → 递归展开。与 Fields 互斥。
	ValueType *Field `json:"valueType,omitempty"`
	// GoType：声明时给出的 Go 类型名（如 "OSInfo"）。**仅代码生成用的提示**，
	// 其他语言的生成器忽略它即可（协议消费方也不需要）。
	//
	// 为什么必须记：field.Json("os", OSInfo{}) 已经交出了类型，生成 Out struct 时就该
	// 复用 OSInfo 本身，而不是照 Fields 重新生成一个等价结构——否则实现侧会出现两个
	// 形状相同的类型，赋值得逐字段转换，正是要杜绝的那种运行时转换。
	GoType string `json:"goType,omitempty"`
	// ItemType：数组元素的标量类型（[]string 与 []number 在契约里得能区分；
	// Fields 只能表达"元素是对象时的字段"，标量元素此前无处安放）。
	ItemType ParamType `json:"itemType,omitempty"`
	// Opaque：该字段没有可声明的结构（裸 map[string]any / any）。
	// 弱类型是合法选择，但要成为**看得见的决定**而不是默认路径（docs/type-system.md §3）：
	// UI 据此标注「无结构约束」，平台侧据此跳过结构校验——
	// 否则「没声明结构」与「声明了但恰好为空」分不清。
	Opaque bool `json:"opaque,omitempty"`
}

// Option：enum 的一个候选项。Label 为空时前端回退显示 Value——
// 值本身可读时（asc/desc）不必再写一遍，值是代码时（发音人 xiaoyan）才需要显示名。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// OneOfVariant：oneOf 的一个分支。Name 是分支标识（报错定位用），不落盘到运行值里——
// 运行值就是该分支本身的形状，不带 discriminator 包装。
type OneOfVariant struct {
	Name  string    `json:"name"`
	Label string    `json:"label,omitempty"`
	Type  ParamType `json:"type"`
	// GoType：该分支的 Go 类型名——**数组分支指的是元素类型**（[]Block → "Block"）。
	// 与 Name 分开是因为匿名切片没有名字：reflect.TypeOf([]Block{}).Name() 返回空串，
	// 只用 Name 会生成出 `[]schema.` 这种坏代码（实测踩到）。
	GoType string  `json:"goType,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// FileRef：标记「这个类型是平台文件引用」。SDK 的 sokel.File 实现它。
//
// 契约包只需要**认出**文件字段（报 type=file、绑定时交给标准库）；
// 取字节与上传属于运行时，那是 SDK 的事，不该为了识别一个字段把运行时拖进来。
type FileRef interface{ FileRef() }

var fileRefType = reflect.TypeOf((*FileRef)(nil)).Elem()

// isFileRef：t 是不是文件引用（*File 或 File 都算）。
func isFileRef(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t.Implements(fileRefType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(fileRefType)
}

// DeriveFields 从 Go 类型推导契约字段（供 sokel/field 的 .Shape() 用）。
//
// 这里用反射不违背「运行时零反射」：它只在**声明期**执行——sokel-gen 运行 schema 取声明的
// 那一刻——产物是生成的静态 Field 字面量。反射的问题从来不是反射本身，是运行时反射。
func DeriveFields(t reflect.Type) []Field { return deriveFields(t) }

// deriveFields 从入/出参 struct 反射推导契约字段。
//
//	tag：`sokel:"name,optional"` + 独立 `label:"..."` `desc:"..."` `default:"..."`。
//	类型：bool→boolean，整/浮→number，string→string，*File→file，slice→array，struct/map→json（递归子字段）。
func deriveFields(t reflect.Type) []Field {
	return deriveFieldsSeen(t, map[reflect.Type]bool{})
}

// deriveFieldsSeen：带「当前递归路径上已访问类型集」的推导。seen 用于环检测——
// 自引用/递归 struct（如树形 Block{ Blocks []*Block }）再次进入同一类型即停，返回空，
// 避免无限递归栈溢出（此前 Register 启动推导递归类型契约时直接崩进程）。
// path-scoped：进入时标记、退出时删除，故兄弟分支的同类型不受影响（不是全局去重）。
func deriveFieldsSeen(t reflect.Type, seen map[reflect.Type]bool) []Field {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if seen[t] {
		return nil // 环点：本类型已在当前路径上 → 停止下钻
	}
	seen[t] = true
	defer delete(seen, t)
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, optional := parseSokelTag(sf)
		if name == "-" {
			continue
		}
		f := Field{Name: name, Label: sf.Tag.Get("label"), Desc: sf.Tag.Get("desc")}
		f.Type, f.Fields, f.ValueType = fieldTypeSeen(sf.Type, seen)
		// `opaque:"理由"`：类型定义里的无结构字段也能说明理由。
		// 不然只有 builder 的 field.Object 能写，而结构里的 map[string]any 就成了没人解释的黑洞。
		taggedOpaque := false
		if rv, ok := sf.Tag.Lookup("opaque"); ok && strings.TrimSpace(rv) != "" {
			taggedOpaque = true
			if f.Desc == "" {
				f.Desc = strings.TrimSpace(rv)
			}
		}
		// 整数：契约类型是 number，用 GoType 记住它其实是整数（及宽度）。
		if f.Type == TNumber && f.GoType == "" {
			f.GoType = intKindName(sf.Type)
		}
		// `type:"..."`：显式指定契约类型，覆盖反射推导（string/number/boolean/file/json/array/enum）。
		// 逗号分隔 = 联合类型（如 `type:"number,string"`，chat_id 数字或字符串）：首个为主类型（显示/固定值控件），
		// 全部进 Types（变量绑定/校验时接受其中任一，别的仍拦——不放过错误绑定）。
		if tv, ok := sf.Tag.Lookup("type"); ok && strings.TrimSpace(tv) != "" {
			var ts []ParamType
			for _, p := range strings.Split(tv, ",") {
				if p = strings.TrimSpace(p); p != "" {
					ts = append(ts, ParamType(p))
				}
			}
			if len(ts) > 0 {
				f.Type = ts[0]
				if len(ts) > 1 {
					f.Types = ts
				}
			}
		}
		// `enum:"a,b,c"`：把 string 字段声明为 enum + 候选值（画布据此渲染下拉、限定取值）。
		// `enum:"a,b"` → 纯值；`enum:"a=甲,b=乙"` → 带显示名。
		// 用 = 而不是 : 分隔：枚举值里出现冒号（URL、时间）比出现等号常见得多。
		if ev, ok := sf.Tag.Lookup("enum"); ok && strings.TrimSpace(ev) != "" {
			f.Type = TEnum
			for _, o := range strings.Split(ev, ",") {
				if o = strings.TrimSpace(o); o == "" {
					continue
				}
				val, label, _ := strings.Cut(o, "=")
				f.Options = append(f.Options, Option{
					Value: strings.TrimSpace(val),
					Label: strings.TrimSpace(label),
				})
			}
		}
		// opaque 判定必须在 type / enum tag **之后**：它们会改写 f.Type，
		// 而「有没有结构」取决于最终类型。此前放在前面，于是 `any` + `type:"number,string"`
		// （标量联合，如 id 可能是数字或字符串）被误标成 opaque —— opaque 审计工具第一次跑就抓到了。
		// 没有可声明的结构 = opaque。两种来源：
		//   json  —— 裸 map / any / 空 struct
		//   array —— 元素无结构（[]map[string]any、[]any；`type tag = map[string]any` + []tag 也是这种）
		// 标量元素的数组（[]string）不算 opaque：元素类型是明确的，记进 ItemType。
		// （早先这里写着「契约里暂无处表达」——那句话过时了，Field.ItemType 一直都在，
		//   只是 builder 那条路在填、反射这条路漏了。漏的后果是下游看到一个"无结构数组"，
		//   和真正没结构的 []any 混为一谈。）
		if f.Type == TArray && len(f.Fields) == 0 {
			f.ItemType = elemScalarType(sf.Type)
		}
		// 显式标了 opaque 就以标签为准：自动判定只认「裸 map / any / 元素无结构」这几种形状，
		// 认不出**递归**（[]*Block 里又有 []*Block —— 环检测让子字段为空，但它并非没有结构，
		// 只是契约表达不了）。早先这里是无条件赋值，把标签连同它的理由一起抹掉了。
		f.Opaque = taggedOpaque ||
			((f.Type == TJSON || f.Type == TArray) && len(f.Fields) == 0 && f.ValueType == nil &&
				f.ItemType == "" && (f.Type == TJSON || elemHasNoStructure(sf.Type)))
		if dv, ok := sf.Tag.Lookup("default"); ok {
			f.Default = coerceDefault(dv, f.Type)
		}
		// 必填判定：默认「非 optional 且无 default 且非指针」。
		// 文件例外：文件参数只能声明为指针 *File（值类型会被当 json struct），若沿用「指针=可选」就永远标不了必填 —— 故文件按 ,optional 显式判定。
		f.Required = !optional && f.Default == nil && (sf.Type.Kind() != reflect.Pointer || f.Type == TFile)
		out = append(out, f)
	}
	return out
}

// elemScalarType：切片的标量元素类型（[]string→string、[]int→number、[]bool→boolean）；
// 元素不是标量（结构体、map、any）时返回空——那些要么有 Fields，要么是真的没结构。
func elemScalarType(t reflect.Type) ParamType {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return ""
	}
	el := t.Elem()
	if isFileRef(el) { // []*File / []File → 元素类型 file（array<file>）
		return TFile
	}
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.String:
		return TString
	case reflect.Bool:
		return TBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return TNumber
	}
	return ""
}

// fieldType 把 Go 类型映射到 ParamType（并对 json/array 递归推导子字段、对 map 推导 valueType）。
func fieldType(t reflect.Type) (ParamType, []Field, *Field) {
	return fieldTypeSeen(t, map[reflect.Type]bool{})
}

// fieldTypeSeen：带路径已访问集的类型映射（环检测经 seen 传递给 deriveFieldsSeen，见其注释）。
// 第三个返回值是 valueType（仅 map[string]T 且 T 非 any 时非空），见 Field.ValueType。
func fieldTypeSeen(t reflect.Type, seen map[reflect.Type]bool) (ParamType, []Field, *Field) {
	if isFileRef(t) {
		return TFile, nil, nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return TBool, nil, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return TNumber, nil, nil
	case reflect.String:
		return TString, nil, nil
	case reflect.Slice, reflect.Array:
		// 文件数组（[]*File / []File）：报 array<file>（ItemType 由 deriveFields 的
		// elemScalarType 补）——文件列表的唯一表达（web docs/type-system.md §12）；
		// 不做原始 struct 反射（否则输出 i_d/u_r_l + 内联 data 噪音字段）。
		if isFileRef(t.Elem()) {
			return TArray, nil, nil
		}
		return TArray, deriveFieldsSeen(t.Elem(), seen), nil
		// 注：array 的 Opaque 由 deriveFieldsSeen 统一判定（元素无结构 → 整个数组无约束）。
	case reflect.Struct:
		return TJSON, deriveFieldsSeen(t, seen), nil
	case reflect.Map:
		// map[string]any → 标 opaque（无结构约束）；
		// map[string]T（T 具体）→ 递归展开为 valueType（键运行期才知道、值类型统一）。
		// 于是 Go 的语言层面天然对应 typed/opaque 二分，不用新造概念（docs/type-system.md §3）。
		// 非 string 键无法用 JSON 对象表达，一律按 opaque 处理。
		if t.Key().Kind() != reflect.String || t.Elem().Kind() == reflect.Interface {
			return TJSON, nil, nil // valueType 为 nil → 调用方据此标 Opaque
		}
		et, ef, ev := fieldTypeSeen(t.Elem(), seen)
		return TJSON, nil, &Field{Type: et, Fields: ef, ValueType: ev}
	case reflect.Interface:
		return TJSON, nil, nil
	default:
		return TString, nil, nil
	}
}

// parseSokelTag 取字段的对外名与 optional 标记。无 sokel tag 时用字段名的下划线小写形式。
// ParseTag 取字段的对外名与 optional 标记（导出给 SDK 复用）。
func ParseTag(sf reflect.StructField) (string, bool) { return parseSokelTag(sf) }

// ApplyDefaultTag 把 `default:"..."` 写进字段（导出给 SDK 复用）。
func ApplyDefaultTag(v reflect.Value, sf reflect.StructField) { applyDefaultTag(v, sf) }

func parseSokelTag(sf reflect.StructField) (name string, optional bool) {
	tag := sf.Tag.Get("sokel")
	if tag == "" {
		return toSnake(sf.Name), false
	}
	parts := strings.Split(tag, ",")
	name = strings.TrimSpace(parts[0])
	if name == "" {
		name = toSnake(sf.Name)
	}
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "optional" {
			optional = true
		}
	}
	return name, optional
}

// bindInput 把平台传来的 input JSON 对象按 sokel tag 绑定进入参 struct（含文件字段）。
// BindInput 把平台传来的 input JSON 绑进入参 struct，**按 sokel tag 递归**。
//
// 递归是必须的：出参那侧（StructToVars）一直按 sokel tag 递归展开，入参却曾只认顶层，
// 于是嵌套结构里的 snake_case 字段静默绑空——Go 的 json 大小写不敏感匹配跨不过下划线，
// `doc_id` 落不进 `DocID`，而且不报错。两侧必须互为逆运算。
func BindInput(input json.RawMessage, dst any) error { return bindInput(input, dst) }

func bindInput(input json.RawMessage, dst any) error {
	if len(input) == 0 {
		return nil
	}
	return bindValue(input, reflect.ValueOf(dst).Elem())
}

// bindValue 按目标类型递归解码：struct 按 sokel tag 取键，slice/map 逐元素下钻，
// 其余交给标准库。
func bindValue(raw json.RawMessage, v reflect.Value) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return bindValue(raw, v.Elem())

	case reflect.Struct:
		// 文件引用及其它自带对齐 json tag 的平台类型：交给标准库，别按 sokel tag 拆。
		if isFileRef(v.Type()) {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name, _ := parseSokelTag(sf)
			val, ok := obj[name]
			if !ok {
				applyDefaultTag(v.Field(i), sf)
				continue
			}
			if err := bindValue(val, v.Field(i)); err != nil {
				return err
			}
		}
		return nil

	case reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return json.Unmarshal(raw, v.Addr().Interface()) // 不是数组（如 []byte 的 base64）：按标准库来
		}
		out := reflect.MakeSlice(v.Type(), len(items), len(items))
		for i, it := range items {
			if err := bindValue(it, out.Index(i)); err != nil {
				return err
			}
		}
		v.Set(out)
		return nil

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		out := reflect.MakeMapWithSize(v.Type(), len(obj))
		for k, val := range obj {
			ev := reflect.New(v.Type().Elem()).Elem()
			if err := bindValue(val, ev); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(k), ev)
		}
		v.Set(out)
		return nil
	}
	return json.Unmarshal(raw, v.Addr().Interface())
}

// StructToVars 是 structToVars 的导出视图（生成的代码与测试用）。
func StructToVars(o any) map[string]any { return structToVars(o) }

// structToVars 把出参 struct 转成 {对外名: 值} 映射（供平台落为节点输出变量）。
// 递归按 sokel tag 展开嵌套 struct/slice，使嵌套字段名也走契约名；*File 原样带出（其 json tag 已对齐）。
func structToVars(o any) map[string]any {
	v := reflect.ValueOf(o)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	return structFieldsToMap(v)
}

// structFieldsToMap 按 sokel tag 把 struct 展成 {对外名: 值}。
//
// **nil 的指针与 interface 字段不带出**：一是流式下后帧不该用空值覆盖前帧，
// 二是 typed 出参会把所有字段都摊出来，而「这次没有的东西」不该以空值出现在输出里——
// 比如 http 的 file 模式本就没有 body，带一个 body:null 出去，下游引用它只会更困惑。
func structFieldsToMap(v reflect.Value) map[string]any {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	m := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _ := parseSokelTag(sf)
		fv := v.Field(i)
		if (fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface) && fv.IsNil() {
			continue
		}
		m[name] = sokelValue(fv)
	}
	return m
}

// sokelValue 按 sokel 语义转换一个值：嵌套 struct → map（*File 除外，原样保留），slice → []any 递归，其余原样。
func sokelValue(v reflect.Value) any {
	if isFileRef(v.Type()) {
		return v.Interface() // 文件引用：其 json tag 已对齐平台，原样保留
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return sokelValue(v.Elem())
	case reflect.Struct:
		return structFieldsToMap(v)
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = sokelValue(v.Index(i))
		}
		return out
	default:
		return v.Interface()
	}
}

func applyDefaultTag(fv reflect.Value, sf reflect.StructField) {
	dv, ok := sf.Tag.Lookup("default")
	if !ok || !fv.CanSet() {
		return
	}
	switch fv.Kind() {
	case reflect.Bool:
		b, _ := strconv.ParseBool(dv)
		fv.SetBool(b)
	case reflect.String:
		fv.SetString(dv)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := strconv.ParseInt(dv, 10, 64)
		fv.SetInt(n)
	case reflect.Float32, reflect.Float64:
		f, _ := strconv.ParseFloat(dv, 64)
		fv.SetFloat(f)
	}
}

func coerceDefault(dv string, t ParamType) any {
	switch t {
	case TBool:
		b, _ := strconv.ParseBool(dv)
		return b
	case TNumber:
		if f, err := strconv.ParseFloat(dv, 64); err == nil {
			return f
		}
		return dv
	default:
		return dv
	}
}

// toSnake：Go 字段名 → 下划线名（无 sokel tag 时的兜底）。缩略词按一个词切：
// ID→id、URL→url、ChatID→chat_id、HTTPCode→http_code（曾逐大写字母插下划线 → i_d/u_r_l）。
func toSnake(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r >= 'A' && r <= 'Z' {
			// 词边界：前一个是小写/数字（chatI|D 不算）；或处于连续大写串的末尾且后跟小写（HTTPC|ode）。
			prevLower := i > 0 && (rs[i-1] >= 'a' && rs[i-1] <= 'z' || rs[i-1] >= '0' && rs[i-1] <= '9')
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			prevUpper := i > 0 && rs[i-1] >= 'A' && rs[i-1] <= 'Z'
			if prevLower || (prevUpper && nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// elemHasNoStructure：数组元素是否「无结构可言」（裸 map / any / 接口），
// 用于把 []map[string]any 这类判为 opaque，而 []string 不判。
func elemHasNoStructure(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return false
	}
	el := t.Elem()
	if isFileRef(el) { // []*File / []File：元素类型明确（file），有结构
		return false
	}
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.Interface:
		return true
	case reflect.Map:
		return el.Elem().Kind() == reflect.Interface
	}
	return false
}

// intKindName：整数字段的 Go 类型名（int / int64 / uint32…），非整数返回空。
// 保留宽度而不是一律记成 int —— 生成 int64 字段时降成 int 会丢精度。
func intKindName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return t.Kind().String()
	}
	return ""
}
