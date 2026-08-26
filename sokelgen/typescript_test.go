// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// TS 执行契约必须带 itemType（数组元素类型）——丢在这一层，前端的数量位校验
// （file 列表 vs 单 file，web docs/type-system.md §11）就从源头断了。
// 早先 RenderTS 只出 name/type/required，itemType 在 Go 契约里一直有、到 TS 就被扔掉。
func TestRenderTSKeepsItemType(t *testing.T) {
	ops := []OpIO{{
		OpID:  "send",
		Label: "发送",
		Inputs: []Field{
			{Name: "files", Type: "array", ItemType: "file", Required: true},
			{Name: "note", Type: "string"},
		},
	}}
	got, err := RenderTS("test/pkg", ops)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `itemType: "file"`) {
		t.Errorf("array 参数的 itemType 没进 TS 契约：\n%s", got)
	}
	if strings.Contains(got, `"note", type: "string", required: false, itemType`) {
		t.Errorf("无 itemType 的参数不该带该键：\n%s", got)
	}
	if !strings.Contains(got, "itemType?: string") {
		t.Errorf("KernelParam 接口缺 itemType 字段：\n%s", got)
	}
}
