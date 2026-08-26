// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

// natsTransport：NATS 出站部署。插件持 token 连入 → 注册（上报契约）→ 订阅平台分发的 subject →
// 逐调用绑定入参、跑 handler、按帧回复。非流式用 request-reply；流式向回复通道逐帧发布 + 终止符。
type natsTransport struct{}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

var processStart = time.Now().UTC().Format(time.RFC3339)

// registerBody：注册握手的载荷。
//
// 单独一个函数是为了能测：**声明了却没上报**是这套自报机制最典型的静默失效——
// 插件侧一切正常、平台侧什么也没发生，作者只能对着一个没反应的界面排查。
// （auth_flow 上线当天就栽了一次：WithAuth 写好了，这里漏了一行。）
func (p *Plugin) registerBody(instanceID, host string, ops []Operation) map[string]any {
	return map[string]any{
		"token": p.cfg.Token, "instance_id": instanceID, "host": host,
		// 进程启动时刻：注册与每拍心跳重发**同一值**——新进程新值，平台据此区分
		// 「重新上线的新副本」与「一直活着的老副本」（instance_id/host 相同时全靠它）。
		"started_at": processStart,
		"region":     pluginenv.Get("REGION"), // 可选：部署地域标注（实例表「区域」列），如 hk / sgp
		// 版本自报三档：代码 SetVersion > 环境变量 SOKEL_VERSION（发布镜像最方便的注入位）
		// > "sdk-go" 兜底。此前写死 sdk-go——实例列表的「版本」列形同虚设，
		// 配合 started_at 才答得上「线上跑的是不是我刚发的那版」。
		"version":   firstNonEmpty(p.version, pluginenv.Get("VERSION"), "sdk-go"),
		"transport": string(NATS), "operations": ops,
		"managed":           p.managed,                // token 经部署级自动注册兑换 = 随部署托管副本
		"credential_schema": p.credFields,             // 凭证契约自报（WithCredential 声明时非空；平台只读展示）
		"oauth":             p.oauth,                  // 凭证经 OAuth 获取的声明（WithOAuth）
		"auth_flow":         p.authFlow,               // 协作式认证流声明（WithAuth/WithOAuth；平台据此给凭证行加「登录」按钮）
		"events":            p.eventContract(),        // 事件契约自报（DeclareEvent 声明时非空；wire-protocol §7）
		"events_common":     p.eventsCommonContract(), // 事件公共字段契约（DeclareEventsCommon；触发时平台平铺到输入顶层）
		"capabilities":      p.capabilitiesContract(), // 可选能力自报（SetCapabilities）：同一操作"做到什么程度"
		"doc":               p.doc,                    // 使用说明 markdown（SetDoc / plugin.DeclareDoc 声明）
		"doc_url":           p.docURL,                 // 使用说明外链（已有文档站时用它，别抄一份进来）
	}
}

