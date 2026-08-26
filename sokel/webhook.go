// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

// Webhooks relayed by the platform: upstream system -> platform /hooks/{token} -> a __webhook__
// frame arrives here.
//
// Why a special operation name rather than a new frame type: it reuses the existing call frame
// wholesale — credential injection, tracing, timeouts and the reply subject all come for free — and
// the SDK only intercepts the name before dispatch. An older SDK answers "unknown operation", which
// the platform translates into "the plugin registered no webhook handler", so the upgrade path stays
// clean.
//
// What the handler is responsible for: verifying the upstream signature with the secret in the
// credential (every vendor signs differently; the platform does not know the upstream, the plugin
// does), parsing the body, pushing typed events with ctx.Trigger (which reuses the declared-event
// check and the platform's deduplication), and deciding the response (GitLab wants a 2xx, Feishu's
// URL verification wants the challenge echoed back).

import (
	"encoding/base64"
	"encoding/json"
	"log"

	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// WebhookRequest is one inbound webhook (the platform has stripped Authorization, Cookie and other
// platform-side headers).
type WebhookRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"` // whatever follows /hooks/{token}, usually empty
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
	BodyB64 string            `json:"body_b64"`
}

// Header looks a header up case-insensitively (HTTP semantics: X-Gitlab-Token and x-gitlab-token
// both hit).
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

// WebhookResponse is the reply sent back upstream.
type WebhookResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"-"`
}

// OK is the shorthand success reply.
func OK() WebhookResponse { return WebhookResponse{Status: 200} }

// Text is the shorthand for replies that must carry a body (Feishu's challenge, say).
func Text(status int, body string) WebhookResponse {
	return WebhookResponse{Status: status, Body: []byte(body)}
}

// WebhookCtx is the webhook handler's context: the full event-source context (plugin.SourceCtx) with
// credentials, triggering, file upload and credential write-back, so a generated TriggerXxx works
// directly.
type WebhookCtx = plugin.SourceCtx

// RegisterWebhook registers the webhook handler (one per plugin: route upstream event types by
// header or path yourself).
func RegisterWebhook(p *Plugin, fn func(WebhookCtx, *WebhookRequest) WebhookResponse) {
	p.webhookFn = fn
}

// handleWebhookFrame handles one __webhook__ frame; the transport intercepts before dispatch and
// calls it. sctx comes from the transport with the full SourceCtx (Trigger, Upload, UpdateCredential).
// The reply carries how many events this call triggered: that is how the platform's webhook log panel
// answers "the request arrived, so why did no workflow start?".
func (p *Plugin) handleWebhookFrame(sctx SourceCtx, input json.RawMessage) []byte {
	fail := func(msg string) []byte {
		b, _ := json.Marshal(map[string]any{"status": 0, "error": msg})
		return b
	}
	if p.webhookFn == nil {
		return fail("the plugin registered no webhook handler")
	}
	var req WebhookRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return fail("could not parse the webhook frame: " + err.Error())
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
	log.Printf("[sokel] ✓ webhook handled (status=%d events=%d)", resp.Status, counted.n)
	return out
}

// countingSourceCtx counts successful Triggers (the events field of the webhook log).
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
