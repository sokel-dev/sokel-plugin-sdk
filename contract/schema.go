// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import "fmt"

// FieldSpec：字段声明。由 sokel/field 的 builder 实现——契约层只认这个接口，
// 不依赖 builder 的具体类型（否则 sokel ↔ field 成环）。
type FieldSpec interface{ Field() Field }

// BuildFields 把一组声明交出为契约字段。
func BuildFields(specs []FieldSpec) []Field {
	out := make([]Field, 0, len(specs))
	for _, s := range specs {
		if s == nil {
			continue
		}
		out = append(out, s.Field())
	}
	return out
}

// Meta：操作的元信息（不含入/出参——那是 Inputs/Outputs 的事）。
type Meta struct {
	ID         string
	Label      string
	Desc       string
	TimeoutSec int  // 插件最清楚自己这个操作要跑多久，让它自报，用户不必拖出节点时猜
	Stream     bool // 流式（多帧回复）
	Internal   bool // 内部操作（认证流等）：不上画布，仅平台面板调用
}

// Schema：一个操作的完整声明。**只声明，不含实现**——
// 实现由生成的专用注册函数接住（签名完全具体，零泛型零 any）。
//
// 先定义后实现是刻意的：契约是对外接口，应该能被单独评审、单独导出成 JSON
// 供其他语言 SDK 生成对应类型，而不是从实现代码里反推出来的副产品。
type Schema interface {
	Meta() Meta
	Inputs() []FieldSpec
	Outputs() []FieldSpec
}

// CredentialSchema：本插件凭证契约的声明。
//
// 与操作/事件同一条路子（schema 里声明 → sokel-gen 生成类型 → 注册握手上报）。
// 早先凭证只能靠 main 包里的 struct tag 反射，那条路表达不出 enum 候选值与默认值——
// 它是操作在 codegen 之前的写法，凭证只是没跟着迁过来。
type CredentialSchema interface {
	CredentialFields() []FieldSpec
}

// AuthMeta：凭证的**获取方式**（协作式认证流的契约部分）。
//
// 只有契约，没有实现——Start/Poll/Submit 是函数，留在实现侧由生成的 RegisterAuth 接住，
// 与操作「schema 声明 + OnXxx 接实现」完全同形。
type AuthMeta struct {
	// Kind：认证形态。用 auth.QR() / auth.Input() / auth.OAuth() 构造，别手写字面量。
	Kind AuthKind
	// Steps：插件实现哪几步。**由 Kind 决定**（见 contract/auth），不该手写——
	// 手写就是把「qr 要哪几步」这件事抄第二遍，而抄错的那份没人会发现。
	// 生成的 RegisterAuth 参数表照它长，缺一个就是编译错，而不是启动时才 panic。
	Steps []AuthStep
	// Provider / Scopes：kind=oauth 专用。作用域**由插件声明**，平台不写死——
	// 加一个新的 Google 服务插件时平台一行都不用改。
	Provider string
	Scopes   []string
}

// AuthKind：认证形态。
type AuthKind string

const (
	AuthQR    AuthKind = "qr"    // 二维码（插件出题）
	AuthInput AuthKind = "input" // 用户回填，如短信验证码（插件出题）
	AuthOAuth AuthKind = "oauth" // 第三方同意页（**平台代答**）
)

// AuthStep：认证流的一步。
type AuthStep string

const (
	StepStart  AuthStep = "start"
	StepPoll   AuthStep = "poll"
	StepSubmit AuthStep = "submit"
)

// AuthSchema：声明凭证怎么拿到。挂在凭证声明上（同一个类型多一个方法）而不是另起一个——
// 认证方式是**凭证的属性**，分成两处声明只会让「这条凭证怎么来的」要翻两个地方。
type AuthSchema interface {
	AuthMeta() AuthMeta
}

// AuthOf 取出认证声明。
func AuthOf(s AuthSchema) AuthMeta { return s.AuthMeta() }

// CredentialOf 把凭证声明摊平成契约字段。
func CredentialOf(s CredentialSchema) []Field { return BuildFields(s.CredentialFields()) }

// OperationOf 把声明摊平成线协议的 Operation（注册握手用）。
func OperationOf(s Schema) Operation {
	m := s.Meta()
	return Operation{
		ID: m.ID, Label: m.Label, Desc: m.Desc,
		Stream: m.Stream, Internal: m.Internal, TimeoutSec: m.TimeoutSec,
		Inputs:  BuildFields(s.Inputs()),
		Outputs: BuildFields(s.Outputs()),
	}
}

// Operation 操作声明。Inputs/Outputs 留空时由 Register 的 In/Out 类型反射推导。
// json tag 用于注册握手时上报契约给平台（形态对齐前端 PluginOperation）。
type Operation struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Desc     string `json:"desc,omitempty"`
	Stream   bool   `json:"stream,omitempty"`   // 是否流式（多帧回复）
	Internal bool   `json:"internal,omitempty"` // 内部操作（如 auth_start/submit/poll 认证流）：不出现在画布节点，仅平台面板调用
	// TimeoutSec 该操作的建议超时（秒）。平台取超时的优先级：节点显式配置 > 本字段 > 平台默认 60s。
	// 重活（转写/长文本合成/大文件解析）务必声明，否则 60s 抢跑，而用户拖出节点时并不知道该填多少。
	TimeoutSec int     `json:"timeoutSec,omitempty"`
	Inputs     []Field `json:"inputs"`
	Outputs    []Field `json:"outputs"`
}

// opEntry 已注册操作 + 其类型擦除的调用入口。

// RequireInputs 按操作契约检查入参：必填的不能缺、不能是空串。
//
// 让契约**吃到实处**：执行器不再各写一遍 `if x == "" { return err }`——那份知识
// 与前端的 required 标记是两份手写副本，今天核对出前端漏标 6 个、后端有 32 处散落检查。
// 走同一份声明，两边就不可能分叉。
//
// 只查「有没有」，不查类型：类型归一在调用前统一做过（coerceContractTypes）。
func RequireInputs(op Operation, cfg map[string]any) error {
	for _, f := range op.Inputs {
		if !f.Required {
			continue
		}
		v, ok := cfg[f.Name]
		if !ok || v == nil || v == "" {
			label := f.Label
			if label == "" {
				label = f.Name
			}
			return fmt.Errorf("「%s」缺少必填参数「%s」（%s）", op.Label, label, f.Name)
		}
	}
	return nil
}
