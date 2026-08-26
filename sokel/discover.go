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

// rediscoverOutcome decides whether a long-disconnected replica should exit and let its
// supervisor restart it onto a freshly discovered broker address.
//
// Why exiting is the mechanism: the NATS client redials the address it was born with, forever
// (MaxReconnects(-1)) -- a broker that moved leaves the replica orphaned for good (observed: a
// 6-day-old container permanently failing after a broker switch). Swapping the live connection
// under the runtime would touch every subscription; all supported deployments run under a
// restart supervisor, so a reasoned exit IS the reconnect.
//
// Keep waiting when: the outage is still short (normal jitter), discovery itself fails (the
// platform is down too -- nowhere better to go), or discovery returns the same address (the
// broker is down, not moved).
func rediscoverOutcome(disconnected, minDown time.Duration, discover func() (string, error), currentAddr string) (exit bool, reason string) {
	if disconnected < minDown {
		return false, ""
	}
	fresh, err := discover()
	if err != nil || fresh == "" || fresh == currentAddr {
		return false, ""
	}
	return true, fmt.Sprintf("broker moved: connected to %s but discovery now points at %s (down %s)", currentAddr, fresh, disconnected.Round(time.Second))
}
