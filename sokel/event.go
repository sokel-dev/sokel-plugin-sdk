// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// 插件事件触发（见 docs/plugin-event-triggers.md + plugin-wire-protocol.md §7）。
// 事件 = 第三种自报契约（与 Operation / 凭证并列）：插件声明「我产哪些事件、每种什么 payload」，
// 常驻事件源循环里用 ctx.Trigger 主动把外部事件推给平台起 workflow。
//
// 与操作的区别：操作是 request/reply（平台调插件）；事件是 fire-and-forget（插件推平台）。

const triggerSubject = "sokel.trigger"

// Event 一个事件类型的契约。Fields = 该事件 payload 的类型化契约（与操作 I/O 同一套 Field/反射）。
// Event 定义在 plugin-core/contract —— 与操作契约同处，平台与 SDK 共用一份。
type Event = contract.Event

// Source 一个常驻事件源（长轮询 / 订阅外部系统等）。SDK 为它起一个 goroutine 跑 fn。
type Source struct {
	ID    string
	Label string
}

type sourceEntry struct {
	src Source
	fn  func(SourceCtx) error
}

// DeclareEvent 声明一种事件及其 payload 契约。Fields 留空时由 T 反射推导（同 Operation 的 In/Out）。
func DeclareEvent[T any](p *Plugin, e Event) {
	if e.Fields == nil {
		e.Fields = deriveFields(reflect.TypeOf(new(T)).Elem())
	}
	if e.Fields == nil {
		e.Fields = []Field{}
	}
	p.events = append(p.events, e)
}

// DeclareEvent 实现 plugin.EventHost：声明一种事件（NATS 传输）。
func (p *Plugin) DeclareEvent(e contract.Event) { p.events = append(p.events, e) }

// DeclareEventsCommon 实现 plugin.EventHost：记下公共字段（校验已在 contract 侧做过）。
func (p *Plugin) DeclareEventsCommon(fields []contract.Field, _ []string) {
	p.eventsCommon = fields
}

// RegisterSource 注册一个常驻事件源；p.Run() 时 SDK 起 goroutine 执行 fn，fn 内用 ctx.Trigger 推事件。
func RegisterSource(p *Plugin, src Source, fn func(SourceCtx) error) {
	p.sources = append(p.sources, sourceEntry{src: src, fn: fn})
}

// eventContract 上报给平台的事件契约（注册握手 events 字段）。
func (p *Plugin) eventContract() []Event { return p.events }

// DeclareEventsCommon 声明「所有事件 payload 共有」的公共字段（如 tg 的 chat_id）。
// 平台触发时把这些字段从 payload 平铺到输入顶层（{{节点.chat_id}}），各事件分支共享同一变量。
// 须在全部 DeclareEvent 之后调用；强校验 fail fast（不静默缩水，避免新增事件悄悄破坏存量流）：
//   - 每个字段必须在【所有】已声明事件的契约中存在且类型一致；
//   - 不得与保留字（_event/event/input/credential_id）或任何事件 id 撞名
//     （credential_id 由平台平铺：推事件的 bot 凭证 id，下游动态凭证回话用）。
func DeclareEventsCommon(p *Plugin, names ...string) error {
	if len(p.events) == 0 {
		return fmt.Errorf("公共字段须在 DeclareEvent 之后声明（当前无任何事件）")
	}
	reserved := map[string]bool{"_event": true, "event": true, "input": true, "credential_id": true}
	eventIDs := map[string]bool{}
	for _, e := range p.events {
		eventIDs[e.ID] = true
	}
	var out []Field
	for _, name := range names {
		if reserved[name] {
			return fmt.Errorf("公共字段「%s」与保留字冲突", name)
		}
		if eventIDs[name] {
			return fmt.Errorf("公共字段「%s」与事件 id 冲突", name)
		}
		var spec *Field
		for _, e := range p.events {
			var hit *Field
			for i := range e.Fields {
				if e.Fields[i].Name == name {
					hit = &e.Fields[i]
					break
				}
			}
			if hit == nil {
				return fmt.Errorf("公共字段「%s」在事件「%s」的契约中不存在", name, e.ID)
			}
			if spec == nil {
				spec = hit
			} else if spec.Type != hit.Type {
				return fmt.Errorf("公共字段「%s」在各事件中类型不一致（%s vs %s）", name, spec.Type, hit.Type)
			}
		}
		out = append(out, *spec)
	}
	p.eventsCommon = out
	return nil
}

// eventsCommonContract 上报给平台的事件公共字段契约（注册握手 events_common 字段）。
func (p *Plugin) eventsCommonContract() []Field { return p.eventsCommon }

// credEntry：注册回包 credentials 列表项 —— 分配给本实例的一个 bot 身份（wire-protocol v1.3）。
type credEntry struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

