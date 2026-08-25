// Package plugin：插件实现与**传输**之间的接缝。
//
// 一个插件的实现（schema 声明 + handler）应当只有一份，被两种传输承接：
//
//	server            进程内直调        —— 平台自己就是宿主
//	plugin-builtin    sokel + NATS       —— 单独部署到别的机器
//
// 差别只有传输。所以实现不该认识 *sokel.Plugin / sokel.Ctx 这些 NATS 那侧的类型，
// 只认下面这几个接口；由谁来实现它们，就决定了这次调用走哪条路。
//
// 这几个接口刻意小：真实插件用到的运行时能力就这么多（凭证、取文件、存文件、报状态）。
// 接口一大，两种传输就都难实现，而且大出来的部分多半只有一侧用得上。
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// Host：插件把自己的操作注册到宿主。sokel.Plugin 与平台的进程内宿主各实现一份。
type Host interface {
	Register(op contract.Operation, fn Invoke)
}

// CredentialHost：能接住凭证契约的宿主（SDK 的 *sokel.Plugin 实现它）。
//
// 单独一个小接口而不是并进 Host：进程内宿主没有「上报凭证契约」这回事，
// 而生成的代码要能同时挂两边。生成物也因此不必 import go-sdk——
// plugin-core 反过来依赖 SDK 会成环（内核自带的契约声明就在 plugin-core 里）。
type CredentialHost interface {
	SetCredentialContract(fields []contract.Field)
}

// DocHost：能接住「使用说明」的宿主。
//
// 与凭证契约同一个模式（小接口、可选实现）：插件把自己的说明书交出来，
// 平台原样收下并在界面上渲染。这样「这个 key 去哪申请」「有什么坑」
// 跟着插件代码走，而不是散在平台前端的硬编码表、凭证字段的 placeholder
// 和作者的记忆里——那三处正是它现在待的地方。
type DocHost interface {
	SetDoc(markdown, url string)
}

// DeclareDoc：宿主支持就交出说明书，不支持就静默跳过（进程内/远端两边同一份代码）。
//
// markdown 与 url 给一个即可：自己写一段，或指向已有的文档站——
// 抄一份进来的那份迟早与站上的不一致。
func DeclareDoc(h Host, markdown, url string) {
	if dh, ok := h.(DocHost); ok {
		dh.SetDoc(markdown, url)
	}
}

// CapabilityHost：能接住「可选能力自报」的宿主。
//
// 与 DocHost 同一个模式（小接口、可选实现）。它解决的是「操作有没有」之外的那一半：
// 同一个操作，两家实现做到的程度可以差很远——存储插件都有 keyword_query，
// 但一家是带中文分词的 BM25、另一家是相似度近似。不报的话平台只能静默忽略，
// 用户配了字段加权却毫无体现，那比「不支持」更坏。
type CapabilityHost interface {
	SetCapabilities(caps map[string]bool)
}

// DeclareCapabilities：宿主支持就收下能力位，不支持就静默跳过（进程内/远端两边同一份代码）。
func DeclareCapabilities(h Host, caps map[string]bool) {
	if ch, ok := h.(CapabilityHost); ok {
		ch.SetCapabilities(caps)
	}
}

// Invoke：一次调用。raw 是入参 JSON（由生成的代码解到具体类型），产出走 Sink。
type Invoke func(ctx Ctx, raw json.RawMessage, out Sink) error

// Ctx：一次调用能用到的运行时能力。
//
// 各传输的实现方式不同，但语义一致——比如 Upload：NATS 那侧分块传回平台，
// 进程内那侧直接落存储层。handler 不必知道自己跑在哪。
type Ctx interface {
	context.Context
	// Credential 本次调用的凭证字段（平台解析后下发；无凭证返回 nil）。
	Credential() map[string]string
	// Upload 把字节存进平台文件层，得到一个文件引用（可直接作为出参交给下游）。
	Upload(name, mime string, data []byte) (*File, error)
	// UploadReader 同上，但**边读边传**：内存占用是一个块（1MB），与文件大小无关。
	//
	// 几百 MB 以上的东西（NAS 上的视频、压缩包）一律走这条——Upload 要求先把整个文件
	// 读进内存，那不是"慢一点"，是插件进程直接被撑爆。
	UploadReader(name, mime string, r io.Reader) (*File, error)
	// Fetch 取回文件字节。File.Blob 是它的方法形式，插件里一般写 f.Blob(ctx)。
	Fetch(f *File) ([]byte, error)
}

// Sink：产出。多次调用 = 多帧（流式）；非流式由传输侧缓冲合并。
type Sink interface {
	// Vars 类型化输出变量（进下游节点），按 sokel tag 落为契约名。
	Vars(v any)
	// Text 人类可读文本（展示 / tracing，不进下游变量）。
	Text(s string)
	// JSON 结构化展示。
	JSON(v any)
}

// File：平台文件引用。**只是数据**——取字节要通过 Ctx，因为那依赖传输。
//
// json tag 与平台的文件值形态对齐，故可直接作为出参字段交出去。
type File struct {
	ID   string `json:"id,omitempty"`   // 平台文件 id（f_…）
	URL  string `json:"url,omitempty"`  // 平台下载路径（/api/v1/files/<id>）
	Name string `json:"name,omitempty"` // 文件名
	Mime string `json:"mime,omitempty"` // MIME 类型
	// Size 不能 omitempty：0 字节是一个**要能被看见**的值。此前 size=0 时字段整个
	// 消失，下游想写「file.size > 0」的空文件闸都无从引用（实测：空下载的输出里
	// 没有 size 键，用户对着有 size 的成功样例照抄条件，永远判不中）。
	Size int64  `json:"size"`           // 字节数
	Data []byte `json:"data,omitempty"` // 内联字节兜底（小文件/测试；正常路径为空）
}

// FileRef 实现 contract.FileRef：让契约包认出这是文件字段。
func (f *File) FileRef() {}

// Blob 取文件字节：优先内联 Data，否则请传输侧去平台文件层拉。
// 写成方法只是顺手——真正干活的是 ctx，因为取字节依赖传输。
func (f *File) Blob(ctx Ctx) ([]byte, error) {
	if f == nil {
		return nil, errors.New("nil file")
	}
	if len(f.Data) > 0 {
		return f.Data, nil
	}
	return ctx.Fetch(f)
}

// —— 事件源 ——
//
// 事件与操作一样：实现只声明「有哪些事件、怎么推」，由谁承接是传输的事。

// EventHost：声明事件契约的宿主。
type EventHost interface {
	// DeclareEvent 声明一种事件。
	DeclareEvent(e contract.Event)
	// DeclareEventsCommon 声明「所有事件共有」的字段（平台会平铺到触发输入顶层）。
	DeclareEventsCommon(fields []contract.Field, names []string)
}

// SourceCtx：常驻事件源能用到的运行时能力。比 Ctx 多两样：推事件、改凭证。
type SourceCtx interface {
	Ctx
	// Trigger 推一条事件。eventID 用于平台侧去重（同一条外部消息重复推只触发一次）。
	//
	// payload 收 any 而不是 map：传输侧按 sokel tag 把 struct 展成契约名（与 Sink.Vars 同一套），
	// 类型安全由生成的 TriggerXxx 在外层保证——这里再收窄一次只会让生成物多一道转换。
	Trigger(event, eventID string, payload any) error
	// UpdateCredential 回写凭证字段（如刷新到的 token）。
	UpdateCredential(patch map[string]string) error
	// ReportStatus 上报本源的状态（随心跳带回平台，凭证列表上可见）。
	ReportStatus(status, msg string)
}
