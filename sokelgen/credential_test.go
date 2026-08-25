package sokelgen

import (
	"strings"
	"testing"
)

// 凭证生成物要同时给两样：typed 读取用的 Cred，和上报用的**声明原样字段**。
// 只给 Cred（回头反射它）的话，enum 候选值与默认值会在生成的那一刻就丢掉——
// 那正是凭证留在反射路上一直缺的东西。
func TestRenderCredentialKeepsDeclaredFields(t *testing.T) {
	fields := []Field{
		{Name: "api_key", Label: "Key", Type: "secret", Required: true},
		{Name: "region", Label: "区域", Type: "select", Default: "cn",
			Options: []Option{{Value: "cn", Label: "国内"}, {Value: "us", Label: "美国"}}},
		{Name: "note", Label: "备注", Type: "text"},
	}
	src, err := RenderCredential("main", SchemaRef{}, fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type Cred struct",
		"APIKey string `sokel:\"api_key\"",           // 必填 → 不带 optional（首字母缩写按 Go 惯例大写）
		"`sokel:\"note,optional\"",                   // 非必填 → 带 optional
		"secret:\"true\"",                            // secret 要能被 CredentialAs 识别
		"func credentialContract() []contract.Field", // 上报的是声明原样字段
		"Options: []contract.Option{",                // ← 反射产不出这个，正是迁移的理由
		"Default: \"cn\"",
		"plugin.CredentialHost", // 生成物不 import go-sdk（plugin-core 里的声明会成环）
	} {
		if !strings.Contains(src, want) {
			t.Errorf("生成物缺 %q\n---\n%s", want, src)
		}
	}
	if strings.Contains(src, "go-sdk/sokel") {
		t.Errorf("生成物不该 import go-sdk:\n%s", src)
	}
}

// 凭证的类型词汇是**表单控件**，不是操作字段那套 string/number。
// 用错不拦的话，声明了下拉却渲染成文本框，作者多半会以为是前端的 bug。
func TestCredentialTypeVocabularyIsChecked(t *testing.T) {
	err := checkCredTypes([]Field{{Name: "region", Type: "enum"}})
	if err == nil || !strings.Contains(err.Error(), "field.Select") {
		t.Errorf("enum 应被拦下并指路 field.Select, got %v", err)
	}
	for _, ok := range []string{"text", "secret", "select", "url", "secret-file", "header-list"} {
		if err := checkCredTypes([]Field{{Name: "f", Type: ok}}); err != nil {
			t.Errorf("%q 是合法的凭证字段类型: %v", ok, err)
		}
	}
}

// 生成的 RegisterAuth 参数表 = 声明的步骤。这是这条路相对手写 WithAuth 的实际收益：
// **少给一个 handler 是编译错**，而不是启动时才 panic（更早的写法连 panic 都没有，
// 面板会静静卡在缺的那一步）。
func TestRenderAuthSignatureFollowsSteps(t *testing.T) {
	qr, err := RenderAuth("main", AuthMeta{Kind: "qr", Steps: []string{"start", "poll"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"contract.AuthQR", // 有名类型的常量，不是裸字面量
		"[]contract.AuthStep{contract.StepStart, contract.StepPoll}",
		"start func(plugin.Ctx) (*plugin.AuthChallenge, error)",
		"poll func(ctx plugin.Ctx, authID string) (*plugin.AuthState, error)",
		"plugin.AuthHandlers{Start: start, Poll: poll}",
	} {
		if !strings.Contains(qr, want) {
			t.Errorf("qr 生成物缺 %q\n---\n%s", want, qr)
		}
	}
	if strings.Contains(qr, "submit") {
		t.Error("没声明 submit 就不该出现在参数表里")
	}

	// oauth：全程平台代答 → 一个 handler 都不该要
	oauth, err := RenderAuth("main", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(oauth, "func RegisterAuth(h plugin.AuthHost) {") {
		t.Errorf("oauth 不该要 handler:\n%s", oauth)
	}
	if !strings.Contains(oauth, `[]string{"s"}`) { // gofmt 会对齐字段名，只钉值
		t.Errorf("作用域要带进生成物（平台不写死）:\n%s", oauth)
	}
	// 生成物只 import 内核：plugin-core 里也有契约声明，依赖 go-sdk 会成环
	for _, src := range []string{qr, oauth} {
		if strings.Contains(src, "go-sdk/sokel") {
			t.Errorf("生成物不该 import go-sdk:\n%s", src)
		}
	}
}

// 声明本身的自洽：写错的组合在生成时就拦下，不留到运行期。
func TestCheckAuthMeta(t *testing.T) {
	cases := []struct {
		name string
		meta AuthMeta
		bad  string // 期望错误里出现的关键词；空 = 应通过
	}{
		{"合法 qr", AuthMeta{Kind: "qr", Steps: []string{"start", "poll"}}, ""},
		{"合法 oauth", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}}, ""},
		{"未知 kind", AuthMeta{Kind: "sms"}, "不认识"},
		{"未知步骤", AuthMeta{Kind: "qr", Steps: []string{"start", "poll", "confirm"}}, "不认识"},
		// oauth 声明步骤 = 承诺一份永远不会被调用的实现（上一版正是这个空壳）
		{"oauth 不该声明步骤", AuthMeta{Kind: "oauth", Provider: "google", Scopes: []string{"s"}, Steps: []string{"start"}}, "不该声明 Steps"},
		{"oauth 缺作用域", AuthMeta{Kind: "oauth", Provider: "google"}, "Scopes"},
		// 但不是每家都有作用域：Notion 的权限是用户在同意页上勾页面，没有 scope 可传
		{"无作用域的家不必给", AuthMeta{Kind: "oauth", Provider: "notion"}, ""},
		{"oauth 缺 provider", AuthMeta{Kind: "oauth", Scopes: []string{"s"}}, "Provider"},
		// 少一步 = 面板卡在那一步
		{"qr 缺 poll", AuthMeta{Kind: "qr", Steps: []string{"start"}}, "start 与 poll"},
	}
	for _, c := range cases {
		err := checkAuthMeta(c.meta)
		if c.bad == "" {
			if err != nil {
				t.Errorf("%s: 不该报错, got %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.bad) {
			t.Errorf("%s: 期望错误含 %q, got %v", c.name, c.bad, err)
		}
	}
}
