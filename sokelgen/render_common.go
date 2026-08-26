// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

// 各语言渲染器共用的两件事：具名结构的收集，以及契约字面量的渲染。
//
// 收集单独抽出来，是因为「同一个结构在多处引用只定义一次」这件事三种语言都要做，
// 而各写一遍的结果必然是各有各的漏（Go 侧靠 goType 复用，别的语言若不收就会
// 生成一串形状相同但互不相干的匿名类型，作者写实现时得来回转换——正是要杜绝的东西）。

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// namedModel 一个具名结构：契约里 goType 给了名字的 json / array 字段。
type namedModel struct {
	Name   string
	Fields []Field
}

// collectModels 递归收下所有具名结构。同名只保留第一份——契约里同名不同形是声明错误，
// 但那属于 Validate 的职责，渲染阶段再判一次只会给出两处不一致的报错。
func collectModels(fields []Field, into map[string][]Field) {
	for _, f := range fields {
		if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) > 0 {
			if _, ok := into[f.GoType]; !ok {
				into[f.GoType] = f.Fields
			}
		}
		collectModels(f.Fields, into)
		for _, v := range f.OneOf {
			if name := variantModelName(v); name != "" && len(v.Fields) > 0 {
				if _, ok := into[name]; !ok {
					into[name] = v.Fields
				}
			}
			collectModels(v.Fields, into)
		}
		if f.ValueType != nil {
			collectModels([]Field{*f.ValueType}, into)
		}
	}
}

// variantModelName oneOf 分支的类型名。数组分支的 goType 指的是**元素类型**。
func variantModelName(v OneOfVariant) string {
	if v.GoType != "" {
		return v.GoType
	}
	return exportName(v.Name)
}

// sortedModels 稳定顺序输出——生成物必须是确定性的，否则每次生成都产生无意义 diff。
func sortedModels(m map[string][]Field) []namedModel {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]namedModel, 0, len(names))
	for _, n := range names {
		out = append(out, namedModel{Name: n, Fields: m[n]})
	}
	return out
}

// anonModelName 匿名嵌套结构的名字：所属类型 + 字段名。
// 没有 goType 的嵌套结构靠它拿到一个稳定的名字，而不是每处生成一个内联字面量。
func anonModelName(owner, field string) string { return owner + exportName(field) }