func (natsTransport) run(p *Plugin) error {
	// 统一端点：https 平台地址 → 经 /connect-info 发现真实承载地址；直填 nats:// 也兼容（跳过发现）。
	target, err := discoverNATS(p.cfg.Endpoint, p.cfg.Token)
	if err != nil {
		return err
	}
	// NATS 连接鉴权 token：优先用 SOKEL_NATS_TOKEN（broker 的共享传输密钥），
	// 缺省回退到接入 token（无鉴权 broker 会忽略它，向后兼容）。接入 token 仍在注册握手 payload 里做身份认定。
	natsToken := pluginenv.Get("NATS_TOKEN")
	if natsToken == "" {
		natsToken = p.cfg.Token
	}
	// RetryOnFailedConnect：启动时 broker 未就绪不退出（挂起等它就绪）；断连无限重连、订阅自动恢复。
	opts := []nats.Option{
		nats.Token(natsToken), nats.Name(p.cfg.Name), nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true), nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, derr error) { log.Printf("[sokel] 与平台断开: %v（自动重连中）", derr) }),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Printf("[sokel] 已重连平台: %s", c.ConnectedUrl()) }),
	}
	if ca := pluginenv.Get("NATS_CA"); ca != "" { // 连 TLS broker 且证书非系统信任时的自定义 CA
		opts = append(opts, nats.RootCAs(ca))
	}
	nc, err := nats.Connect(target, opts...)
	if err != nil {
		return fmt.Errorf("连接平台 %q: %w", target, err)
	}
	defer nc.Drain()

	// 自动注册(开箱即用):没预置接入 token 但有部署级密钥(SOKEL_DEPLOY_KEY)时,
	// 先经 sokel.enroll 用「插件名 + 密钥」兑换本插件默认组的真 token——随部署托管的
	// 第一方插件容器凭它免去人肉复制接入命令。兑换后流程与手动配置完全一致。
	// 注:此路径要求 Endpoint 直填 nats://(托管容器即如此);https 统一端点的发现步骤
	// 本身需要接入 token,先有鸡还是先有蛋,不支持。平台侧未开启(未设 PLUGIN_ENROLL_KEY)
	// 时请求无人应答,与注册同款 8s 重试——症状可见,不静默。
	if p.cfg.Token == "" {
		if key := pluginenv.Get("DEPLOY_KEY"); key != "" {
			payload, _ := json.Marshal(map[string]string{"plugin": p.cfg.Name, "key": key})
			for {
				resp, rerr := nc.Request("sokel.enroll", payload, 8*time.Second)
				if rerr == nil {
					var er struct {
						OK    bool   `json:"ok"`
						Token string `json:"token"`
						Error string `json:"error"`
					}
					_ = json.Unmarshal(resp.Data, &er)
					if er.OK && er.Token != "" {
						p.cfg.Token = er.Token
						p.managed = true // 注册时自报「随部署托管」,实例列表标注来源
						log.Printf("[sokel] 自动注册成功（deploy key → 默认组 token）")
						break
					}
					log.Printf("[sokel] 自动注册被拒（%s），8s 后重试…", er.Error)
				} else {
					log.Printf("[sokel] 自动注册请求失败（%v），8s 后重试…", rerr)
				}
				time.Sleep(8 * time.Second)
			}
		}
	}

	host, _ := os.Hostname()
	instanceID := stableInstanceID(p.cfg.Token) // 稳定副本身份：重启复用（落盘/env），不再每次注册都算新实例
	ops := p.contract()

	board := newStateBoard() // 源实例运行态（P1）：源 goroutine 写，注册/心跳带 snapshot 上报
	var notifySubject string // 凭证变更即时通知的组广播 subject（方案 A；回包下发）
	register := func() (subject, name string, creds []credEntry, err error) {
		body := p.registerBody(instanceID, host, ops)
		if len(p.sources) > 0 {
			body["source_states"] = board.snapshot() // 每个 源×凭证 的 running/error/exited（面板展示每个 bot）
		}
		payload, _ := json.Marshal(body)
		resp, rerr := nc.Request("sokel.register", payload, 8*time.Second)
		if rerr != nil {
			return "", "", nil, rerr
		}
		var reg struct {
			OK            bool              `json:"ok"`
			Name          string            `json:"name"`
			Subject       string            `json:"subject"`
			NotifySubject string            `json:"notify_subject"` // 凭证变更即时通知（组广播；方案 A）
			Error         string            `json:"error"`
			Credentials   []credEntry       `json:"credentials"`   // v1.3：分配给本实例的凭证子集（多 bot 单实例）
			Credential    map[string]string `json:"credential"`    // 旧平台兼容：单凭证字段
			CredentialID  string            `json:"credential_id"` // 单凭证 id（事件路由键）
		}
		_ = json.Unmarshal(resp.Data, &reg)
		if !reg.OK || reg.Subject == "" {
			return "", "", nil, fmt.Errorf("注册被拒：%s", reg.Error)
		}
		if reg.NotifySubject != "" {
			notifySubject = reg.NotifySubject
		}
		// 凭证集合优先复数（v1.3 分片下发）；旧平台只有单数 → 折算为单元素集合（行为同旧版）。
		creds = reg.Credentials
		if len(creds) == 0 && (reg.CredentialID != "" || len(reg.Credential) > 0) {
			creds = []credEntry{{ID: reg.CredentialID, Fields: reg.Credential}}
		}
		return reg.Subject, reg.Name, creds, nil
	}

	// 启动注册：失败不退出（broker/平台可能尚未就绪），按固定间隔重试直到成功——
	// 与运行期断连重连（订阅自动恢复 + 心跳 re-register）一起构成完整自愈。
	subject, name, creds, err := register()
	for err != nil {
		log.Printf("[sokel] 注册失败（%v），8s 后重试…", err)
		time.Sleep(8 * time.Second)
		subject, name, creds, err = register()
	}

	rt := natsFiles{nc: nc, token: p.cfg.Token} // 文件字节经同一条 NATS 连接分块传输
	// QueueSubscribe：同组多副本共享一个队列组 → 每次调用只投递给其中一个副本（真·负载均衡）。
	// 曾用普通 Subscribe——多副本时每次调用被广播、各副本重复执行（POST 类副作用重复！），先回者赢。
	if _, err := nc.QueueSubscribe(subject, "sokel-workers", func(m *nats.Msg) {
		p.dispatchNATS(nc, m, rt, instanceID)
	}); err != nil {
		return fmt.Errorf("订阅失败: %w", err)
	}
	log.Printf("[sokel] 已接入平台：插件「%s」就绪，副本 %s 监听 %s", name, instanceID, subject)

	// 常驻事件源（多 bot 单实例，wire-protocol v1.3）：per-credential supervisor——
	// 平台每次注册/心跳下发「分配给本实例的凭证子集」（在线实例取模分片），supervisor reconcile：
	// 每个凭证起一套源 goroutine（SourceCtx 绑定该凭证，Trigger 自动回带其 credential_id）；
	// 凭证被移除/分片迁走 → cancel 该实例（fn 里 ctx.Err() 感知退出）；字段变更（如 session 刷新）→ 重启。
	// 无凭证插件跑一个空凭证实例（= 旧行为）。多副本因此成为 bot 横向扩展 + 自动故障转移。
	var supervisor *sourceSupervisor
	if len(p.sources) > 0 {
		valid := map[string]bool{}
		for _, e := range p.events {
			valid[e.ID] = true
		}
		supervisor = newSourceSupervisor(func(c credEntry) func() {
			ctx, cancel := context.WithCancel(context.Background())
			for _, se := range p.sources {
				se := se
				// 每源一份 ctx（带 sourceID/board/文件运行时）：ReportStatus 落到正确的「源×凭证」条目；
				// rt 供事件附件上传（图片/文件/语音 → 平台文件引用进 payload）。
				sctx := SourceCtx{Context: ctx, token: p.cfg.Token, valid: valid, publish: nc.Publish, cred: c.Fields, credID: c.ID, sourceID: se.src.ID, board: board, rt: rt}
				go func() {
					log.Printf("[sokel] 事件源「%s」启动（credential=%s）", se.src.ID, orBare(c.ID))
					board.set(se.src.ID, c.ID, "running", "")
					err := se.fn(sctx)
					if ctx.Err() != nil {
						board.removeCred(c.ID) // reconcile 停止：状态整体移除（不再上报）
						return
					}
					if err != nil {
						log.Printf("[sokel] 事件源「%s」退出（credential=%s）: %v", se.src.ID, orBare(c.ID), err)
						board.set(se.src.ID, c.ID, "error", err.Error())
					} else {
						board.set(se.src.ID, c.ID, "exited", "")
					}
				}()
			}
			return cancel
		})
		supervisor.reconcile(desiredSourceCreds(creds))

		// 凭证变更即时通知（方案 A）：平台在凭证增删改/扫码写入 session 时 publish 组广播，
		// 收到即防抖 re-register + reconcile —— 新 bot 秒级上线、删 bot 秒级停止（心跳 20~40s 仅兜底）。
		// 普通订阅（非队列组）：组内全部副本都要收（各自的分配集合都可能变化）。
		if notifySubject != "" {
			deb := newDebouncer(300*time.Millisecond, func() {
				if _, _, ncreds, rerr := register(); rerr == nil {
					supervisor.reconcile(desiredSourceCreds(ncreds))
				} else {
					log.Printf("[sokel] 凭证变更 re-register 失败: %v", rerr)
				}
			})
			if _, serr := nc.Subscribe(notifySubject, func(*nats.Msg) { deb.trigger() }); serr != nil {
				log.Printf("[sokel] 订阅凭证变更通知失败: %v", serr)
			}
		}
	}

	// 心跳续约保持副本在线；SIGINT/SIGTERM（docker stop / Ctrl-C）→ 优雅下线：
	// 通知平台立即标记 offline（秒级感知），而非等心跳超时清扫（45s+）。crash 仍走超时兜底。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if _, _, hbCreds, err := register(); err != nil {
				log.Printf("[sokel] 心跳续约失败: %v", err)
			} else if supervisor != nil {
				// 每次心跳按最新分配集合 reconcile：分片迁移/凭证增删/字段刷新 → 起停/重启对应源实例。
				supervisor.reconcile(desiredSourceCreds(hbCreds))
			}
		case sig := <-quit:
			bye, _ := json.Marshal(map[string]any{"token": p.cfg.Token, "instance_id": instanceID})
			_ = nc.Publish("sokel.unregister", bye)
			_ = nc.Flush() // 确保下线通知先于断连送达
			log.Printf("[sokel] 收到 %v，已通知平台下线，退出", sig)
			return nil // defer Drain 收尾
		}
	}
}

