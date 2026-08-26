// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/auth"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// 认证流是**声明**出来的，不是靠几个特定的操作名认出来的。
// 这两条钉住那个分界：声明后上报 auth_flow + 保留 id 的内部操作；
// 业务操作则永远碰不到保留命名空间。

func TestAuthFlowDeclaresStepsAndReservedOps(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.Input(), plugin.AuthHandlers{
		Start:  func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{QRImage: "data:image/png;base64,x"}, nil },
		Poll:   func(Ctx, string) (*AuthState, error) { return &AuthState{Status: AuthPending}, nil },
		Submit: func(Ctx, string, string) error { return nil },
	})
	if p.authFlow == nil || p.authFlow.Kind != AuthInput {
		t.Fatalf("应声明 input 认证流: %+v", p.authFlow)
	}
	if got := strings.Join(p.authFlow.Steps, ","); got != "start,poll,submit" {
		t.Errorf("steps 应如实反映给了哪几个处理器, got %q", got)
	}
	// 操作 id 是传输细节，落在保留命名空间里（业务 id 不许带点号，撞不上）
	for _, want := range []string{"auth.start", "auth.poll", "auth.submit"} {
		if p.find(want) == nil {
			t.Errorf("保留操作 %q 未注册", want)
		}
	}
	// 且都是 internal：不能出现在画布上
	for _, e := range p.ops {
		if !e.op.Internal {
			t.Errorf("认证流操作 %q 必须 internal", e.op.ID)
		}
	}
}

// OAuth 全程由平台代答，插件**不该**为了让按钮出现而写一个永不被调用的处理器。
// （auth.OAuth() 因此没有步骤，生成的 RegisterAuth 也不收 handler。）
func TestOAuthFlowNeedsNoHandlers(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.OAuth("google", "s"), plugin.AuthHandlers{})
	p.SetDoc("# 用法\n凭证去哪申请…", "https://docs.example.com/p")
	p.SetCapabilities(map[string]bool{CapRecency: false, CapTimeRange: true})
	if p.authFlow == nil || p.authFlow.Kind != AuthOAuth {
		t.Fatalf("oauth 应产出 oauth 认证流: %+v", p.authFlow)
	}
	if len(p.authFlow.Steps) != 0 {
		t.Errorf("oauth 由平台代答，不该声明步骤: %v", p.authFlow.Steps)
	}
	if len(p.ops) != 0 {
		t.Errorf("oauth 不该注册任何操作，got %d 个", len(p.ops))
	}
	if p.oauth == nil || p.oauth.Provider != "google" || len(p.oauth.Scopes) != 1 {
		t.Fatalf("oauth 声明要带上 provider/作用域: %+v", p.oauth)
	}

	// 没有作用域的家（Notion 的权限是用户在同意页上勾页面，请求里没有 scope 参数）
	// 同样要报上去。丢掉的话平台收到一个空 oauth，凭证行上那颗「授权」按钮永远不出现，
	// 而日志里一个字都没有。
	q := New(Config{Name: "t"})
	q.SetAuthFlow(auth.OAuth("notion"), plugin.AuthHandlers{})
	if q.oauth == nil || q.oauth.Provider != "notion" {
		t.Fatalf("无作用域的家也要报 oauth 声明: %+v", q.oauth)
	}
}

// start 的挑战形状是**平台契约**：面板按 kind 渲染、按 auth_id 轮询。
// 插件不给 auth_id 时 SDK 得补一个——不然 poll 拿不到句柄，面板只能一直转圈。
func TestAuthStartFillsDefaults(t *testing.T) {
	p := New(Config{Name: "t"})
	p.SetAuthFlow(auth.QR(), plugin.AuthHandlers{
		Start: func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{QRImage: "qr", ExpiresIn: 60}, nil },
		Poll:  func(Ctx, string) (*AuthState, error) { return nil, nil },
	})
	out, err := p.invokeBuffered(natsCtx{}, "auth.start", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := out["auth_id"].(string); s == "" {
		t.Error("插件没给 auth_id 时 SDK 必须补一个")
	}
	ch, _ := out["challenge"].(map[string]any)
	if ch["kind"] != "qr" || ch["qr_image"] != "qr" {
		t.Errorf("挑战应带上 kind 与二维码: %v", ch)
	}
}

