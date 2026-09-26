// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 脚手架里的 SDK 版本必须跟着发布走。曾经写死 ^0.3.0 / >=0.3：npm 在 0.x 上 caret 锁 minor，
// 新插件 npm install 只会装到 0.3.x，而那一版不认平台下发的 inbox_prefix，在缺省配置的平台上
// 从第一天起就注册不上（F-304）。事实源是随 tag 一起改的 sdk-node/package.json。
func TestScaffoldPinsTheReleasedSDK(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "sdk-node", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(pkg.Version, ".")
	if len(parts) != 3 {
		t.Fatalf("sdk-node/package.json 的版本不是 x.y.z：%q", pkg.Version)
	}

	var ts struct{ Dependencies map[string]string }
	if err := json.Unmarshal([]byte(scaffoldTS("demo")["package.json"]), &ts); err != nil {
		t.Fatal(err)
	}
	if got, want := ts.Dependencies["@sokel-dev/plugin-sdk"], "^"+pkg.Version; got != want {
		t.Errorf("TS 脚手架锁的 SDK 是 %q，当前发布是 %q", got, want)
	}

	req := scaffoldPython("demo")["requirements.txt"]
	if !strings.Contains(req, ">="+pkg.Version) {
		t.Errorf("Python 脚手架的下界不是当前发布 %s：%q", pkg.Version, req)
	}
	if !strings.Contains(req, "<"+parts[0]+".") {
		t.Errorf("Python 脚手架没有上界（1.0 前每个 minor 都可能破坏协议）：%q", req)
	}
}