// fieldsSig：凭证字段的稳定签名（排序 k=v 拼接），供 reconcile 判定「字段变更 → 重启该源实例」。
func (c credEntry) fieldsSig() string {
	keys := make([]string, 0, len(c.Fields))
	for k := range c.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(c.Fields[k])
		b.WriteByte('\n')
	}
	return b.String()
}

// desiredSourceCreds：注册回包凭证集合 → supervisor 期望集合。空（无凭证插件）→ 一个空凭证裸实例（旧行为）。
func desiredSourceCreds(creds []credEntry) []credEntry {
	if len(creds) == 0 {
		return []credEntry{{}}
	}
	return creds
}

// orBare：日志用——空凭证 id 显示 "(无凭证)"。
func orBare(id string) string {
	if id == "" {
		return "(无凭证)"
	}
	return id
}

// debouncer：合并短窗内的多次触发为一次执行（凭证变更通知可能连发——批量增删只 re-register 一趟）。
type debouncer struct {
	mu    sync.Mutex
	timer *time.Timer
	d     time.Duration
	fn    func()
}

func newDebouncer(d time.Duration, fn func()) *debouncer {
	return &debouncer{d: d, fn: fn}
}

func (b *debouncer) trigger() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(b.d, b.fn)
}

// sourceState：一个「源×凭证」实例的运行态（P1，随注册/心跳 source_states 上报，面板展示每个 bot）。
type sourceState struct {
	SourceID     string `json:"source_id"`
	CredentialID string `json:"credential_id,omitempty"`
	Status       string `json:"status"` // running | error | exited（auth_required 预留给 P2）
	Error        string `json:"error,omitempty"`
	Since        string `json:"since"` // RFC3339，进入当前状态的时刻
}

// stateBoard：源实例状态板（并发安全）。键 = source_id × credential_id；snapshot 稳定排序供上报。
type stateBoard struct {
	mu  sync.Mutex
	m   map[string]sourceState
	now func() string // 可注入（测试）；生产 = RFC3339 当前时刻
}

func newStateBoard() *stateBoard {
	return &stateBoard{m: map[string]sourceState{}, now: func() string { return time.Now().Format(time.RFC3339) }}
}

func (b *stateBoard) set(sourceID, credID, status, errMsg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[sourceID+"|"+credID] = sourceState{SourceID: sourceID, CredentialID: credID, Status: status, Error: errMsg, Since: b.now()}
}

// removeCred：某凭证实例被 reconcile 停止 → 移除其全部源状态（不再上报）。
func (b *stateBoard) removeCred(credID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, st := range b.m {
		if st.CredentialID == credID {
			delete(b.m, k)
		}
	}
}

func (b *stateBoard) snapshot() []sourceState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]sourceState, 0, len(b.m))
	for _, st := range b.m {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].CredentialID < out[j].CredentialID
	})
	return out
}

// sourceSupervisor：per-credential 源实例监督器（多 bot 单实例）。
// 每次注册/心跳按平台下发的「分配凭证集合」reconcile：新凭证起实例、被移除停（cancel）、
// 字段变更（如 session 刷新）重启。start 返回该实例的停止函数。
type sourceSupervisor struct {
	mu      sync.Mutex
	running map[string]struct {
		stop func()
		sig  string
	}
	start func(credEntry) func()
}

func newSourceSupervisor(start func(credEntry) func()) *sourceSupervisor {
	return &sourceSupervisor{running: map[string]struct {
		stop func()
		sig  string
	}{}, start: start}
}

func (s *sourceSupervisor) reconcile(desired []credEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]credEntry{}
	for _, c := range desired {
		want[c.ID] = c
	}
	// 停：不在期望集合，或字段变更（先停后起 = 重启）。
	for id, r := range s.running {
		c, ok := want[id]
		if ok && c.fieldsSig() == r.sig {
			continue
		}
		r.stop()
		delete(s.running, id)
	}
	// 起：期望但未运行。
	for id, c := range want {
		if _, ok := s.running[id]; ok {
			continue
		}
		s.running[id] = struct {
			stop func()
			sig  string
		}{stop: s.start(c), sig: c.fieldsSig()}
	}
}

// SourceCtx 常驻事件源的运行上下文：Trigger 主动向平台推事件；Credential 取本实例绑定的凭证。
// 多 bot 单实例（v1.3）：每个源实例绑定**一个**凭证（supervisor 按分配集合起停），
// ctx.Context 在凭证被移除/变更时取消（fn 里的 ctx.Err() 检查即感知退出）。
type SourceCtx struct {
	context.Context
	token    string
	valid    map[string]bool                         // 已声明的事件 id 集（挡拼写错误）
	publish  func(subject string, data []byte) error // 生产=nc.Publish；测试可注入
	cred     map[string]string                       // 本实例绑定凭证的字段（如 telegram bot_token）
	credID   string                                  // 本实例绑定凭证 id（事件路由键，Trigger 自动回带）
	sourceID string                                  // 本源 id（ReportStatus 写状态板用）
	board    *stateBoard                             // 状态板（随心跳上报；测试注入的裸 ctx 可为 nil）
	rt       fileRuntime                             // 文件运行时（事件附件上传；测试注入的裸 ctx 可为 nil）
}

