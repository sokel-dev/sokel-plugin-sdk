// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

// 语言中立的插件声明（sokel.yaml / sokel.json）。
//
// Go 插件用 schema/ 包声明契约——那是 Go 的惯例（编译期、方法名写错即编译失败）。
// 但契约本身与 Go 无关：Python / Node 的插件作者不该为了声明几个字段先学一遍 Go builder，
// 更不该让「装个 Go 工具链」成为写插件的前提之外还要读 Go 代码。
//
// 所以声明有两条等价入口，产出同一份 IR：
//
//	schema/ 包（Go builder）──┐
//	                          ├─▶ IR ─▶ 渲染 Go / TypeScript / Python
//	sokel.yaml（本文件）──────┘
//
// YAML 与 JSON 是**同一份格式**：YAML 先转成 JSON 再按同一组 tag 解码，
// 于是两种写法不可能漂——不存在「YAML 支持而 JSON 不支持」的键。
// 解码开 DisallowUnknownFields：拼错的键当场报错，而不是静默丢掉一个字段
// （声明式格式最典型的静默失效就是「写了但没生效」）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest 一个插件的完整声明。字段顺序即推荐的书写顺序。
type Manifest struct {
	Plugin       PluginDecl      `json:"plugin"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	Credential   *CredentialDecl `json:"credential,omitempty"`
	Events       []EventDecl     `json:"events,omitempty"`
	EventsCommon []string        `json:"eventsCommon,omitempty"`
	Operations   []OperationDecl `json:"operations"`
	Codegen      CodegenList     `json:"codegen,omitempty"`

	// path：manifest 自身的路径（doc 之类的相对路径按它解析）。不来自文件内容。
	path string
}

// PluginDecl 插件身份与说明书。
type PluginDecl struct {
	Name    string `json:"name"`
	Label   string `json:"label,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Version string `json:"version,omitempty"`
	// Doc：使用说明 markdown 的**文件路径**（相对 manifest 所在目录）。
	// 不内联进 manifest：说明书里全是代码块与缩进，塞进 YAML 只会两边都难读。
	Doc    string `json:"doc,omitempty"`
	DocURL string `json:"docUrl,omitempty"`
}

// CredentialDecl 凭证契约 + 凭证是怎么拿到的。
type CredentialDecl struct {
	Auth   *AuthDecl `json:"auth,omitempty"`
	Fields []Field   `json:"fields,omitempty"`
}

// AuthDecl 协作式认证声明。步骤由 kind 定死（见 contract/auth）：
// qr = start+poll，input = start+poll+submit，oauth = 平台代答、插件不写 handler。
type AuthDecl struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// Steps 按 kind 推出步骤。**不让作者填**：多写一步 = 承诺一份永远不会被调用的实现，
// 少写一步 = 面板卡在缺的那一步，而两种错都没人会发现。
func (a AuthDecl) Steps() []string {
	switch a.Kind {
	case "qr":
		return []string{"start", "poll"}
	case "input":
		return []string{"start", "poll", "submit"}
	}
	return nil // oauth：平台代答，插件一步都不实现
}

// EventDecl 一种事件及其 payload 契约。
type EventDecl struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Desc   string  `json:"desc,omitempty"`
	Fields []Field `json:"fields"`
}

// OperationDecl 一个操作的契约。
type OperationDecl struct {
	ID         string  `json:"id"`
	Label      string  `json:"label,omitempty"`
	Desc       string  `json:"desc,omitempty"`
	Stream     bool    `json:"stream,omitempty"`
	Internal   bool    `json:"internal,omitempty"`
	TimeoutSec int     `json:"timeoutSec,omitempty"`
	Inputs     []Field `json:"inputs,omitempty"`
	Outputs    []Field `json:"outputs,omitempty"`
}

// CodegenDecl 生成目标。写在 manifest 里，`sokel-gen generate <目录>` 就不必每次带参数——
// CI 的 check 也因此不需要知道每个插件是什么语言。
type CodegenDecl struct {
	Lang string `json:"lang"`          // ts | python
	Out  string `json:"out,omitempty"` // 生成物路径（相对 manifest 所在目录）
}

// CodegenList：一个或多个生成目标。
//
// 允许多个，是因为「同一份声明、多种语言」正是这套东西存在的理由——
// 参考插件就要在 Python 与 Node 各实现一遍，而两边照着的必须是**同一个文件**，
// 不是两份抄来抄去的副本。
type CodegenList []CodegenDecl