// contractJSON 契约字典（= 注册载荷里契约那部分）。渲染成 JSON 是刻意的：
// 三种语言的生成物里都是**同一份 JSON**，golden 因此可以逐字节比。
func contractJSON(m *Manifest, doc string) (map[string]any, error) {
	out := map[string]any{"name": m.Plugin.Name}
	// label / desc / version 不进注册载荷（平台的展示名来自插件目录），但要进生成物：
	// 声明在 sokel.yaml 里的东西必须在某处**看得见**，否则改了它连 check 都不会红。
	putIfSet(out, "label", m.Plugin.Label)
	putIfSet(out, "desc", m.Plugin.Desc)
	putIfSet(out, "version", m.Plugin.Version)
	ops := make([]map[string]any, 0, len(m.Operations))
	for _, op := range m.Operations {
		entry := map[string]any{"id": op.ID, "inputs": nonNil(op.Inputs), "outputs": nonNil(op.Outputs)}
		putIfSet(entry, "label", op.Label)
		putIfSet(entry, "desc", op.Desc)
		if op.Stream {
			entry["stream"] = true
		}
		if op.Internal {
			entry["internal"] = true
		}
		if op.TimeoutSec > 0 {
			entry["timeoutSec"] = op.TimeoutSec
		}
		ops = append(ops, entry)
	}
	// 认证流的三个保留操作是**契约的一部分**：平台面板经 /credentials/{id}/auth/{step}
	// 调它们，契约里没有的话面板不知道该发什么参数。声明了 auth 就自动带上，作者不写。
	if m.Credential != nil && m.Credential.Auth != nil {
		ops = append(ops, authOperations(m.Credential.Auth.Steps())...)
	}
	out["operations"] = ops
	if m.Credential != nil && len(m.Credential.Fields) > 0 {
		out["credential_schema"] = m.Credential.Fields
	}
	if len(m.Events) > 0 {
		evs := make([]map[string]any, 0, len(m.Events))
		for _, e := range m.Events {
			entry := map[string]any{"id": e.ID, "fields": nonNil(e.Fields)}
			putIfSet(entry, "label", e.Label)
			putIfSet(entry, "desc", e.Desc)
			evs = append(evs, entry)
		}
		out["events"] = evs
	}
	if len(m.EventsCommon) > 0 {
		common := make([]Field, 0, len(m.EventsCommon))
		for _, name := range m.EventsCommon {
			// 校验已经确认过：每个公共字段在所有事件里都有且类型一致，取第一个即可
			if f := findField(m.Events[0].Fields, name); f != nil {
				common = append(common, *f)
			}
		}
		out["events_common"] = common
	}
	if m.Credential != nil && m.Credential.Auth != nil {
		a := m.Credential.Auth
		flow := map[string]any{"kind": a.Kind}
		if steps := a.Steps(); len(steps) > 0 {
			flow["steps"] = steps
		}
		out["auth_flow"] = flow
		if a.Kind == "oauth" {
			oauth := map[string]any{"provider": a.Provider}
			if len(a.Scopes) > 0 {
				oauth["scopes"] = a.Scopes
			}
			out["oauth"] = oauth
		}
	}
	if len(m.Capabilities) > 0 {
		out["capabilities"] = m.Capabilities
	}
	putIfSet(out, "doc", doc)
	putIfSet(out, "doc_url", m.Plugin.DocURL)

	// 过一遍 JSON：把 Field 这类带 omitempty 的结构体归一成普通 map，
	// 于是各语言渲染器面对的是同一份数据，不必各自理解 Go 的 struct tag。
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err := json.Unmarshal(b, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// authOperations 认证流保留操作的契约。形状与 Go SDK 的 auth.go 一字不差——
// 平台面板按这份契约构造请求，三种 SDK 给出不同形状的话，面板就只对其中一种有效。
func authOperations(steps []string) []map[string]any {
	specs := map[string]map[string]any{
		"start": {
			"id": "auth.start", "label": "Start authentication", "internal": true,
			"inputs": []Field{},
			"outputs": []Field{
				{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true},
				{Name: "challenge", Label: "Challenge", Type: "json", Required: true,
					Opaque: true, Desc: "The challenge depends on kind: qr carries qr_image, input carries prompt"},
				{Name: "expires_in", Label: "Expires in (s)", Type: "number", GoType: "int"},
			},
		},
		"poll": {
			"id": "auth.poll", "label": "Poll authentication", "internal": true,
			"inputs": []Field{{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true}},
			"outputs": []Field{
				{Name: "status", Label: "Status", Type: "string", Required: true,
					Desc: "pending / scanned / confirmed / expired"},
				{Name: "session", Label: "Session", Type: "json", Opaque: true,
					Desc: "The credential content once confirmed; its shape is the plugin's own. The platform writes it into the credential row and strips it from the response"},
			},
		},
		"submit": {
			"id": "auth.submit", "label": "Submit authentication input", "internal": true,
			"inputs": []Field{
				{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true},
				{Name: "input", Label: "User input", Type: "string", Required: true},
			},
			"outputs": []Field{{Name: "ok", Label: "Submitted", Type: "boolean", Required: true}},
		},
	}
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		if spec, ok := specs[s]; ok {
			out = append(out, spec)
		}
	}
	return out
}

func nonNil(fs []Field) []Field {
	if fs == nil {
		return []Field{} // 空数组而非 null：下游（平台/前端）不必防 null
	}
	return fs
}

func putIfSet(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// —— 字面量渲染 ——

// renderJSONLiteral 渲染成 JSON（TS 直接可用：JSON 字面量就是合法的 TS 对象字面量）。
// 键排序 + 固定缩进，保证生成物确定性。
func renderJSONLiteral(v any, indent int) string {
	pad := strings.Repeat("  ", indent)
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return trimFloat(t)
	case int:
		return strconv.Itoa(t)
	case string:
		return jsonQuote(t)
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for i, item := range t {
			b.WriteString(pad + "  " + renderJSONLiteral(item, indent+1))
			if i < len(t)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "]")
		return b.String()
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for i, k := range keys {
			b.WriteString(pad + "  " + jsonQuote(k) + ": " + renderJSONLiteral(t[k], indent+1))
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "}")
		return b.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// renderPyLiteral 渲染成 Python 字面量（true/false/null 三个词与 JSON 不同）。
func renderPyLiteral(v any, indent int) string {
	pad := strings.Repeat("    ", indent)
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return trimFloat(t)
	case string:
		return jsonQuote(t) // JSON 的转义规则是 Python 字符串字面量的子集
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for _, item := range t {
			b.WriteString(pad + "    " + renderPyLiteral(item, indent+1) + ",\n")
		}
		b.WriteString(pad + "]")
		return b.String()
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range keys {
			b.WriteString(pad + "    " + jsonQuote(k) + ": " + renderPyLiteral(t[k], indent+1) + ",\n")
		}
		b.WriteString(pad + "}")
		return b.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// jsonQuote：用 encoding/json 而不是 strconv.Quote —— 后者会把非 ASCII 转义成 \uXXXX，
// 生成物里满屏 中 没人读得下去（契约的 label 基本都是中文）。
func jsonQuote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// docComment 把一段说明切成注释行。prefix 是各语言的注释前缀。
func docComment(prefix, s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		// 空行不带尾随空格：生成物里一行看不见的空白既碍眼又会让 diff 变脏
		fmt.Fprintf(&b, "%s\n", strings.TrimRight(prefix+line, " \t"))
	}
	return b.String()
}
