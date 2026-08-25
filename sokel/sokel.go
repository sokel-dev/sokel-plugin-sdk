// Package sokel is the Sokel plugin SDK for Go.
//
// 一套写法，传输无关：插件作者用类型化 In/Out struct 声明操作契约（反射自动上报平台），
// 用 Emitter 逐帧产出结果（文本 / JSON / 类型化变量 / 文件），完全不碰底层传输。
// 作者只需两个配置：Endpoint（平台地址，https 统一端点）+ Token。
// 当前承载为 NATS（request-reply + inbox 多帧，覆盖非流式/流式）；SDK 经平台
// /connect-info 发现真实承载地址 —— 未来新增 transport 只改发现与 SDK 内部，作者配置不变。
package sokel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"time"
)

// Transport 当前承载方式（注册时上报，UI 按实例展示）。
type Transport string

const (
	NATS Transport = "nats"
)

// Config：作者只需端点 + token；handler 写法与部署方式无关。
type Config struct {
	Endpoint string // 平台统一端点（https://hub.example.com）；也兼容直填 nats://（跳过发现）
	Token    string // 接入 token（插件管理里生成）
	Name     string // 客户端名（可选，默认取可执行名）
}

// Ctx 传给 handler：内嵌 context.Context，并携带文件传输所需的运行时句柄。
// Ctx 就是 plugin.Ctx —— **传输无关的接口**。
//
// 插件作者继续写 func(ctx sokel.Ctx, …)，签名不变；但它现在是接口，
// 于是同一个 handler 既能挂 NATS 传输（下面的 natsCtx），也能挂平台的进程内宿主。
type Ctx = plugin.Ctx

// natsCtx：NATS 传输下的运行时能力实现。
type natsCtx struct {
	context.Context
	rt   fileRuntime
	cred map[string]string
}

// Credential 返回本次调用平台下发的、已解析的凭证字段（SDK/NATS 插件自己发外部请求时用它拼鉴权）。
// - 通用鉴权方案凭证带 "_scheme" 键（auth_basic/auth_bearer/auth_header/auth_query…），其余为该方案字段（username/password、token、header_name/value…）；
// - 服务凭证为其 credentialSchema 声明的字段（如 api_key、base_url）。
// 无凭证时返回 nil。平台是唯一存储/加密方，插件从不落地凭证。
func (c natsCtx) Credential() map[string]string { return c.cred }

type opEntry struct {
	op     Operation
	invoke func(ctx Ctx, input json.RawMessage, sink emitterCore) error
}

// Plugin 插件实例：持配置与操作表，Run() 按传输连接/监听并分发。
type Plugin struct {
	version    string // SetVersion 显式声明的插件版本（注册自报；空则回退 SOKEL_VERSION 环境变量）
	cfg        Config
	ops        []opEntry
	credFields []Field // 凭证契约（WithCredential 反射 Cred 结构体得到）；注册时上报，平台只读展示（client 自报）
	// doc/docURL：使用说明（markdown / 外链）。注册时上报，平台在「使用说明」抽屉里渲染。
	// 说明书跟着插件代码走：改了重新部署即更新，平台不提供编辑入口。
	doc    string
	docURL string
	// oauth：凭证经 OAuth 获取的声明（WithOAuth）。nil = 不走 OAuth。
	oauth *OAuthSpec
	// authFlow：协作式认证流的声明（WithAuth / WithOAuth）。nil = 该插件的凭证靠手填。
	authFlow     *authFlowDecl
	events       []Event       // 事件契约（DeclareEvent 反射得到）；注册时上报（见 event.go / wire-protocol §7）
	eventsCommon []Field       // 事件公共字段契约（DeclareEventsCommon）：全事件共有字段，触发时平台平铺到输入顶层
	sources      []sourceEntry // 常驻事件源（RegisterSource）；Run() 时各起一个 goroutine
	// capabilities：可选能力自报（SetCapabilities，见 capabilities.go）。
	// 「操作有没有」看 operations；「某个操作做到什么程度」看这里。
	capabilities map[string]bool
	// webhookFn：平台代收 webhook 的处理器（RegisterWebhook，见 webhook.go）。nil = 不支持。
	webhookFn func(WebhookCtx, *WebhookRequest) WebhookResponse
	// managed：本副本的 token 经部署级自动注册（sokel.enroll）兑换而来 = 随部署托管。
	// 注册时自报，平台在实例列表标注来源（托管 / 自部署）。
	managed bool
}

