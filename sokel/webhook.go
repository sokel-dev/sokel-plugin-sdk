// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// 平台代插件收 webhook：外部系统 → 平台 /hooks/{token} → __webhook__ 帧到这里。
//
// 为什么是特殊 operation 名而不是新帧结构：完全复用现有的调用帧（凭证下发/追踪/
// 超时/回复通道全都白拿），SDK 只在分发前拦截这个名字。老 SDK 收到会回
// unknown operation，平台把它翻译成「插件未注册 webhook 处理器」——升级路径干净。
//
// handler 的职责：用凭证里的 secret 验上游签名（各家算法不同，平台不懂上游，
// 插件懂）→ 解析 body → ctx.Trigger 推 typed 事件（走既有声明校验与平台去重）→
// 返回响应（GitLab 要 2xx、飞书 URL 校验要回 challenge，由 handler 决定）。

import (
	"encoding/base64"
	"encoding/json"
	"log"

	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// WebhookRequest 一次入站 webhook（平台已过滤掉 Authorization/Cookie 等平台侧头）。
type WebhookRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"` // /hooks/{token} 之后的余段（通常空）
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
	BodyB64 string            `json:"body_b64"`
}

// Header 大小写不敏感取头（HTTP 语义；上游发 X-Gitlab-Token 或 x-gitlab-token 都认）。
func (r *WebhookRequest) Header(name string) string {
	for k, v := range r.Headers {
		if equalFold(k, name) {
			return v
		}
	}
	return ""
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// WebhookResponse 回给上游的应答。
type WebhookResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"-"`
}

// OK 便捷应答。
func OK() WebhookResponse { return WebhookResponse{Status: 200} }

// Text 便捷应答（飞书 challenge 这类要回 body 的场景）。
func Text(status int, body string) WebhookResponse {
	return WebhookResponse{Status: status, Body: []byte(body)}
}

// WebhookCtx webhook 处理上下文 = 完整的事件源上下文（plugin.SourceCtx）：
// 凭证/触发/上传文件/回写凭证全套——生成的 TriggerXxx 直接可用。
type WebhookCtx = plugin.SourceCtx

// RegisterWebhook 注册 webhook 处理器（一个插件一个：按 Header/path 自行分流上游事件类型）。
func RegisterWebhook(p *Plugin, fn func(WebhookCtx, *WebhookRequest) WebhookResponse) {
	p.webhookFn = fn
}

// handleWebhookFrame 处理一帧 __webhook__（transport 分发前拦截调用）。
// sctx 由 transport 构造（SourceCtx 全量能力：Trigger/Upload/UpdateCredential）。
// 应答带 events 计数（本次触发了几条事件）——平台的 webhook 日志面板靠它回答
// 「请求到了但为什么没起工作流」这一问。
func (p *Plugin) handleWebhookFrame(sctx SourceCtx, input json.RawMessage) []byte {
	fail := func(msg string) []byte {
		b, _ := json.Marshal(map[string]any{"status": 0, "error": msg})
		return b
	}
	if p.webhookFn == nil {
		return fail("插件未注册 webhook 处理器")
	}
	var req WebhookRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return fail("webhook 帧解不开: " + err.Error())
	}
	req.Body, _ = base64.StdEncoding.DecodeString(req.BodyB64)
	counted := &countingSourceCtx{SourceCtx: sctx}
	resp := p.webhookFn(counted, &req)
	if resp.Status == 0 {
		resp.Status = 200
	}
	out, _ := json.Marshal(map[string]any{
		"status": resp.Status, "headers": resp.Headers,
		"body_b64": base64.StdEncoding.EncodeToString(resp.Body),
		"events":   counted.n,
	})
	log.Printf("[sokel] ✓ webhook 处理完成（status=%d events=%d）", resp.Status, counted.n)
	return out
}

// countingSourceCtx 数 Trigger 成功次数（webhook 日志的 events 字段）。
type countingSourceCtx struct {
	SourceCtx
	n int
}

func (c *countingSourceCtx) Trigger(event, eventID string, payload any) error {
	err := c.SourceCtx.Trigger(event, eventID, payload)
	if err == nil {
		c.n++
	}
	return err
}
