// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// 编进二进制的参考声明必须是**能用的**：`sokel-gen example` 印出来的东西，
// 照抄下来要能过解析。印一份自己都读不了的声明，比不印更糟——
// 照抄的人（或 AI）会以为是自己写错了。
func TestExampleManifestParses(t *testing.T) {
	m, err := sokelgen.ParseManifest([]byte(ExampleManifest), false)
	if err != nil {
		t.Fatalf("内嵌的参考声明解析失败: %v", err)
	}
	if len(m.Operations) == 0 || len(m.Events) == 0 || m.Credential == nil {
		t.Fatalf("参考声明该覆盖操作/事件/凭证三样，实际 ops=%d events=%d cred=%v",
			len(m.Operations), len(m.Events), m.Credential != nil)
	}
}

func TestEmbeddedDocsAreUsable(t *testing.T) {
	if !strings.Contains(ManifestDoc, "## Field") {
		t.Error("内嵌的写法说明里没有 Field 一节——多半嵌错文件了")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(Schema), &doc); err != nil {
		t.Fatalf("内嵌的 schema 不是合法 JSON: %v", err)
	}
	if doc["$id"] == nil {
		t.Error("schema 缺 $id")
	}
	for name, src := range map[string]string{"python": ExamplePython, "node": ExampleNode} {
		if !strings.Contains(src, "kitchen") {
			t.Errorf("内嵌的 %s 实现看着不对（没提到 kitchen-sink）", name)
		}
	}
}