// UnmarshalJSON 单个对象与数组两种写法都认（一个目标时不必写成一元数组）。
func (c *CodegenList) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "[") {
		var list []CodegenDecl
		if err := decodeStrict(b, &list); err != nil {
			return err
		}
		*c = list
		return nil
	}
	var one CodegenDecl
	if err := decodeStrict(b, &one); err != nil {
		return err
	}
	*c = CodegenList{one}
	return nil
}

func decodeStrict(b []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// ManifestNames：按此顺序找 manifest。同目录同时存在多个不算错——取第一个，
// 但那多半是改名没删干净，所以 FindManifest 会把重复的一并报出来。
var ManifestNames = []string{"sokel.yaml", "sokel.yml", "sokel.json"}

// FindManifest 在目录里找插件 manifest。没有则返回空串（不是错误：Go 插件走 schema/ 那条路）。
func FindManifest(dir string) (string, error) {
	var found []string
	for _, n := range ManifestNames {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			found = append(found, p)
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("%s holds more than one manifest (%s) — keep one", dir, strings.Join(found, " / "))
	}
	if len(found) == 0 {
		return "", nil
	}
	return found[0], nil
}

// LoadManifest 读一份 manifest（YAML 或 JSON，按扩展名判断）并校验。
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	m, err := ParseManifest(raw, strings.HasSuffix(path, ".json"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.path = path
	return m, nil
}

// ParseManifest 解析 manifest 字节。asJSON=false 时按 YAML 读。
//
// YAML 先转成 JSON 再解码，而不是给每个结构再写一套 yaml tag：两套 tag 就是两份声明，
// 迟早出现 `valueType` 在 JSON 里认、在 YAML 里被 yaml.v3 小写成 `valuetype` 而不认这种事。
func ParseManifest(raw []byte, asJSON bool) (*Manifest, error) {
	data := raw
	if !asJSON {
		var node any
		if err := yaml.Unmarshal(raw, &node); err != nil {
			return nil, fmt.Errorf("parsing YAML: %w", err)
		}
		b, err := json.Marshal(node)
		if err != nil {
			return nil, fmt.Errorf("converting YAML to JSON: %w", err)
		}
		data = b
	}
	// 键名归一：协议文档里注册载荷是 snake_case（events_common / doc_url），
	// 而 Field 那层是 camelCase（valueType / oneOf）。两种都认，省得作者照着协议
	// 抄一行下来却撞上一个「unknown field」——那是纯粹的记忆负担，不是纪律。
	var node any
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parsing the declaration: %w", err)
	}
	node = applyAliases(node)
	data, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}

	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing the declaration: %w", err)
	}
	m.normalize()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// keyAliases：snake_case 写法 → 结构体认的键名。
var keyAliases = map[string]string{
	"events_common": "eventsCommon",
	"doc_url":       "docUrl",
	"timeout_sec":   "timeoutSec",
	"value_type":    "valueType",
	"item_type":     "itemType",
	"one_of":        "oneOf",
	"go_type":       "goType",
}

func applyAliases(node any) any {
	switch t := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			if alias, ok := keyAliases[k]; ok {
				k = alias
			}
			out[k] = applyAliases(v)
		}
		return out
	case []any:
		for i, v := range t {
			t[i] = applyAliases(v)
		}
		return t
	}
	return node
}

// Dir：manifest 所在目录（doc 路径、生成物路径都相对它）。
func (m *Manifest) Dir() string {
	if m.path == "" {
		return "."
	}
	return filepath.Dir(m.path)
}

// Path：manifest 自身路径（报错定位用）。
func (m *Manifest) Path() string { return m.path }

// —— 归一 ——

// normalize 展开书写糖，让后续所有环节只面对协议里的那几种类型。
//
//	int    → number + goType:int（其他语言据此产 int 而不是 float）
//	files  → array + itemType:file（数量位只有这一种表达，见 type-system §12）
//	ints   → array + itemType:number + goType:int
func (m *Manifest) normalize() {
	m.normalizeSugar()
	m.resolveGoTypeRefs()
}

// normalizeSugar 展开书写糖。
func (m *Manifest) normalizeSugar() {
	for i := range m.Operations {
		normalizeFields(m.Operations[i].Inputs)
		normalizeFields(m.Operations[i].Outputs)
	}
	for i := range m.Events {
		normalizeFields(m.Events[i].Fields)
	}
	if m.Credential != nil {
		normalizeFields(m.Credential.Fields)
	}
}