// poll 返回 nil 视为 pending（插件常见写法：还没到终态就 return nil, nil），
// 且 session 只在 confirmed 时带出——中途带出去等于让平台反复覆写凭证行。
func TestAuthPollNilIsPendingAndSessionOnlyOnConfirmed(t *testing.T) {
	cases := []struct {
		name        string
		state       *AuthState
		wantStatus  string
		wantSession bool
	}{
		{"nil 视为 pending", nil, AuthPending, false},
		{"未确认不带 session", &AuthState{Status: AuthScanned, Session: json.RawMessage(`{"a":1}`)}, AuthScanned, false},
		{"确认才带 session", &AuthState{Status: AuthConfirmed, Session: json.RawMessage(`{"a":1}`)}, AuthConfirmed, true},
	}
	for _, c := range cases {
		p := New(Config{Name: "t"})
		st := c.state
		p.SetAuthFlow(auth.QR(), plugin.AuthHandlers{
			Start: func(Ctx) (*AuthChallenge, error) { return &AuthChallenge{}, nil },
			Poll:  func(Ctx, string) (*AuthState, error) { return st, nil },
		})
		out, err := p.invokeBuffered(natsCtx{}, "auth.poll", json.RawMessage(`{"auth_id":"a1"}`))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out["status"] != c.wantStatus {
			t.Errorf("%s: status = %v, want %q", c.name, out["status"], c.wantStatus)
		}
		if _, has := out["session"]; has != c.wantSession {
			t.Errorf("%s: session 出现与否 = %v, want %v", c.name, has, c.wantSession)
		}
	}
}

// 业务操作 id 的尺子：保留命名空间（带点号）与旧的三个约定名都当场拦下。
// 静默放行的代价是——那三个名字曾经能让凭证行凭空长出「登录」按钮。
func TestBusinessOpIDRejectsReservedNames(t *testing.T) {
	bad := []string{"auth.start", "auth_start", "auth_poll", "auth_submit", "SendText", "send-text", "", "2fa"}
	for _, id := range bad {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("操作 id %q 应被拒", id)
				}
			}()
			mustBusinessOpID(id)
		}()
	}
	for _, id := range []string{"send_text", "gmail_list", "a", "get2"} {
		mustBusinessOpID(id) // 合法：不该 panic
	}
}

// 声明了就必须**上报**——这是自报机制最典型的静默失效：插件侧一切正常、
// 平台侧什么也没发生，作者只能对着一个没反应的界面排查。
// （auth_flow 上线当天就栽在这：声明写好了，握手载荷漏了一行。）
func TestRegisterBodyCarriesDeclarations(t *testing.T) {
	p := New(Config{Name: "t", Token: "skp_x"})
	p.SetCredentialContract([]Field{{Name: "tok", Type: contract.TString}})
	p.SetAuthFlow(auth.OAuth("google", "s"), plugin.AuthHandlers{})
	p.SetDoc("# 用法\n凭证去哪申请…", "https://docs.example.com/p")
	p.SetCapabilities(map[string]bool{CapRecency: false, CapTimeRange: true})
	Register(p, Operation{ID: "send_text"}, func(Ctx, struct{}, *Emitter[struct{}]) error { return nil })

	var body map[string]any
	raw, _ := json.Marshal(p.registerBody("inst", "host", p.contract()))
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"operations", "credential_schema", "oauth", "auth_flow", "token", "instance_id", "doc", "doc_url", "capabilities"} {
		if body[k] == nil {
			t.Errorf("握手载荷缺 %q —— 声明了却没上报，平台侧等于没声明", k)
		}
	}
	flow, _ := body["auth_flow"].(map[string]any)
	if flow["kind"] != "oauth" {
		t.Errorf("auth_flow 应带 kind: %v", body["auth_flow"])
	}
	// 能力自报：**false 也必须上报**——「声明了不支持」与「没声明」是两回事,
	// 前者平台该提示用户,后者按旧行为放行。少一个 false 就等于把提示悄悄关掉。
	caps, _ := body["capabilities"].(map[string]any)
	if caps[CapRecency] != false || caps[CapTimeRange] != true {
		t.Errorf("能力自报没如实上报: %v", body["capabilities"])
	}
}

// 没声明过能力的插件：上报 null,平台按「未声明」处理(保持旧行为)。
// 反过来做——把没列出的都当不支持——会让所有老插件在升级 SDK 当天集体"失去能力"。
func TestCapabilitiesAbsentWhenUndeclared(t *testing.T) {
	p := New(Config{Name: "t", Token: "skp_x"})
	var body map[string]any
	raw, _ := json.Marshal(p.registerBody("inst", "host", p.contract()))
	_ = json.Unmarshal(raw, &body)
	if body["capabilities"] != nil {
		t.Errorf("未声明时应为 null,实际 %v", body["capabilities"])
	}
}