func New(cfg Config) *Plugin {
	if cfg.Name == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.Name = baseName(exe)
		} else {
			cfg.Name = "sokel-plugin"
		}
	}
	return &Plugin{cfg: cfg}
}

// Register 注册一个类型化操作。In/Out 为入/出参 struct（sokel tag 标注）；
// 契约由反射自动推导（除非 op.Inputs/Outputs 已显式给出）。handler 用 Emitter[Out] 产出结果。
func Register[In any, Out any](p *Plugin, op Operation, h func(Ctx, In, *Emitter[Out]) error) {
	mustBusinessOpID(op.ID)
	registerTyped(p, op, h)
}

// registerTyped：注册本体（不校验 id）。业务操作经 Register 进来（先过校验），
// 平台保留 id 的内部操作（认证流）经 registerReserved 进来。
func registerTyped[In any, Out any](p *Plugin, op Operation, h func(Ctx, In, *Emitter[Out]) error) {
	if op.Inputs == nil {
		op.Inputs = deriveFields(reflect.TypeOf(new(In)).Elem())
	}
	if op.Outputs == nil {
		op.Outputs = deriveFields(reflect.TypeOf(new(Out)).Elem())
	}
	// 无入/出参的操作：上报空数组而非 null，避免下游（平台/前端契约视图）对 null 解引用。
	if op.Inputs == nil {
		op.Inputs = []Field{}
	}
	if op.Outputs == nil {
		op.Outputs = []Field{}
	}
	p.ops = append(p.ops, opEntry{
		op: op,
		invoke: func(ctx Ctx, input json.RawMessage, sink emitterCore) (err error) {
			var in In
			if err := bindInput(input, &in); err != nil {
				return fmt.Errorf("绑定入参失败: %w", err)
			}
			// handler panic 兜底：转成 error 帧（节点标红、报可读错误），不让单次坏调用崩掉整个插件进程。
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("操作 %q 内部 panic: %v\n%s", op.ID, r, debug.Stack())
				}
			}()
			return h(ctx, in, &Emitter[Out]{core: sink})
		},
	})
}

// mustBusinessOpID：业务操作 id 必须是 ^[a-z][a-z0-9_]*$。
//
// 这条规则同时买下两样东西：**带点号的 id 成为平台保留命名空间**（认证流的 auth.start
// 等就落在那儿，业务 id 结构上撞不上），以及大写/连字符这类会在下游各处别扭的写法当场被拦。
// 启动即 panic 而不是静默改名——操作 id 发布后是画布图里的引用路径，改名等于断链。
func mustBusinessOpID(id string) {
	if id == "" {
		panic("sokel: 操作 id 不能为空")
	}
	for i, r := range id {
		ok := (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			if r == '.' {
				panic(fmt.Sprintf("sokel: 操作 id %q 落在平台保留命名空间（带点号）——认证流请用 sokel.WithAuth 声明", id))
			}
			panic(fmt.Sprintf("sokel: 操作 id %q 不合法，必须是小写字母/数字/下划线（^[a-z][a-z0-9_]*$）", id))
		}
	}
	// 旧约定的三个名字：曾经靠嗅探它们来认认证流，现在改成声明式（见 auth.go 开头）。
	// 不拦的话，老插件升级 SDK 后按钮会静默消失，作者只能对着一个「什么都没发生」排查。
	switch id {
	case "auth_start", "auth_submit", "auth_poll":
		panic(fmt.Sprintf("sokel: 操作 id %q 是旧的认证流约定，已改为声明式：sokel.WithAuth(p, sokel.AuthFlow{…})", id))
	}
}

// registerReserved：注册平台保留 id 的内部操作（认证流等）。走 registerTyped 绕开 id 校验，
// 但反射推导与 panic 兜底与业务操作**同一份实现**。
func registerReserved[In any, Out any](p *Plugin, op Operation, h func(Ctx, In) (Out, error)) {
	registerTyped(p, op, func(ctx Ctx, in In, out *Emitter[Out]) error {
		res, err := h(ctx, in)
		if err != nil {
			return err
		}
		out.Vars(res)
		return nil
	})
}