// resolveGoTypeRefs：`goType: Profile` 给结构起个名字，之后再引用同一个名字时
// **不必把字段抄第二遍**（出参回显入参的结构是最常见的情形）。
//
// 抄第二遍才是真正的风险：两份会漂，而漂了之后平台看到的是两个形状相同、
// 名字相同、内容却不一样的结构，谁也说不清哪份是对的。
func (m *Manifest) resolveGoTypeRefs() {
	defs := map[string][]Field{}
	m.walkFields(func(f *Field) {
		if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) > 0 {
			if _, ok := defs[f.GoType]; !ok {
				defs[f.GoType] = f.Fields
			}
		}
	})
	if len(defs) == 0 {
		return
	}
	m.walkFields(func(f *Field) {
		if f.GoType == "" || isIntGoType(f.GoType) || len(f.Fields) > 0 || f.ValueType != nil || f.Opaque {
			return
		}
		if fields, ok := defs[f.GoType]; ok {
			f.Fields = fields
		}
	})
}

// walkFields 遍历所有声明里的字段（含嵌套、oneOf 分支、valueType）。
func (m *Manifest) walkFields(fn func(*Field)) {
	for i := range m.Operations {
		walkFieldList(m.Operations[i].Inputs, fn)
		walkFieldList(m.Operations[i].Outputs, fn)
	}
	for i := range m.Events {
		walkFieldList(m.Events[i].Fields, fn)
	}
	if m.Credential != nil {
		walkFieldList(m.Credential.Fields, fn)
	}
}

func walkFieldList(fs []Field, fn func(*Field)) {
	for i := range fs {
		fn(&fs[i])
		walkFieldList(fs[i].Fields, fn)
		for j := range fs[i].OneOf {
			walkFieldList(fs[i].OneOf[j].Fields, fn)
		}
		if fs[i].ValueType != nil {
			walkFieldList([]Field{*fs[i].ValueType}, fn)
		}
	}
}

func normalizeFields(fs []Field) {
	for i := range fs {
		f := &fs[i]
		switch f.Type {
		case "int":
			f.Type, f.GoType = "number", "int"
		case "files":
			f.Type, f.ItemType = "array", "file"
		case "ints":
			f.Type, f.ItemType, f.GoType = "array", "number", "int"
		case "strings":
			f.Type, f.ItemType = "array", "string"
		}
		if f.ValueType != nil {
			vt := *f.ValueType
			normalizeOne(&vt)
			f.ValueType = &vt
		}
		normalizeFields(f.Fields)
		for j := range f.OneOf {
			normalizeFields(f.OneOf[j].Fields)
		}
	}
}

func normalizeOne(f *Field) {
	tmp := []Field{*f}
	normalizeFields(tmp)
	*f = tmp[0]
}

// —— 校验 ——

