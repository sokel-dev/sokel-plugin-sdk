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

// discoverNATS turns a single https endpoint into the real address of the current transport (NATS
// today) via the platform's /connect-info.
//
// The endpoint form does not name a transport: adding one later changes what discovery returns and
// the SDK internals, while the author's SOKEL_ENDPOINT stays as it is. A literal nats:// (or tls://)
// skips discovery and connects directly, for local development and offline setups.
func discoverNATS(endpoint, token string) (string, error) {
	ep := strings.TrimSpace(endpoint)
	if strings.HasPrefix(ep, "nats://") || strings.HasPrefix(ep, "tls://") {
		return ep, nil
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		return "", fmt.Errorf("invalid endpoint %q: expected a platform URL (https://…) or nats://", endpoint)
	}
	url := strings.TrimRight(ep, "/") + "/api/v1/connect-info"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discovery failed at %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery failed at %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info struct {
		Transports map[string]string `json:"transports"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("parsing connect-info: %w", err)
	}
	if u := info.Transports["nats"]; u != "" {
		return u, nil
	}
	return "", fmt.Errorf("the platform offers no transport (connect-info.transports is empty)")
}