// newAuthID：一次认证尝试的 id（插件没自带时用）。只要在本进程内唯一即可——
// 平台拿它当不透明串回传，不解析。
func newAuthID() string { return fmt.Sprintf("auth_%d", time.Now().UnixNano()) }

// Contract 上报给平台的操作契约（注册握手用）。
func (p *Plugin) contract() []Operation {
	out := make([]Operation, len(p.ops))
	for i, e := range p.ops {
		out[i] = e.op
	}
	return out
}

// Register 实现 plugin.Host：把一个操作注册到本插件（NATS 传输）。
//
// 平台的进程内宿主实现同一个接口，于是**同一份插件实现**两边都能挂——
// 差别只有传输，实现本身不认识 NATS。
func (p *Plugin) Register(op contract.Operation, fn plugin.Invoke) {
	// 复用 RegisterOp：panic 兜底、空数组归一都在那里，不该有第二份。
	RegisterOp(p, op, func(ctx Ctx, raw json.RawMessage, out Sink) error { return fn(ctx, raw, out) })
}

func (p *Plugin) find(opID string) *opEntry {
	for i := range p.ops {
		if p.ops[i].op.ID == opID {
			return &p.ops[i]
		}
	}
	if opID == "" && len(p.ops) == 1 {
		return &p.ops[0] // 单操作插件：operation 省略时默认唯一操作
	}
	return nil
}

// invokeBuffered 非流式调用：找操作、绑定入参、缓冲各帧、合并 variables 为输出对象。
func (p *Plugin) invokeBuffered(ctx Ctx, operation string, input json.RawMessage) (map[string]any, error) {
	entry := p.find(operation)
	if entry == nil {
		return nil, fmt.Errorf("unknown operation %q", operation)
	}
	sink := &bufferSink{}
	if err := entry.invoke(ctx, input, sink); err != nil {
		return nil, err
	}
	if sink.vars == nil {
		sink.vars = map[string]any{}
	}
	return sink.vars, nil
}

// Run 阻塞运行：连接平台（统一端点经 /connect-info 发现承载地址）、注册（上报契约）、心跳、分发调用。
func (p *Plugin) Run() error {
	return (&natsTransport{}).run(p)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// 编译期确认：sokel 是 plugin 接口的一种传输实现（NATS）。
// 平台的进程内宿主实现同一组接口——同一份插件实现因此两边都能挂。
var (
	_ plugin.Host           = (*Plugin)(nil)
	_ plugin.CredentialHost = (*Plugin)(nil)
	_ plugin.Ctx            = natsCtx{}
	_ plugin.Sink           = Sink{}
)

// Env：读插件的接入环境变量，名字不带前缀（如 Env("TOKEN") 读 SOKEL_TOKEN）。
//
// 改名过渡期两个前缀都认、新名优先（SOKEL_ → SOKEL_，见 pluginenv）：
// 环境变量是插件与平台之间的契约，线上容器里写的还是老名字，一刀切改名等于
// 所有插件下次重启集体连不上。插件作者一律用它读，别直接 os.Getenv。
func Env(name string) string { return pluginenv.Get(name) }

// EnvOr：同 Env，取不到时用默认值。
func EnvOr(name, def string) string {
	if v := pluginenv.Get(name); v != "" {
		return v
	}
	return def
}

// traceCtxKey 平台追踪上下文在 context 里的键。
type traceCtxKey struct{}

// TraceValue 取平台下发的追踪上下文（run_id / workflow_id / node_id）。
// 非工作流调用（试调用、健康检查）没有这些值，返回空串——**调用方要把空串当
// 「没有重试语义」处理**，而不是当成一个恒定的键（那会把两次独立调用错误地去重）。
func TraceValue(ctx context.Context, key string) string {
	t, _ := ctx.Value(traceCtxKey{}).(map[string]string)
	return t[key]
}

// SetVersion：声明插件版本（注册时自报，实例列表「版本」列显示）。
// 不调用则回退环境变量 SOKEL_VERSION（发布镜像注入最方便），再兜底 "sdk-go"。
func (p *Plugin) SetVersion(v string) { p.version = v }
