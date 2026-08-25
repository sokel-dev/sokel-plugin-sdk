package sokelgen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// 生成 In/Out struct：契约字段 → Go 字段。
// 第一条底线仍是语法合法（拼串生成最容易栽在这），再看类型映射对不对。
func TestRenderTypes(t *testing.T) {
	ops := []OpIO{{
		OpID: "file_digest", InType: "FileDigestIn", OutType: "FileDigestOut",
		Inputs: []Field{
			{Name: "file", Type: "file", Required: true},
			{Name: "files", Type: "array", ItemType: "file"},
			{Name: "algo", Type: "enum", Options: []Option{{Value: "md5"}}},
			{Name: "count", Type: "number"},
			{Name: "on", Type: "boolean"},
			{Name: "hosts", Type: "array", ItemType: "string"},
			{Name: "os", Type: "json", GoType: "OSInfo"},                                  // 复用声明时给的类型
			{Name: "rows", Type: "array", GoType: "Row"},                                  // 元素复用
			{Name: "blob", Type: "json", Opaque: true},                                    // 无结构 → any
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{{Name: "A", Type: "json"}}}, // 联合 → 包装类型
			{Name: "inline", Type: "json", Fields: []Field{{Name: "x", Type: "string"}}},  // 匿名 → 内联
		},
		Outputs: []Field{{Name: "md5", Type: "string"}},
	}}

	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_types.go", src, 0); err != nil {
		t.Fatalf("生成的代码语法不合法: %v\n---\n%s", err, src)
	}

	// 用正则而不是字面片段：gofmt 会**按列对齐**结构体字段，锁死空格数是脆断言
	// （这个坑我踩过两次了）。这里只锁「字段名 + 类型」的对应关系。
	for _, want := range []string{
		`type FileDigestIn struct`,
		`type FileDigestOut struct`,
		`File\s+\*plugin\.File\s+.json:"file"`,
		`Files\s+\[\]\*plugin\.File`,
		`Algo\s+string`, // enum 就是字符串
		`Count\s+float64`,
		`On\s+bool`,
		`Hosts\s+\[\]string`,      // 标量元素
		`OS\s+schema\.OSInfo`,     // 复用声明的类型；os 是缩略词，字段名全大写
		`Rows\s+\[\]schema\.Row`,  // 元素复用
		`Blob\s+map\[string\]any`, // opaque
		`Doc\s+FileDigestInDoc`,   // oneOf → 包装类型（见 union_test.go；any 太容易断言错）
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("缺片段 %q\n---\n%s", want, src)
		}
	}
	// 匿名结构内联展开
	if !strings.Contains(src, "Inline struct {") {
		t.Errorf("匿名 json 应内联展开\n---\n%s", src)
	}
	// snake_case → 导出的 CamelCase，json tag 保留契约里的名字
	if strings.Contains(src, "file_digest struct") {
		t.Errorf("类型名不该是 snake\n---\n%s", src)
	}
}

// 没有用到 schema 包的类型时，不该硬塞一个未使用的 import（Go 会编译失败）。
func TestRenderTypesNoUnusedImport(t *testing.T) {
	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, []OpIO{{
		OpID: "ping", InType: "PingIn", OutType: "PingOut",
		Inputs:  []Field{{Name: "msg", Type: "string"}},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, `"schema"`) {
		t.Errorf("没用到就不该 import schema 包\n---\n%s", src)
	}
	if strings.Contains(src, "sokel.File") || strings.Contains(src, `go-sdk/sokel"`) {
		t.Errorf("没有文件字段就不该 import sokel\n---\n%s", src)
	}
}

// 生成的字段名要符合 Go 惯例：缩略词全大写。
// 不处理的话是 KbId / Ok / Md5，golint 会抱怨，手写代码里也没人这么写。
func TestExportNameInitialisms(t *testing.T) {
	cases := map[string]string{
		"kb_id":     "KbID",
		"ids":       "IDs", // 复数形式同样是缩略词
		"ok":        "OK",
		"md5":       "MD5",
		"doc_id":    "DocID",
		"http_url":  "HTTPURL",
		"num_cpu":   "NumCPU",
		"os":        "OS",
		"page_no":   "PageNo", // 不在表里的照常
		"include_x": "IncludeX",
	}
	for in, want := range cases {
		if got := exportName(in); got != want {
			t.Errorf("exportName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 数组元素是联合：type=array + oneOf。生成 []Wrapper，而不是退化成 []any。
func TestRenderTypesArrayOfUnion(t *testing.T) {
	ops := []OpIO{{
		OpID: "chat", SchemaType: "Chat", InType: "ChatIn", OutType: "ChatOut",
		Inputs: []Field{{
			Name: "parts", Type: "array",
			OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string"}}},
				{Name: "ImagePart", GoType: "ImagePart", Type: "json",
					Fields: []Field{{Name: "url", Type: "string"}}},
			},
		}},
	}}
	src, err := RenderTypes("gen", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	// 字段是「包装类型的切片」
	if !regexp.MustCompile(`Parts\s+\[\]ChatInParts\s+`).MatchString(src) {
		t.Errorf("字段应是 []ChatInParts:\n%s", src)
	}
	// 包装类型只定义一次，且带两个类型化访问器
	if strings.Count(src, "type ChatInParts struct") != 1 {
		t.Errorf("包装类型应恰好定义一次:\n%s", src)
	}
	for _, m := range []string{"AsTextPart", "AsImagePart", "Raw"} {
		if !strings.Contains(src, ") "+m+"(") {
			t.Errorf("缺访问器 %s:\n%s", m, src)
		}
	}
	if strings.Contains(src, "[]any") {
		t.Errorf("不该退化成 []any:\n%s", src)
	}
}

// 生成的字段必须带 **sokel** tag（值=契约名），不能只有 json tag。
// BindInput 认的是 sokel tag；缺了就退回「Go 字段名转 snake_case」，
// 契约名不是 snake_case 时直接绑不上——p_http 的 responseType 落不进 ResponseType，
// file 模式静默走成文本模式，产出一个空文件。
func TestRenderTypesEmitsAcnTag(t *testing.T) {
	ops := []OpIO{{OpID: "request", SchemaType: "Request", InType: "RequestIn", OutType: "RequestOut",
		Inputs: []Field{{Name: "responseType", Type: "string"}, {Name: "bodyType", Type: "string"}}}}
	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"responseType", "bodyType"} {
		if !regexp.MustCompile(`json:"` + name + `" sokel:"` + name + `"`).MatchString(src) {
			t.Errorf("字段 %s 应同时带 json 与 sokel tag:\n%s", name, src)
		}
	}
}
