// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
	"os"
	"strings"
)

// stableInstanceID：副本的稳定身份（GitLab-runner 式）。
// 此前用 host-pid —— 每次重启 pid 变化即被平台视作全新实例，旧行永远以 offline 留存、越积越多。
// 现按优先级取：
//  1. SOKEL_INSTANCE_ID 环境变量（多副本同机部署时显式指定）；
//  2. 工作目录 .sokel-instance-id.<token指纹> 文件（首次生成 host-<4字节随机> 并落盘，重启复用）。
//     文件名带接入 token 指纹：同目录跑多个插件/多个组各有各的身份——曾共用一个文件导致
//     tg 与 wechat 注册成同名实例、换目录启动又漂移出「幽灵双实例」；
//  3. 落盘失败（只读文件系统等）→ 退回 host-pid（至少可用，但重启会换新身份）。
const instanceIDFile = ".sokel-instance-id"

func stableInstanceID(token string) string {
	if v := strings.TrimSpace(pluginenv.Get("INSTANCE_ID")); v != "" {
		return v
	}
	file := instanceIDFile
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		file = instanceIDFile + "." + hex.EncodeToString(sum[:4])
	}
	if b, err := os.ReadFile(file); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	host, _ := os.Hostname()
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	id := fmt.Sprintf("%s-%s", host, hex.EncodeToString(buf))
	if err := os.WriteFile(file, []byte(id+"\n"), 0o644); err != nil {
		return fmt.Sprintf("%s-%d", host, os.Getpid()) // 落盘失败兜底
	}
	return id
}
