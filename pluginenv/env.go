// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package pluginenv：插件侧环境变量的统一读法。
//
// 只做一件事——把 SOKEL_ 前缀收在一处，别让每个插件各写各的字面量。
//
// 这里**没有**其它前缀的兼容层：认第二个前缀省下的是一次重新部署，
// 换来的是一个没人敢摘的历史包袱——不划算。
package pluginenv

import (
	"os"
	"strings"
)

const prefix = "SOKEL_"

// Get：读 SOKEL_<name>。name 不带前缀，如 Get("TOKEN") / Get("NATS_TOKEN")。
func Get(name string) string { return strings.TrimSpace(os.Getenv(prefix + name)) }
