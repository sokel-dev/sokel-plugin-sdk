// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// 能力自报：插件告诉平台「同一份契约里，哪些**可选**能力我其实不支持」。
//
// 为什么需要（做第二家存储插件时顶出来的，见 dev-plugins/kbstore-pgvector/docs/contract-notes.md）：
// 一份契约可以有多个实现，而实现之间的差别未必体现在「有没有这个操作」上——
// 常常是**同一个操作的某个可选入参我处理不了**。存储契约里的 recency（时效加权）就是：
// ES 有 distance_feature 一把梭，pgvector 侧要自己写衰减表达式，第一版没做。
// 契约里没有地方能说出这件事，于是插件只能**静默忽略**那个入参——
// 平台既不能降级也不能提示，用户配了时效加权、界面一切正常、结果里毫无体现。
//
// 与 operations 的分工：操作有没有，看 operations；某个操作**做到什么程度**，看这里。

// 存储引擎（知识库）的可选能力。常量而不是裸字符串：拼错一个字母就是"声明了但没生效"，
// 而那正是这套机制最典型的静默失效——编译器能挡就别留给运行期。
const (
	// CapRecency：keyword_query 支持 recency（时效加权：越接近 pivot 越靠前）。
	CapRecency = "recency"
	// CapTimeRange：检索支持 time_range 过滤。
	CapTimeRange = "time_range"
	// CapKeywordBM25：关键词腿是真正的 BM25（含中文分词），而不是相似度近似。
	// pgvector 版用 trigram 顶替，召回质量弱一档——选型时必须看得见。
	CapKeywordBM25 = "keyword_bm25"
	// CapFieldBoosts：keyword_query 支持按字段加权（title^3 这类）。
	CapFieldBoosts = "field_boosts"

	// CapWebhook：平台代收 webhook（RegisterWebhook 注册过处理器）。
	// 不用作者手动声明——注册即事实，capabilitiesContract 自动并入；
	// 前端凭证行的「Webhook」按钮与插件详情的 Webhook tab 按这一位显隐。
	CapWebhook = "webhook"
)

// SetCapabilities 声明本插件支持/不支持哪些可选能力。
//
// **只列自己知道的**：没出现在这张表里的能力，平台按「未声明」处理（保持旧行为，不做推断）——
// 老插件不会因为多了这套机制就被判成什么都不支持。
func (p *Plugin) SetCapabilities(caps map[string]bool) { p.capabilities = caps }

// capabilitiesContract：注册握手上报用（nil / 空表都上报 null，平台按未声明处理）。
func (p *Plugin) capabilitiesContract() map[string]bool {
	caps := p.capabilities
	// webhook 能力不靠自报靠事实：注册了处理器就是支持。作者忘声明不该让入口按钮消失。
	if p.webhookFn != nil {
		merged := map[string]bool{CapWebhook: true}
		for k, v := range caps {
			merged[k] = v
		}
		caps = merged
	}
	if len(caps) == 0 {
		return nil
	}
	return caps
}
