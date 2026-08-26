package sokelgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// YAML 与 JSON 必须是同一份格式。两条解析路径分别实现的话，迟早出现
// 「YAML 支持而 JSON 不支持」的键，而那种差异没人会主动去查。
func TestParseManifest_YAMLEqualsJSON(t *testing.T) {
	yamlSrc := `
plugin: { name: demo }
operations:
  - id: do_it
    label: 干活
    inputs:
      - { name: who, type: string, required: true }
      - { name: mode, type: enum, options: [fast, { value: full, label: 全量 }], default: fast }
    outputs:
      - { name: ok, type: boolean, required: true }
`
	jsonSrc := `{
  "plugin": {"name": "demo"},
  "operations": [{
    "id": "do_it", "label": "干活",
    "inputs": [
      {"name": "who", "type": "string", "required": true},
      {"name": "mode", "type": "enum", "options": ["fast", {"value": "full", "label": "全量"}], "default": "fast"}
    ],
    "outputs": [{"name": "ok", "type": "boolean", "required": true}]
  }]
}`
	fromYAML, err := ParseManifest([]byte(yamlSrc), false)
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	fromJSON, err := ParseManifest([]byte(jsonSrc), true)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	a, _ := json.Marshal(fromYAML)
	b, _ := json.Marshal(fromJSON)
	if string(a) != string(b) {
		t.Fatalf("两种写法解析结果不同：\nYAML: %s\nJSON: %s", a, b)
	}
	if got := fromYAML.Operations[0].Inputs[1].Options; len(got) != 2 || got[0].Value != "fast" || got[1].Label != "全量" {
		t.Fatalf("enum 两种候选项写法没都认：%+v", got)
	}
}

// 拼错的键必须当场报错。静默丢掉一个字段 = 作者以为声明了、平台侧什么也没有，
// 这正是声明式格式最典型的失效方式。
func TestParseManifest_UnknownKeyIsError(t *testing.T) {
	_, err := ParseManifest([]byte("plugin: { name: demo }\noperations: [{id: a, inputs: [], outputs: []}]\nlable: 拼错了\n"), false)
	if err == nil || !strings.Contains(err.Error(), "lable") {
		t.Fatalf("拼错的顶层键没被拦住：%v", err)
	}
}

func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"操作 id 大写", "plugin: {name: d}\noperations: [{id: DoIt, inputs: [], outputs: []}]", "不合法"},
		{"操作 id 带点号", "plugin: {name: d}\noperations: [{id: auth.start, inputs: [], outputs: []}]", "保留命名空间"},
		{"旧认证约定", "plugin: {name: d}\noperations: [{id: auth_start, inputs: [], outputs: []}]", "credential.auth"},
		{"enum 没候选", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: enum}], outputs: []}]", "没有 options"},
		{"未知类型", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: strng}], outputs: []}]", "不认识"},
		{"opaque 无理由", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, opaque: true}], outputs: []}]", "无结构必须给理由"},
		{"fields 与 valueType 并存", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, fields: [{name: x, type: string}], valueType: {name: v, type: string}}], outputs: []}]", "互斥"},
		{"字段重名", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: x, type: string},{name: x, type: string}], outputs: []}]", "重复"},
		{"goType 引用无定义", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, goType: Ghost}], outputs: []}]", "没有任何地方给出它的 fields"},
		{"公共字段缺一个事件", "plugin: {name: d}\nevents_common: [chat_id]\nevents: [{id: a, fields: [{name: chat_id, type: string}]}, {id: b, fields: [{name: other, type: string}]}]\noperations: []", "契约中不存在"},
		{"公共字段类型不一致", "plugin: {name: d}\nevents_common: [chat_id]\nevents: [{id: a, fields: [{name: chat_id, type: string}]}, {id: b, fields: [{name: chat_id, type: number}]}]\noperations: []", "类型不一致"},
		{"公共字段撞保留字", "plugin: {name: d}\nevents_common: [_event]\nevents: [{id: a, fields: [{name: _event, type: string}]}]\noperations: []", "保留字"},
		{"auth kind 不认识", "plugin: {name: d}\ncredential: {auth: {kind: sms}}\noperations: [{id: a, inputs: [], outputs: []}]", "不合法"},
		{"oauth 缺 provider", "plugin: {name: d}\ncredential: {auth: {kind: oauth}}\noperations: [{id: a, inputs: [], outputs: []}]", "必须给 provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.src), false)
			if err == nil {
				t.Fatalf("这份声明本该被拒：%s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("报错没说清问题（要含 %q）：%v", tc.want, err)
			}
		})
	}
}

