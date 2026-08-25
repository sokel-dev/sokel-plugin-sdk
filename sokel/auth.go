package sokel

// 协作式凭证认证：有些凭证**没法让人填**——微信会话要扫码、验证码要回填、
// Google 账号要走同意页。这类凭证由面板与插件协作生成：
//
//	面板点「登录/授权」 → start 拿挑战 → （扫码/回填/跳同意页）→ 2s 轮询 poll → confirmed
//
// 插件在 schema 里用 auth.QR() / auth.Input() / auth.OAuth() 声明这条流，
// sokel-gen 生成 RegisterAuth 接住实现；**不要**自己去注册名叫 auth_start 的操作。
//
// 为什么是声明而不是「注册几个特定名字的操作」：
//   - 早先平台靠嗅探操作 id（`operations.some(id === "auth_start")`）来决定要不要显示
//     登录按钮。那三个名字既没被保留、也没有校验——做身份服务的插件只要有个业务操作
//     叫 auth_start，凭证行就会凭空长出一个按钮，面板点下去还会打到业务操作上；
//   - 而且它逼出过空壳代码：Gmail 的授权全程由平台代答，却仍被迫声明一个
//     永远不会被调用的 auth_start，只为让按钮出现。
//
// 现在能力写在声明里，操作 id 退化成传输细节（`auth.start` 等保留 id，
// 业务 id 不允许带点号，见 sokel.go 的 mustBusinessOpID，撞不上）。

import (
	"encoding/json"
	"fmt"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// AuthKind：挑战的形态，决定面板怎么渲染。**定义在 contract**（声明侧与实现侧同一份），
// 这里只是别名——两处各定义一份的话，迟早出现"值相同但类型不同"的转换噪音。
type AuthKind = contract.AuthKind

const (
	AuthQR    = contract.AuthQR    // 二维码（插件出题）
	AuthInput = contract.AuthInput // 用户回填，如短信验证码（插件出题）
	AuthOAuth = contract.AuthOAuth // 第三方同意页（**平台代答**，见 WithOAuth）
)

// 保留操作 id：带点号，业务 id 产生不出来（业务 id 限定 ^[a-z][a-z0-9_]*$）。
const (
	opAuthStart  = "auth.start"
	opAuthPoll   = "auth.poll"
	opAuthSubmit = "auth.submit"
)

// AuthChallenge / AuthState：认证流的运行时形状。**定义在 plugin-core**，这里只是别名——
// 生成的注册代码因此只 import 内核，不必依赖 SDK（plugin-core 里也有契约声明，会成环）。
type (
	AuthChallenge = plugin.AuthChallenge
	AuthState     = plugin.AuthState
)

const (
	// AuthPending 等状态常量：拼错字符串不会报错，只会让面板一直转圈。
	AuthPending   = "pending"
	AuthScanned   = "scanned"
	AuthConfirmed = "confirmed"
	AuthExpired   = "expired"
)

// authFlowDecl：注册握手上报的声明（平台/面板据此决定要不要给凭证行加「登录」按钮）。
type authFlowDecl struct {
	Kind  AuthKind `json:"kind"`
	Steps []string `json:"steps"` // start / poll / submit，平台按它路由 /credentials/{id}/auth/{step}
}

// SetAuthFlow 实现 plugin.AuthHost：接住声明 + 实现，把处理器挂到保留操作 id 上。
//
// 声明侧（schema 的 AuthMeta）与实现侧（handlers）在这里合流，接线只有这一份。
func (p *Plugin) SetAuthFlow(meta contract.AuthMeta, h plugin.AuthHandlers) {
	decl := authFlowDecl{Kind: meta.Kind}
	if h.Start != nil {
		decl.Steps = append(decl.Steps, "start")
		registerReserved(p, Operation{ID: opAuthStart, Label: "发起认证", Internal: true},
			func(ctx Ctx, _ authStartIn) (authStartOut, error) {
				ch, err := h.Start(ctx)
				if err != nil {
					return authStartOut{}, err
				}
				if ch == nil {
					return authStartOut{}, fmt.Errorf("认证流 Start 未返回挑战")
				}
				kind := ch.Kind
				if kind == "" {
					kind = string(meta.Kind)
				}
				authID := ch.AuthID
				if authID == "" {
					authID = newAuthID()
				}
				return authStartOut{
					AuthID: authID,
					Challenge: map[string]any{
						"kind": kind, "qr_image": ch.QRImage, "prompt": ch.Prompt,
					},
					ExpiresIn: ch.ExpiresIn,
				}, nil
			})
	}
	if h.Poll != nil {
		decl.Steps = append(decl.Steps, "poll")
		registerReserved(p, Operation{ID: opAuthPoll, Label: "轮询认证状态", Internal: true},
			func(ctx Ctx, in authPollIn) (authPollOut, error) {
				st, err := h.Poll(ctx, in.AuthID)
				if err != nil {
					return authPollOut{}, err
				}
				if st == nil {
					return authPollOut{Status: AuthPending}, nil
				}
				out := authPollOut{Status: st.Status}
				// 只在 confirmed 时带 session：中途带出去等于让平台反复覆写凭证行
				if st.Status == AuthConfirmed && len(st.Session) > 0 {
					out.Session = json.RawMessage(st.Session)
				}
				return out, nil
			})
	}
	if h.Submit != nil {
		decl.Steps = append(decl.Steps, "submit")
		registerReserved(p, Operation{ID: opAuthSubmit, Label: "提交认证输入", Internal: true},
			func(ctx Ctx, in authSubmitIn) (authSubmitOut, error) {
				if err := h.Submit(ctx, in.AuthID, in.Input); err != nil {
					return authSubmitOut{}, err
				}
				return authSubmitOut{OK: true}, nil
			})
	}
	p.authFlow = &decl
	// OAuth 的 provider/作用域同属这份声明（凭证是怎么拿到的，是一件事不是两件）。
	//
	// 判据只看 provider：**不是每家都有作用域**——Notion 的权限是用户在同意页上勾页面，
	// 请求里压根没有 scope 参数。跟着要求非空的话，声明会被这里静默丢掉，
	// 平台侧收到一个空 oauth，凭证行上那颗「授权」按钮就永远不出现，而日志里一个字都没有。
	if meta.Provider != "" {
		p.oauth = &OAuthSpec{Provider: meta.Provider, Scopes: meta.Scopes}
	}
}

// —— 保留操作的 I/O（平台契约，与 /credentials/{id}/auth/{step} 的形状对齐）——

type authStartIn struct{}

type authStartOut struct {
	AuthID    string         `sokel:"auth_id" label:"认证 id"`
	Challenge map[string]any `sokel:"challenge" label:"认证挑战"`
	ExpiresIn int            `sokel:"expires_in,optional" label:"有效期(秒)"`
}

type authPollIn struct {
	AuthID string `sokel:"auth_id" label:"认证 id"`
}

type authPollOut struct {
	Status string `sokel:"status" label:"状态" desc:"pending / scanned / confirmed / expired"`
	// 平台写入凭证行后从响应里剥离，不会到前端。
	//
	// 声明成 any 而不是 json.RawMessage：nil 的 interface 字段不会被带出（见 structFieldsToMap），
	// 而 nil 的 []byte 会带出一个 session:null——那会让平台在 pending 时也去覆写凭证行。
	Session any `sokel:"session,optional" label:"会话"`
}

type authSubmitIn struct {
	AuthID string `sokel:"auth_id" label:"认证 id"`
	Input  string `sokel:"input" label:"用户输入"`
}

type authSubmitOut struct {
	OK bool `sokel:"ok" label:"已提交"`
}