// dispatchNATS 处理一次调用。
func (p *Plugin) dispatchNATS(nc *nats.Conn, m *nats.Msg, rt fileRuntime, instanceID string) {
	if m.Reply == "" {
		return
	}
	var call struct {
		Operation    string            `json:"operation"`
		Input        json.RawMessage   `json:"input"`
		Credential   map[string]string `json:"credential"`    // 平台下发的解析后凭证字段（SDK 插件自己发外部请求时用）
		CredentialID string            `json:"credential_id"` // 凭证 id（webhook 帧的事件路由键）
		Trace        map[string]string `json:"trace"`         // 平台下发的追踪上下文（run_id/workflow_id/node_id），仅用于日志
	}
	_ = json.Unmarshal(m.Data, &call)
	tag := traceTag(call.Trace)
	// 平台代收 webhook 的特殊帧：分发前拦截（老 SDK 没这段会走 unknown operation，
	// 平台把它翻译成「插件未注册 webhook 处理器」）。
	if call.Operation == "__webhook__" {
		log.Printf("[sokel] ← webhook 入站%s", tag)
		valid := map[string]bool{}
		for _, e := range p.events {
			valid[e.ID] = true
		}
		sctx := SourceCtx{Context: context.Background(), token: p.cfg.Token, valid: valid,
			publish: nc.Publish, cred: call.Credential, credID: call.CredentialID, sourceID: "webhook", rt: rt}
		_ = m.Respond(p.handleWebhookFrame(sctx, call.Input))
		return
	}
	entry := p.find(call.Operation)
	if entry == nil && len(p.ops) == 1 {
		entry = &p.ops[0] // 单操作插件：operation 省略时默认唯一操作
	}
	if entry == nil {
		log.Printf("[sokel] ✗ 未知操作 %q%s", call.Operation, tag)
		_ = m.Respond([]byte(fmt.Sprintf(`{"error":"unknown operation %q"}`, call.Operation)))
		return
	}
	op := entry.op.ID
	log.Printf("[sokel] ← %s 开始%s", op, tag)
	start := time.Now()
	// Trace 塞进 context：插件可用 sokel.TraceValue(ctx, "run_id") 取运行标识——
	// 发送类插件拿它派生幂等键（同一次节点执行重试多少遍都是同一条消息）。
	ctx := natsCtx{Context: context.WithValue(context.Background(), traceCtxKey{}, call.Trace), rt: rt, cred: call.Credential}

	if entry.op.Stream {
		// 流式：逐帧发布到回复通道，末尾发终止符。
		sink := &natsStreamSink{nc: nc, reply: m.Reply, instance: instanceID}
		if err := entry.invoke(ctx, call.Input, sink); err != nil {
			log.Printf("[sokel] ✗ %s 失败(%s)%s: %v", op, time.Since(start).Round(time.Millisecond), tag, err)
			b, _ := json.Marshal(frame{Kind: "error", Text: err.Error()})
			_ = nc.PublishMsg(msgWithInstance(m.Reply, instanceID, b))
		} else {
			log.Printf("[sokel] ✓ %s 完成(%s)%s", op, time.Since(start).Round(time.Millisecond), tag)
		}
		_ = nc.PublishMsg(msgWithInstance(m.Reply, instanceID, []byte(`{"kind":"end"}`)))
		return
	}

	// 非流式：缓冲各帧 → 合并 variables 为单次回复（= 节点输出对象，兼容平台现有分发）。
	sink := &bufferSink{}
	if err := entry.invoke(ctx, call.Input, sink); err != nil {
		log.Printf("[sokel] ✗ %s 失败(%s)%s: %v", op, time.Since(start).Round(time.Millisecond), tag, err)
		_ = m.RespondMsg(msgWithInstance(m.Reply, instanceID, []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))))
		return
	}
	log.Printf("[sokel] ✓ %s 完成(%s)%s", op, time.Since(start).Round(time.Millisecond), tag)
	reply, _ := json.Marshal(sink.vars)
	_ = m.RespondMsg(msgWithInstance(m.Reply, instanceID, reply)) // 落点自报（0141）
}

