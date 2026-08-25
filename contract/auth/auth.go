// Package auth 构造凭证的获取方式声明。
//
// 为什么是构造函数而不是让人填结构体：**步骤由形态决定**——扫码一定是 start+poll，
// OAuth 一定一步都没有（平台代答）。让人再写一遍 Steps 只是把同一件事抄第二遍，
// 而抄错的那份没人会发现：多写一步 = 承诺一份永远不会被调用的实现，
// 少写一步 = 面板卡在缺的那一步。
//
// 与 contract/field 同一套路子：能由构造函数定死的，就不留给调用方填。
package auth

import "github.com/sokel-dev/sokel-plugin-sdk/contract"

// QR 扫码登录：插件出题（生成二维码）、平台转发、面板轮询到 confirmed。
func QR() contract.AuthMeta {
	return contract.AuthMeta{
		Kind:  contract.AuthQR,
		Steps: []contract.AuthStep{contract.StepStart, contract.StepPoll},
	}
}

// Input 用户回填（如短信验证码）：比扫码多一步 submit——那一步正是这种形态的全部意义。
func Input() contract.AuthMeta {
	return contract.AuthMeta{
		Kind:  contract.AuthInput,
		Steps: []contract.AuthStep{contract.StepStart, contract.StepPoll, contract.StepSubmit},
	}
}

// OAuth 第三方同意页。**没有步骤**：client_secret 在平台手里，插件既构造不出同意页地址，
// 也不该经手 refresh_token，所以 start/poll 全程由平台代答。
//
// 作用域摆在参数上：OAuth 的最小权限就体现在这儿，对 Google 这类家留空等于要一个必然被拒的同意页。
// 但**不是每家都有作用域**——Notion 的权限是用户在同意页上勾哪些页面，没有 scope 参数可传，
// 所以这里不强制非空，是否必填由平台侧那家的 provider 说了算。
func OAuth(provider string, scopes ...string) contract.AuthMeta {
	return contract.AuthMeta{Kind: contract.AuthOAuth, Provider: provider, Scopes: scopes}
}
