// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"strings"
	"testing"
)

type fileCreated struct{}

func (fileCreated) EventMeta() EventMeta { return EventMeta{ID: "file_created", Label: "新文件"} }
func (fileCreated) Fields() []FieldSpec {
	return []FieldSpec{specOf(Field{Name: "path", Type: TString, Required: true}), specOf(Field{Name: "size", Type: TNumber})}
}

type fileDeleted struct{}

func (fileDeleted) EventMeta() EventMeta { return EventMeta{ID: "file_deleted"} }
func (fileDeleted) Fields() []FieldSpec {
	return []FieldSpec{specOf(Field{Name: "path", Type: TString, Required: true})}
}

type fieldSpecFn func() Field

func (f fieldSpecFn) Field() Field { return f() }

func specOf(f Field) FieldSpec { return fieldSpecFn(func() Field { return f }) }

func TestEventOf(t *testing.T) {
	e := EventOf(fileCreated{})
	if e.ID != "file_created" || e.Label != "新文件" || len(e.Fields) != 2 {
		t.Fatalf("事件契约: %+v", e)
	}
	// 空字段要是空数组不是 null —— 下游不必防 null
	type empty struct{ EventSchema }
	if got := EventOf(noFields{}); got.Fields == nil {
		t.Errorf("空字段应为空数组: %+v", got)
	}
	_ = empty{}
}

type noFields struct{}

func (noFields) EventMeta() EventMeta { return EventMeta{ID: "x"} }
func (noFields) Fields() []FieldSpec  { return nil }

// 公共字段：必须在**所有**事件里都有且类型一致。
// 不做「取交集」——那样新增一个事件少写了某字段，公共字段就悄悄缩水，存量流跟着断。
func TestValidateCommonFields(t *testing.T) {
	events := []Event{EventOf(fileCreated{}), EventOf(fileDeleted{})}

	got, err := ValidateCommonFields(events, []string{"path"})
	if err != nil || len(got) != 1 || got[0].Name != "path" {
		t.Fatalf("path 在两个事件里都有，应通过: %+v %v", got, err)
	}

	// size 只在 file_created 里有 → 报错，并指明是哪个事件缺
	_, err = ValidateCommonFields(events, []string{"size"})
	if err == nil || !strings.Contains(err.Error(), "file_deleted") {
		t.Errorf("应指明哪个事件缺该字段: %v", err)
	}
	// 与平台保留字撞名
	if _, err := ValidateCommonFields(events, []string{"event"}); err == nil ||
		!strings.Contains(err.Error(), "保留字") {
		t.Errorf("应拦保留字: %v", err)
	}
	// 与事件 id 撞名
	if _, err := ValidateCommonFields(events, []string{"file_created"}); err == nil ||
		!strings.Contains(err.Error(), "事件 id") {
		t.Errorf("应拦与事件 id 撞名: %v", err)
	}
	// 类型不一致
	mixed := []Event{
		{ID: "a", Fields: []Field{{Name: "k", Type: TString}}},
		{ID: "b", Fields: []Field{{Name: "k", Type: TNumber}}},
	}
	if _, err := ValidateCommonFields(mixed, []string{"k"}); err == nil ||
		!strings.Contains(err.Error(), "类型") {
		t.Errorf("类型不一致应报错: %v", err)
	}
}
