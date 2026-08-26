// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

type msgEvent struct {
	ChatID int64  `sokel:"chat_id" label:"对话 ID"`
	Text   string `sokel:"text" label:"文本"`
	Raw    any    `sokel:"raw" label:"原始事件"`
}

// DeclareEvent[T]：事件契约由 payload struct 反射派生（与操作 I/O 同一套 deriveFields）。
func TestDeclareEventDerivesFields(t *testing.T) {
	p := New(Config{Token: "skp_x", Name: "t"})
	DeclareEvent[msgEvent](p, Event{ID: "message", Label: "收到消息"})

	evs := p.eventContract()
	if len(evs) != 1 {
		t.Fatalf("应有 1 个事件契约，got %d", len(evs))
	}
	e := evs[0]
	if e.ID != "message" || e.Label != "收到消息" {
		t.Errorf("事件 id/label 不对: %+v", e)
	}
	names := map[string]string{}
	for _, f := range e.Fields {
		names[f.Name] = string(f.Type)
	}
	if names["chat_id"] != "number" || names["text"] != "string" || names["raw"] != "json" {
		t.Errorf("payload 字段契约反射不对: %+v", e.Fields)
	}
}

// 显式给 Fields 时不反射覆盖（与 Operation 的 Inputs/Outputs 一致）。
func TestDeclareEventExplicitFields(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "e", Fields: []Field{{Name: "only", Type: TString}}})
	evs := p.eventContract()
	if len(evs[0].Fields) != 1 || evs[0].Fields[0].Name != "only" {
		t.Errorf("显式 Fields 应原样保留: %+v", evs[0].Fields)
	}
}