// Upload 产出一个平台文件引用（与操作侧 Ctx.Upload 同语义）：事件源把消息附件（图片/文件/语音）
// 上传回平台文件层，引用放进事件 payload → 下游文件参数原生可用。无运行时（测试）回退内联字节。
func (c SourceCtx) Upload(name, mime string, data []byte) (*File, error) {
	if c.rt == nil {
		return &File{Name: name, Mime: mime, Size: int64(len(data)), Data: data}, nil
	}
	return c.rt.store(c.Context, name, mime, data)
}

// UploadReader 边读边传（与操作侧同语义）：事件源搬 NAS 上的大文件时用它，
// 内存占用恒为一个块。
func (c SourceCtx) UploadReader(name, mime string, r io.Reader) (*File, error) {
	if c.rt == nil {
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return &File{Name: name, Mime: mime, Size: int64(len(b)), Data: b}, nil
	}
	return c.rt.storeReader(c.Context, name, mime, r)
}

// Credential 返回本源实例绑定的凭证字段（事件源用，如 telegram bot_token）。可能为空（无凭证插件）。
func (c SourceCtx) Credential() map[string]string { return c.cred }

// UpdateCredential 把 patch（部分覆盖）回写到本实例绑定的平台凭证（P2 回写通道，wire-protocol v1.4）。
// 会话型凭证（如微信 session）运行中刷新后必须回写——平台是唯一凭证存储方，本地不落地；
// 平台按接入组 token 校验（只能改本组凭证）。fire-and-forget publish。
func (c SourceCtx) UpdateCredential(patch map[string]string) error {
	if c.credID == "" {
		return fmt.Errorf("本源实例未绑定凭证，无可回写目标")
	}
	if len(patch) == 0 {
		return nil
	}
	data, err := json.Marshal(map[string]any{"token": c.token, "credential_id": c.credID, "patch": patch})
	if err != nil {
		return err
	}
	return c.publish("sokel.credential.update", data)
}

// ReportStatus 源自报运行态（P2）：如检测到 session 失效 → ReportStatus("auth_required", …)，
// 面板凭证/实例行随心跳亮「待登录」。status ∈ running/error/exited/auth_required。
func (c SourceCtx) ReportStatus(status, msg string) {
	if c.board == nil {
		return
	}
	c.board.set(c.sourceID, c.credID, status, msg)
}

type triggerMsg struct {
	Token        string `json:"token"`
	Event        string `json:"event"`
	EventID      string `json:"event_id,omitempty"`      // 幂等键（平台去重，如外部系统 update_id）
	CredentialID string `json:"credential_id,omitempty"` // 该 bot 凭证 id（路由键，SDK 自动回带注册下发的值）
	Payload      any    `json:"payload"`
}

// Trigger 推送一个事件到平台（fire-and-forget）。
//   - event：必须是已 DeclareEvent 声明的事件 id；
//   - eventID：幂等键，平台按 (pluginId,event,eventID) 去重，可空；
//   - payload：按该事件 Fields 契约的对象（下游节点按契约引用）。
func (c SourceCtx) Trigger(event, eventID string, payload any) error {
	if !c.valid[event] {
		return fmt.Errorf("未声明的事件 %q（先 DeclareEvent 声明）", event)
	}
	// payload struct → sokel tag 命名的 map（与声明的 Fields 契约同名，下游按契约引用）；
	// 直接 json.Marshal 会用 Go 字段名/json tag，与 sokel 契约名不一致——必须走 structToVars（同 Emitter.Vars）。
	var out any = payload
	if payload != nil {
		if m := structToVars(payload); m != nil {
			out = m
		}
	}
	data, err := json.Marshal(triggerMsg{Token: c.token, Event: event, EventID: eventID, CredentialID: c.credID, Payload: out})
	if err != nil {
		return fmt.Errorf("序列化事件 %q: %w", event, err)
	}
	return c.publish(triggerSubject, data)
}

// 编译期确认：sokel 是事件侧接口的 NATS 实现。
var (
	_ plugin.EventHost = (*Plugin)(nil)
	_ plugin.SourceCtx = SourceCtx{}
)

// Fetch 实现 plugin.Ctx：事件源里也能取文件字节（如回读刚上传的附件）。
func (c SourceCtx) Fetch(f *File) ([]byte, error) {
	if c.rt == nil {
		return nil, errors.New("文件运行时未就绪")
	}
	return c.rt.fetch(c.Context, f)
}
