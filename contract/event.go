// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import "fmt"

// 事件契约的声明式表达。
//
// 此前事件只能命令式声明（DeclareEvent[T] + 反射推导 payload），于是三个事件源插件
// （synology / telegram-bot / wechat-claw）没法迁到声明式——**它们迁不了，不是没迁**。
// 补上之后，操作与事件用同一套声明手法：类型上写方法，生成器读它。

// Event 一种事件及其 payload 契约（注册握手时上报给平台）。
type Event struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Desc   string  `json:"desc,omitempty"`
	Fields []Field `json:"fields"`
}

// EventMeta 事件的身份。与 Meta 之于操作同位。
type EventMeta struct {
	ID    string
	Label string
	Desc  string
}

// EventSchema：一个事件的声明。与 Schema（操作）同构——
// 方法名写错即编译失败，而不是等生成期才发现。
type EventSchema interface {
	EventMeta() EventMeta
	Fields() []FieldSpec
}

// EventOf 由声明产出事件契约。
func EventOf(e EventSchema) Event {
	m := e.EventMeta()
	fields := BuildFields(e.Fields())
	if fields == nil {
		fields = []Field{} // 空数组而非 null：下游（平台/前端）不必防 null
	}
	return Event{ID: m.ID, Label: m.Label, Desc: m.Desc, Fields: fields}
}

// CommonFieldsSchema：可选声明「所有事件共有的字段」。
//
// 平台触发时把这些字段从 payload 平铺到输入顶层（{{节点.chat_id}}），各事件分支共享
// 同一变量。**必须显式声明**而不是从各事件里推断交集：推断的话，新增一个事件少写了
// 某字段，公共字段就悄悄缩水，存量工作流跟着断——而那时没人会想到是这里。
type CommonFieldsSchema interface {
	CommonFields() []string
}

// 保留字：平台在触发输入顶层自己要用的键，公共字段不能与之撞名。
var eventReserved = map[string]bool{
	"_event": true, "event": true, "input": true, "credential_id": true,
}

// ValidateCommonFields 校验公共字段：每个都必须在**所有**事件里存在且类型一致。
//
// fail fast 而不是静默取交集——理由同 CommonFieldsSchema 的注释。
func ValidateCommonFields(events []Event, names []string) ([]Field, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("声明了公共字段但没有任何事件")
	}
	ids := map[string]bool{}
	for _, e := range events {
		ids[e.ID] = true
	}
	out := make([]Field, 0, len(names))
	for _, name := range names {
		if eventReserved[name] {
			return nil, fmt.Errorf("公共字段「%s」与平台保留字冲突", name)
		}
		if ids[name] {
			return nil, fmt.Errorf("公共字段「%s」与事件 id 冲突", name)
		}
		var ref *Field
		for _, e := range events {
			f := findField(e.Fields, name)
			if f == nil {
				return nil, fmt.Errorf("公共字段「%s」在事件「%s」里没有声明", name, e.ID)
			}
			if ref == nil {
				ref = f
				continue
			}
			if f.Type != ref.Type {
				return nil, fmt.Errorf("公共字段「%s」在事件「%s」里的类型是 %s，与其它事件的 %s 不一致",
					name, e.ID, f.Type, ref.Type)
			}
		}
		out = append(out, *ref)
	}
	return out, nil
}

func findField(fs []Field, name string) *Field {
	for i := range fs {
		if fs[i].Name == name {
			return &fs[i]
		}
	}
	return nil
}
