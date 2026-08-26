// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// discoverNATS：统一 https 端点 → 平台 /connect-info 发现当前承载（今天是 NATS）的真实地址。
// 端点形态不绑死承载：未来新 transport 只改发现返回与 SDK 内部，作者的 SOKEL_ENDPOINT 不变。
// 兼容：直填 nats://（或 tls://）时跳过发现直接连（本地开发/离线场景）。
func discoverNATS(endpoint, token string) (string, error) {
	ep := strings.TrimSpace(endpoint)
	if strings.HasPrefix(ep, "nats://") || strings.HasPrefix(ep, "tls://") {
		return ep, nil
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		return "", fmt.Errorf("端点 %q 不合法：应为平台地址（https://…）或 nats://", endpoint)
	}
	url := strings.TrimRight(ep, "/") + "/api/v1/connect-info"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("平台发现失败 %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("平台发现失败 %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info struct {
		Transports map[string]string `json:"transports"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("解析 connect-info: %w", err)
	}
	if u := info.Transports["nats"]; u != "" {
		return u, nil
	}
	return "", fmt.Errorf("平台未提供可用承载（connect-info.transports 为空）")
}