// instanceHeader：回包/流帧上自报副本身份的 header 名（平台侧同名常量，0141）。
// 老平台不读它、老 SDK 不发它，双向都优雅缺省。
const instanceHeader = "Sokel-Instance"

func msgWithInstance(subject, instanceID string, data []byte) *nats.Msg {
	msg := nats.NewMsg(subject)
	msg.Header.Set(instanceHeader, instanceID)
	msg.Data = data
	return msg
}

// traceTag：追踪上下文 → 日志后缀 " [run=… wf=… node=…]"（无上下文时为空串）。
func traceTag(t map[string]string) string {
	if len(t) == 0 {
		return ""
	}
	s := ""
	for _, k := range []struct{ key, label string }{{"run_id", "run"}, {"workflow_id", "wf"}, {"node_id", "node"}, {"trace_id", "tr"}} {
		if v := t[k.key]; v != "" {
			if s != "" {
				s += " "
			}
			s += k.label + "=" + v
		}
	}
	if s == "" {
		return ""
	}
	return " [" + s + "]"
}

// bufferSink 非流式汇聚：合并所有 variables 帧（后帧覆盖同名字段）；text/json 仅流式展示用，此处忽略。
type bufferSink struct{ vars map[string]any }

func (s *bufferSink) emit(f frame) {
	if f.Kind != frameVars {
		return
	}
	if s.vars == nil {
		s.vars = map[string]any{}
	}
	for k, v := range f.Vars {
		s.vars[k] = v
	}
}

// natsStreamSink 流式汇聚：每帧作为一条消息发布到回复通道。
type natsStreamSink struct {
	nc       *nats.Conn
	reply    string
	instance string // 落点自报（0141）：每帧 header 带副本身份
}

func (s *natsStreamSink) emit(f frame) {
	b, _ := json.Marshal(f)
	_ = s.nc.PublishMsg(msgWithInstance(s.reply, s.instance, b))
}