// Trigger：产出线协议 §7 的推送消息 {token, event, event_id, payload}，publish 到 sokel.trigger。
func TestSourceTriggerWireShape(t *testing.T) {
	var gotSubject string
	var gotData []byte
	sc := SourceCtx{
		token:   "skp_abc",
		valid:   map[string]bool{"message": true},
		publish: func(subject string, data []byte) error { gotSubject = subject; gotData = data; return nil },
	}
	if err := sc.Trigger("message", "12345", msgEvent{ChatID: 7, Text: "hi"}); err != nil {
		t.Fatalf("Trigger 出错: %v", err)
	}
	if gotSubject != "sokel.trigger" {
		t.Errorf("subject 应为 sokel.trigger, got %q", gotSubject)
	}
	var m struct {
		Token   string          `json:"token"`
		Event   string          `json:"event"`
		EventID string          `json:"event_id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gotData, &m); err != nil {
		t.Fatalf("消息非法 JSON: %v", err)
	}
	if m.Token != "skp_abc" || m.Event != "message" || m.EventID != "12345" {
		t.Errorf("消息头不对: %+v", m)
	}
	var pl map[string]any
	_ = json.Unmarshal(m.Payload, &pl)
	if pl["chat_id"] != float64(7) || pl["text"] != "hi" {
		t.Errorf("payload 不对: %v", pl)
	}
}

// SourceCtx.Credential：暴露本源实例绑定的凭证（事件源用，如 bot_token；v1.3 起每实例绑定一个）。
func TestSourceCtxCredential(t *testing.T) {
	sc := SourceCtx{cred: map[string]string{"bot_token": "123:ABC"}, credID: "cred_a"}
	if sc.Credential()["bot_token"] != "123:ABC" {
		t.Errorf("Credential 应返回绑定凭证: %v", sc.Credential())
	}
	// 未绑定 → nil map，取键得空串（不 panic）。
	empty := SourceCtx{}
	if empty.Credential()["bot_token"] != "" {
		t.Error("无凭证时取键应为空串")
	}
}

// Trigger 未声明的事件 → 报错（挡拼写错误，好 DX），不静默 publish。
func TestSourceTriggerRejectsUndeclared(t *testing.T) {
	called := false
	sc := SourceCtx{
		token:   "skp_x",
		valid:   map[string]bool{"message": true},
		publish: func(string, []byte) error { called = true; return nil },
	}
	if err := sc.Trigger("callback_query", "1", nil); err == nil {
		t.Error("未声明事件应报错")
	}
	if called {
		t.Error("未声明事件不应 publish")
	}
}

type cbEvent struct {
	ChatID int64  `sokel:"chat_id" label:"对话 ID"`
	Data   string `sokel:"callback_data"`
}

type noChatEvent struct {
	Data string `sokel:"data"`
}

// 公共字段声明：所有事件 payload 共有的字段（如 chat_id）→ 上报 events_common 契约，
// 平台触发时平铺到输入顶层（{{节点.chat_id}}），各分支共享同一变量。
func TestDeclareEventsCommon(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "message"})
	DeclareEvent[cbEvent](p, Event{ID: "callback_query"})
	if err := DeclareEventsCommon(p, "chat_id"); err != nil {
		t.Fatalf("合法公共字段声明不应报错: %v", err)
	}
	fs := p.eventsCommonContract()
	if len(fs) != 1 || fs[0].Name != "chat_id" || fs[0].Type != TNumber {
		t.Errorf("公共字段契约不对: %+v", fs)
	}
}

// 校验：某事件缺该字段 / 类型不一致 / 撞保留字或事件 id → 报错（fail fast，不静默缩水）。
func TestDeclareEventsCommonValidation(t *testing.T) {
	p := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p, Event{ID: "message"})
	DeclareEvent[noChatEvent](p, Event{ID: "bare"})
	if err := DeclareEventsCommon(p, "chat_id"); err == nil {
		t.Error("bare 事件缺 chat_id，应报错")
	}

	p2 := New(Config{Token: "skp_x"})
	DeclareEvent[msgEvent](p2, Event{ID: "message"})
	DeclareEvent[cbEvent](p2, Event{ID: "callback_query"})
	if err := DeclareEventsCommon(p2, "_event"); err == nil {
		t.Error("保留字 _event 应报错")
	}
	if err := DeclareEventsCommon(p2, "message"); err == nil {
		t.Error("撞事件 id 应报错")
	}
	if err := DeclareEventsCommon(p2, "nope"); err == nil {
		t.Error("不存在的字段应报错")
	}

	p3 := New(Config{Token: "skp_x"})
	if err := DeclareEventsCommon(p3, "chat_id"); err == nil {
		t.Error("未声明任何事件时应报错")
	}
}

// per-credential 源监督器（多 bot 单实例，wire-protocol v1.3）：
// reconcile 按「期望凭证集合」起/停/重启源实例——新凭证起、移除停（cancel）、字段变更重启。
func TestSourceSupervisorReconcile(t *testing.T) {
	started := []string{}
	canceled := map[string]int{}
	sup := newSourceSupervisor(func(c credEntry) func() {
		started = append(started, c.ID+":"+c.Fields["k"])
		id := c.ID
		return func() { canceled[id]++ }
	})

	// 初始：两个凭证 → 起两个实例。
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}, {ID: "b", Fields: map[string]string{"k": "2"}}})
	if len(started) != 2 {
		t.Fatalf("应起 2 个实例, got %v", started)
	}
	// 幂等：同一集合再 reconcile → 不动。
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}, {ID: "b", Fields: map[string]string{"k": "2"}}})
	if len(started) != 2 || len(canceled) != 0 {
		t.Fatalf("同集合不应起停, started=%v canceled=%v", started, canceled)
	}
	// b 被移除（分片变化/凭证禁用）→ cancel b。
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "1"}}})
	if canceled["b"] != 1 {
		t.Fatalf("b 应被停止, canceled=%v", canceled)
	}
	// a 字段变更（如 session 刷新）→ 重启：cancel 旧 + 起新。
	sup.reconcile([]credEntry{{ID: "a", Fields: map[string]string{"k": "9"}}})
	if canceled["a"] != 1 || started[len(started)-1] != "a:9" {
		t.Fatalf("a 应重启, canceled=%v started=%v", canceled, started)
	}
}

// 源实例状态板（P1）：每个 源×凭证 一条运行态，随注册/心跳上报（面板展示每个 bot）。
func TestStateBoard(t *testing.T) {
	b := newStateBoard()
	b.set("updates", "cred_a", "running", "")
	b.set("updates", "cred_b", "running", "")
	b.set("updates", "cred_b", "error", "getUpdates 401")
	ss := b.snapshot()
	if len(ss) != 2 {
		t.Fatalf("应有 2 条状态, got %v", ss)
	}
	// 排序稳定（source_id → credential_id），upsert 覆盖同键。
	if ss[0].CredentialID != "cred_a" || ss[0].Status != "running" {
		t.Errorf("cred_a 应 running: %+v", ss[0])
	}
	if ss[1].CredentialID != "cred_b" || ss[1].Status != "error" || ss[1].Error != "getUpdates 401" {
		t.Errorf("cred_b 应 error: %+v", ss[1])
	}
	// 凭证实例被 reconcile 停止 → 移除其全部源状态。
	b.removeCred("cred_b")
	if ss := b.snapshot(); len(ss) != 1 || ss[0].CredentialID != "cred_a" {
		t.Errorf("cred_b 状态应移除: %+v", ss)
	}
}

// SourceCtx.UpdateCredential（P2 回写通道）：publish sokel.credential.update
// {token, credential_id=本实例绑定凭证, patch}——会话型凭证运行中刷新（如微信 token）回到平台持久化。
func TestSourceCtxUpdateCredential(t *testing.T) {
	var gotSubject string
	var gotData []byte
	sc := SourceCtx{
		token:   "skp_x",
		credID:  "cred_a",
		publish: func(subject string, data []byte) error { gotSubject = subject; gotData = data; return nil },
	}
	if err := sc.UpdateCredential(map[string]string{"session": "s2"}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	if gotSubject != "sokel.credential.update" {
		t.Errorf("subject: %q", gotSubject)
	}
	var m struct {
		Token        string            `json:"token"`
		CredentialID string            `json:"credential_id"`
		Patch        map[string]string `json:"patch"`
	}
	_ = json.Unmarshal(gotData, &m)
	if m.Token != "skp_x" || m.CredentialID != "cred_a" || m.Patch["session"] != "s2" {
		t.Errorf("消息不对: %+v", m)
	}
	// 无绑定凭证（裸实例）→ 报错不发（没有可回写的目标）。
	bare := SourceCtx{token: "skp_x", publish: func(string, []byte) error { t.Error("不应 publish"); return nil }}
	if err := bare.UpdateCredential(map[string]string{"k": "v"}); err == nil {
		t.Error("无凭证应报错")
	}
}

// SourceCtx.ReportStatus（P2）：源自报状态（如 session 失效 → auth_required），写状态板随心跳上报。
func TestSourceCtxReportStatus(t *testing.T) {
	b := newStateBoard()
	sc := SourceCtx{credID: "cred_a", sourceID: "updates", board: b}
	sc.ReportStatus("auth_required", "session 失效，需重新扫码")
	ss := b.snapshot()
	if len(ss) != 1 || ss[0].Status != "auth_required" || ss[0].CredentialID != "cred_a" {
		t.Errorf("状态板应记录 auth_required: %+v", ss)
	}
	// 无 board（测试注入的裸 ctx）→ 不 panic。
	SourceCtx{}.ReportStatus("running", "")
}

// SourceCtx.Upload：事件附件上传（无运行时回退内联字节，与操作侧 Ctx.Upload 同语义）。
func TestSourceCtxUploadFallback(t *testing.T) {
	f, err := SourceCtx{}.Upload("a.png", "image/png", []byte{1, 2, 3})
	if err != nil || f.Name != "a.png" || f.Size != 3 || len(f.Data) != 3 {
		t.Errorf("无运行时应回退内联: %+v %v", f, err)
	}
}

// 通知防抖（方案 A）：短窗内多次触发只执行一次（批量凭证变更只 re-register 一趟）。
func TestDebouncer(t *testing.T) {
	var n atomic.Int32
	d := newDebouncer(30*time.Millisecond, func() { n.Add(1) })
	d.trigger()
	d.trigger()
	d.trigger()
	time.Sleep(80 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Errorf("三连触发应合并为一次执行, got %d", got)
	}
	d.trigger()
	time.Sleep(80 * time.Millisecond)
	if got := n.Load(); got != 2 {
		t.Errorf("窗口后再触发应再执行, got %d", got)
	}
}