var (
	opIDRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldIDRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// wireTypes：协议 §5 认得的类型。声明里出现别的一律报错——
// 平台侧遇到未知类型只会退化成「当字符串处理」，那是最难查的一类问题。
var wireTypes = map[string]bool{
	"string": true, "text": true, "number": true, "boolean": true,
	"file": true, "json": true, "array": true, "enum": true, "secret": true,
}

// Validate 全量校验。**一次报全部**而不是撞见第一个就退：改声明的人多半一次写好几处，
// 一条一条来回跑既慢又容易漏。
func (m *Manifest) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(m.Plugin.Name) == "" {
		add("plugin.name must not be empty")
	}
	if len(m.Operations) == 0 && len(m.Events) == 0 {
		add("neither operations nor events — this plugin does nothing")
	}
	seen := map[string]bool{}
	for _, op := range m.Operations {
		switch {
		case op.ID == "":
			add("an operation has no id")
		case strings.Contains(op.ID, "."):
			add("operation id %q is in the platform's reserved namespace (it contains a dot) — declare auth flows under credential.auth", op.ID)
		case !opIDRe.MatchString(op.ID):
			add("operation id %q is invalid; it must match ^[a-z][a-z0-9_]*$", op.ID)
		case op.ID == "auth_start" || op.ID == "auth_submit" || op.ID == "auth_poll":
			add("operation id %q is the old auth-flow convention; it is declarative now: credential.auth", op.ID)
		case seen[op.ID]:
			add("duplicate operation id %q", op.ID)
		}
		seen[op.ID] = true
		errs = append(errs, validateFields(fmt.Sprintf("operation %q inputs", op.ID), op.Inputs)...)
		errs = append(errs, validateFields(fmt.Sprintf("operation %q outputs", op.ID), op.Outputs)...)
	}

	eventIDs := map[string]bool{}
	for _, e := range m.Events {
		if e.ID == "" {
			add("an event has no id")
		} else if !opIDRe.MatchString(e.ID) {
			add("event id %q is invalid; it must match ^[a-z][a-z0-9_]*$", e.ID)
		} else if eventIDs[e.ID] {
			add("duplicate event id %q", e.ID)
		}
		eventIDs[e.ID] = true
		errs = append(errs, validateFields(fmt.Sprintf("event %q fields", e.ID), e.Fields)...)
	}
	errs = append(errs, m.validateCommon(eventIDs)...)

	if m.Credential != nil {
		errs = append(errs, validateFields("credential.fields", m.Credential.Fields)...)
		if a := m.Credential.Auth; a != nil {
			switch a.Kind {
			case "qr", "input":
			case "oauth":
				if a.Provider == "" {
					add("credential.auth.kind=oauth requires a provider")
				}
			default:
				add("credential.auth.kind %q is invalid (qr / input / oauth)", a.Kind)
			}
		}
	}
	for _, c := range m.Codegen {
		switch c.Lang {
		case "ts", "python":
		default:
			add("codegen.lang %q is invalid (ts / python)", c.Lang)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("the declaration has %d problems:\n  - %s", len(errs), strings.Join(errs, "\n  - "))
	}
	return nil
}

// validateCommon：公共字段必须在**每个**事件里都有且类型一致。
// 与 Go 侧 contract.ValidateCommonFields 同一套规则——推断交集是不行的，
// 新增一个事件少写某字段会让公共字段悄悄缩水，存量工作流跟着断。
func (m *Manifest) validateCommon(eventIDs map[string]bool) []string {
	if len(m.EventsCommon) == 0 {
		return nil
	}
	var errs []string
	if len(m.Events) == 0 {
		return []string{"eventsCommon is declared but there are no events"}
	}
	reserved := map[string]bool{"_event": true, "event": true, "input": true, "credential_id": true}
	for _, name := range m.EventsCommon {
		if reserved[name] {
			errs = append(errs, fmt.Sprintf("common field %q collides with a platform-reserved key", name))
			continue
		}
		if eventIDs[name] {
			errs = append(errs, fmt.Sprintf("common field %q collides with an event id", name))
			continue
		}
		var ref *Field
		for _, e := range m.Events {
			hit := findField(e.Fields, name)
			if hit == nil {
				errs = append(errs, fmt.Sprintf("common field %q does not exist in the contract of event %q", name, e.ID))
				break
			}
			if ref == nil {
				ref = hit
			} else if ref.Type != hit.Type {
				errs = append(errs, fmt.Sprintf("common field %q has different types across events (%s vs %s)", name, ref.Type, hit.Type))
				break
			}
		}
	}
	return errs
}

func findField(fs []Field, name string) *Field {
	for i := range fs {
		if fs[i].Name == name {
			return &fs[i]
		}
	}
	return nil
}

func validateFields(where string, fs []Field) []string {
	var errs []string
	seen := map[string]bool{}
	for _, f := range fs {
		switch {
		case f.Name == "":
			errs = append(errs, where+": a field has no name")
		case !fieldIDRe.MatchString(f.Name):
			errs = append(errs, fmt.Sprintf("%s: field name %q is invalid (letters, digits and underscore; must not start with a digit)", where, f.Name))
		case seen[f.Name]:
			errs = append(errs, fmt.Sprintf("%s: duplicate field name %q", where, f.Name))
		}
		seen[f.Name] = true
		errs = append(errs, validateField(where+"."+f.Name, f)...)
	}
	return errs
}

func validateField(where string, f Field) []string {
	var errs []string
	if f.Type == "" {
		return append(errs, where+" has no type")
	}
	if !wireTypes[f.Type] {
		errs = append(errs, fmt.Sprintf("%s has an unknown type %q (string/text/number/boolean/file/json/array/enum/secret; shorthands: int/files/ints/strings)", where, f.Type))
	}
	for _, t := range f.Types {
		if !wireTypes[t] {
			errs = append(errs, fmt.Sprintf("%s lists an unknown type %q in `types`", where, t))
		}
	}
	if f.Type == "enum" && len(f.Options) == 0 {
		errs = append(errs, where+" is an enum but has no options")
	}
	if f.Type != "enum" && len(f.Options) > 0 {
		errs = append(errs, where+" is not an enum but has options")
	}
	if f.ValueType != nil && len(f.Fields) > 0 {
		errs = append(errs, where+" sets both fields and valueType — they are mutually exclusive (fixed keys use fields, runtime keys use valueType)")
	}
	// goType 既是「结构的名字」也是「对该结构的引用」。引用了一个谁也没定义过的名字，
	// 生成出来的会是一个空壳类型——那种失败在运行期表现为「字段莫名其妙全丢了」。
	if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) == 0 && f.ValueType == nil && !f.Opaque &&
		(f.Type == "json" || f.Type == "array") {
		errs = append(errs, fmt.Sprintf("%s references the structure %q, but nothing declares its fields", where, f.GoType))
	}
	if f.Opaque {
		// 与 Go 侧 field.Opaque(reason) 同一条纪律：声明无结构必须给理由。
		// 「图省事」与「确实没有结构」在代码里长得一模一样，理由是唯一能把两者分开的东西。
		if f.Type != "json" && f.Type != "array" {
			errs = append(errs, where+": only json / array may be marked opaque")
		}
		if strings.TrimSpace(f.Desc) == "" {
			errs = append(errs, where+" is opaque but has no desc — declaring no structure requires a reason")
		}
		if len(f.Fields) > 0 || f.ValueType != nil {
			errs = append(errs, where+" is opaque and also declares structure — pick one")
		}
	}
	for _, v := range f.OneOf {
		if v.Name == "" {
			errs = append(errs, where+": a oneOf branch has no name")
		}
		if v.Type == "" {
			errs = append(errs, fmt.Sprintf("%s: oneOf branch %q has no type", where, v.Name))
		}
		errs = append(errs, validateFields(where+".oneOf."+v.Name, v.Fields)...)
	}
	errs = append(errs, validateFields(where, f.Fields)...)
	if f.ValueType != nil {
		errs = append(errs, validateField(where+".valueType", *f.ValueType)...)
	}
	return errs
}