// 书写糖只是写法，落到契约里必须是协议认得的那几种类型。
func TestManifest_Sugar(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
operations:
  - id: a
    inputs:
      - { name: n, type: int }
      - { name: fs, type: files }
      - { name: ss, type: strings }
    outputs: []
`), false)
	if err != nil {
		t.Fatal(err)
	}
	in := m.Operations[0].Inputs
	if in[0].Type != "number" || in[0].GoType != "int" {
		t.Errorf("int 糖没展开：%+v", in[0])
	}
	if in[1].Type != "array" || in[1].ItemType != "file" {
		t.Errorf("files 糖没展开：%+v", in[1])
	}
	if in[2].Type != "array" || in[2].ItemType != "string" {
		t.Errorf("strings 糖没展开：%+v", in[2])
	}
}

// 声明一次结构、之后按名字引用——出参回显入参结构是最常见的情形，
// 抄第二遍的那份迟早会漂。
func TestManifest_GoTypeReference(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
operations:
  - id: a
    inputs:
      - { name: p, type: json, goType: Profile, fields: [{name: nick, type: string}] }
    outputs:
      - { name: p, type: json, goType: Profile }
`), false)
	if err != nil {
		t.Fatal(err)
	}
	out := m.Operations[0].Outputs[0]
	if len(out.Fields) != 1 || out.Fields[0].Name != "nick" {
		t.Fatalf("goType 引用没解析出结构：%+v", out)
	}
}

// 声明了认证流，契约里就必须有那几个保留操作——平台面板按契约构造请求，
// 契约里没有的话面板不知道该发什么参数。
func TestContractJSON_AuthOperations(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
credential: { auth: { kind: qr } }
operations: [{ id: a, inputs: [], outputs: [] }]
`), false)
	if err != nil {
		t.Fatal(err)
	}
	cj, err := contractJSON(m, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, op := range cj["operations"].([]any) {
		ids[op.(map[string]any)["id"].(string)] = true
	}
	if !ids["auth.start"] || !ids["auth.poll"] {
		t.Fatalf("qr 认证流的保留操作没进契约：%v", ids)
	}
	if ids["auth.submit"] {
		t.Fatal("qr 不该有 submit —— 步骤由 kind 定死")
	}
	flow := cj["auth_flow"].(map[string]any)
	if flow["kind"] != "qr" {
		t.Fatalf("auth_flow 没上报：%v", flow)
	}
}

// export yaml → 再读回来，必须是同一份契约。往返丢东西的话，
// 「拿 Go 插件的声明作别的语言的起点」这条路就是断的。
func TestManifestYAML_RoundTrip(t *testing.T) {
	src := `
plugin: { name: demo, label: 示例 }
credential:
  auth: { kind: input }
  fields: [{ name: api_key, type: secret, required: true }]
events_common: [chat_id]
events:
  - id: msg
    fields:
      - { name: chat_id, type: string, required: true }
      - { name: body, type: json, opaque: true, desc: 上游原样透传 }
operations:
  - id: a
    stream: true
    timeoutSec: 30
    inputs:
      - { name: doc, type: json, oneOf: [{name: A, type: json, fields: [{name: t, type: string}]}] }
      - { name: kv, type: json, valueType: { name: v, type: number } }
    outputs: [{ name: ok, type: boolean, required: true }]
`
	m, err := ParseManifest([]byte(src), false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderManifestYAML(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseManifest([]byte(out), false)
	if err != nil {
		t.Fatalf("导出的 YAML 读不回来（往返断了）：%v\n%s", err, out)
	}
	a, _ := ExportManifestJSON(m, "")
	b, _ := ExportManifestJSON(back, "")
	if string(a) != string(b) {
		t.Fatalf("往返后契约变了：\n%s\n---\n%s", a, b)
	}
}

// 参考插件的契约就是 golden：三种语言的生成物内嵌的都是它。
func TestKitchenSink_MatchesGolden(t *testing.T) {
	dir := filepath.Join("..", "examples", "kitchen-sink")
	m, err := LoadManifest(filepath.Join(dir, "sokel.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExportManifestJSON(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "contract.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("参考插件的契约与 golden 不一致 —— 改了 sokel.yaml 就要更新 golden：\nsokel-gen export json ./examples/kitchen-sink > examples/kitchen-sink/contract.golden.json")
	}
}
