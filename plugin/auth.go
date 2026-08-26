// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import "github.com/sokel-dev/sokel-plugin-sdk/contract"

// 协作式认证的**运行时形状**。
//
// 放在 plugin-core 而不是 SDK 里，是为了让生成的注册代码只 import 内核——
// 与凭证契约同一个理由：plugin-core 里也有契约声明，反过来依赖 go-sdk 会成环。
// SDK 侧用类型别名把它们暴露成 sokel.AuthChallenge / sokel.AuthState，作者无感。

// AuthChallenge：start 交给面板的「题目」。
type AuthChallenge struct {
	// AuthID：这一次认证尝试的 id；poll/submit 会带着它回来。留空则 SDK 自动生成。
	AuthID string
	// Kind：留空取声明里的 Kind。同一插件的不同凭证走不同形态时才需要按次覆盖。
	Kind string
	// QRImage：kind=qr 的二维码，data-uri（如 "data:image/png;base64,…"）。
	QRImage string
	// Prompt：给人看的一句话（kind=input 时同时作为输入框 placeholder）。
	Prompt string
	// ExpiresIn：有效期(秒)。0 = 不告诉面板。
	ExpiresIn int
}

// AuthState：poll 的回答。
type AuthState struct {
	// Status：pending / scanned（已扫码待确认）/ confirmed / expired。
	Status string
	// Session：confirmed 时的会话凭据，交**平台**写进凭证行——不回前端，浏览器不经手明文。
	//
	// 必须是 JSON **对象**形态。给字符串会被再包一层引号（双重编码），
	// 插件下次读凭证时就解不回来了。
	Session []byte
}

// AuthHandlers：认证流的实现侧。哪几步非空由声明的 Steps 决定（生成的 RegisterAuth 保证对齐）。
type AuthHandlers struct {
	Start  func(Ctx) (*AuthChallenge, error)
	Poll   func(ctx Ctx, authID string) (*AuthState, error)
	Submit func(ctx Ctx, authID, input string) error
}

// AuthHost：能接住协作式认证流的宿主（SDK 的 *sokel.Plugin 实现它）。
// 与 CredentialHost 同理：小接口、单方法，生成物因此不必 import go-sdk。
type AuthHost interface {
	SetAuthFlow(meta contract.AuthMeta, h AuthHandlers)
}