// —— 转 IR ——

// Ops 把声明转成生成器的 IR。类型名按**操作 id** 派生（与 Go 那条路同一规则），
// 于是同一份契约无论从哪条入口进来，生成物的命名都一致。
func (m *Manifest) Ops() []OpIO {
	out := make([]OpIO, 0, len(m.Operations))
	for _, op := range m.Operations {
		in, outs := op.Inputs, op.Outputs
		if in == nil {
			in = []Field{}
		}
		if outs == nil {
			outs = []Field{}
		}
		out = append(out, OpIO{
			OpID: op.ID, Label: op.Label, Desc: op.Desc, Stream: op.Stream,
			InType: exportName(op.ID) + "In", OutType: exportName(op.ID) + "Out",
			Inputs: in, Outputs: outs,
		})
	}
	return out
}

// EventIOs 事件声明 → IR。
func (m *Manifest) EventIOs() []EventIO {
	out := make([]EventIO, 0, len(m.Events))
	for _, e := range m.Events {
		fs := e.Fields
		if fs == nil {
			fs = []Field{}
		}
		out = append(out, EventIO{ID: e.ID, Label: e.Label, Desc: e.Desc, Fields: fs, TypeName: exportName(e.ID) + "Event"})
	}
	return out
}

// DocMarkdown 读出使用说明（plugin.doc 指向的文件）。没声明则空串。
func (m *Manifest) DocMarkdown() (string, error) {
	if m.Plugin.Doc == "" {
		return "", nil
	}
	p := m.Plugin.Doc
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.Dir(), p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading the user-facing doc %s: %w", p, err)
	}
	return string(b), nil
}

// UnmarshalJSON 让 enum 的候选项两种写法都认：
//
//	options: [asc, desc]                          # 值本身可读，不必再写一遍显示名
//	options: [{value: xiaoyan, label: 小燕（女声）}] # 值是代码，人看不懂时才给显示名
//
// 与协议 §5 的两种形态一一对应（Go 侧是 field.Opt 的可变参数）。
func (o *Option) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		o.Value = s
		return nil
	}
	var alias struct {
		Value string `json:"value"`
		Label string `json:"label,omitempty"`
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&alias); err != nil {
		return fmt.Errorf("an enum option must be a string or {value,label}: %w", err)
	}
	if alias.Value == "" {
		return fmt.Errorf("an enum option is missing `value`")
	}
	o.Value, o.Label = alias.Value, alias.Label
	return nil
}
