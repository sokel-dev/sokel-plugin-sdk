// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// access is everything a replica needs to reach the platform's transport: where to connect, the
// credentials for this access group, and the subject it should listen on.
//
// Per-group credentials, not a shared secret: the broker authorizes each group only for its own
// subjects. Before that, one shared token let any plugin subscribe to sokel.plugin.> and read every
// other plugin's call frames -- which carry decrypted credentials.
type access struct {
	URL     string `json:"url"`
	User    string `json:"user"`
	Pass    string `json:"pass"`
	Subject string `json:"subject"`
	// CA is the broker's certificate authority, in PEM, when it uses one outside the system
	// trust store. It ships with the credentials because it is public information and every
	// replica would otherwise need the same file copied to it by hand -- a broker reachable
	// only by IP cannot get a publicly trusted certificate, so self-signed is the norm there,
	// not an edge case.
	CA string `json:"ca"`
	// InboxPrefix is this group's own reply-subject prefix (_INBOX_G.<group>). The broker only lets
	// this group subscribe under it, so every request this process makes must use it for its
	// replies -- the global _INBOX.> is no longer granted, since it let any group read every other
	// group's replies. Empty from an older platform, which still grants _INBOX.>; keep the default.
	InboxPrefix string `json:"inbox_prefix"`
	// MinProtocol is the oldest wire protocol the platform accepts. Checked before connecting, so an
	// SDK that is too old stops with an upgrade hint instead of failing to register forever.
	MinProtocol int `json:"min_protocol"`
	// Token is only set by enrollment, which exchanges a deployment key for a real access token.
	Token string `json:"-"`
	// AccessToken is the group's access token inside an offline SOKEL_ACCESS bundle (the platform
	// exports it under "token"); it fills cfg.Token so registration authenticates as usual.
	AccessToken string `json:"token"`
}

// errNoTransport marks "the platform says it has no transport", as opposed to a network failure.
// The two lead to different decisions when a replica has been disconnected for a while, so they
// must stay distinguishable (see rediscoverOutcome).
type errNoTransport struct{ detail string }

func (e errNoTransport) Error() string {
	if e.detail != "" {
		return "the platform offers no transport: " + e.detail
	}
	return "the platform offers no transport"
}

func platformURL(endpoint string) (string, error) {
	ep := strings.TrimSpace(endpoint)
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return strings.TrimRight(ep, "/"), nil
	}
	if strings.HasPrefix(ep, "nats://") || strings.HasPrefix(ep, "tls://") {
		// A bare broker address used to be enough, back when one shared secret opened the whole
		// broker. Per-group credentials only exist on the platform, so the endpoint has to be the
		// platform now. Say so plainly -- the alternative symptom is an authorization violation
		// with no hint about what to change.
		return "", fmt.Errorf("SOKEL_ENDPOINT is a broker address (%s), but per-group credentials "+
			"are issued by the platform: point it at the platform URL (https://…) instead", ep)
	}
	return "", fmt.Errorf("invalid endpoint %q: expected a platform URL (https://…)", endpoint)
}

func postJSON(url, auth string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+auth)
	return do(req, url, out)
}

func do(req *http.Request, url string, out any) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusServiceUnavailable {
		// The platform is explicit: it has no transport configured. Not a network problem.
		return errNoTransport{detail: strings.TrimSpace(string(body))}
	}
	if resp.StatusCode != http.StatusOK {
		return httpStatusError{URL: url, Code: resp.StatusCode, Msg: strings.TrimSpace(string(body))}
	}
	return json.Unmarshal(body, out)
}

// discoverAccess exchanges an access token for the transport address and this group's credentials.
func discoverAccess(endpoint, token string) (access, error) {
	base, err := platformURL(endpoint)
	if err != nil {
		return access{}, err
	}
	url := base + "/api/v1/connect-info"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	var info struct {
		Nats     access `json:"nats"`
		Degraded bool   `json:"degraded"`
		Detail   string `json:"detail"`
	}
	if err := do(req, url, &info); err != nil {
		return access{}, err
	}
	if info.Nats.URL == "" {
		return access{}, errNoTransport{}
	}
	if info.Degraded {
		// The platform's own broker connection is down. Keep going anyway: replicas talk to the
		// broker directly, so the platform being disconnected does not stop this one from working.
		log.Printf("[sokel] platform reports a degraded transport: %s", info.Detail)
	}
	return info.Nats, nil
}

// enrollAccess exchanges a deployment-level key for a real access token plus this group's
// credentials, in one call.
//
// Over HTTP rather than the broker: per-group credentials are what enrollment hands out, so asking
// for them over the broker would require already having them. The old sokel.enroll subject worked
// only while one shared secret opened the whole broker.
func enrollAccess(endpoint, key, plugin string) (access, error) {
	base, err := platformURL(endpoint)
	if err != nil {
		return access{}, err
	}
	url := base + "/api/v1/plugins/enroll"
	var out struct {
		Token    string `json:"token"`
		Nats     access `json:"nats"`
		Degraded bool   `json:"degraded"`
		Detail   string `json:"detail"`
	}
	if err := postJSON(url, key, map[string]string{"plugin": plugin}, &out); err != nil {
		return access{}, err
	}
	if out.Token == "" || out.Nats.URL == "" {
		return access{}, errNoTransport{detail: out.Detail}
	}
	a := out.Nats
	a.Token = out.Token
	return a, nil
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
// Keep waiting when the outage is still short (normal jitter), when discovery itself fails over
// the network (the platform is unreachable too -- nowhere better to go), or when discovery returns
// the same address (the broker is down, not moved).
//
// Do NOT keep waiting when the platform answers "no transport": that is an answer, not a failure,
// and it used to be indistinguishable from an unreachable platform -- a replica would then wait
// forever while the platform's NATS was simply misconfigured.
//
// Do NOT keep waiting on a CLOSED connection either, whatever the threshold says. CLOSED means the
// client gave up for good; MaxReconnects(-1) does not bring it back. The usual cause is an
// authorization failure -- this group's broker credentials were rotated, or the group was deleted
// and recreated -- and a restart is exactly the cure, because startup fetches fresh credentials.
// Observed while building per-group auth: a replica that connected before its credentials existed
// logged "connection closed" every 8s forever.
func rediscoverOutcome(status nats.Status, disconnected, minDown time.Duration, discover func() (access, error), currentAddr string) (exit bool, reason string) {
	if status == nats.CLOSED {
		return true, "the broker connection is closed for good (most likely our credentials no longer " +
			"authorize us); exiting so the supervisor restarts us with freshly fetched credentials"
	}
	if disconnected < minDown {
		return false, ""
	}
	fresh, err := discover()
	if err != nil {
		var nt errNoTransport
		if asNoTransport(err, &nt) {
			return true, fmt.Sprintf("the platform reports no transport (%s) after %s down; "+
				"exiting so the supervisor can retry from scratch", nt.detail, disconnected.Round(time.Second))
		}
		return false, ""
	}
	if fresh.URL == "" || fresh.URL == currentAddr {
		return false, ""
	}
	return true, fmt.Sprintf("broker moved: connected to %s but discovery now points at %s (down %s)",
		currentAddr, fresh.URL, disconnected.Round(time.Second))
}

func asNoTransport(err error, out *errNoTransport) bool {
	if e, ok := err.(errNoTransport); ok {
		*out = e
		return true
	}
	return false
}

// httpStatusError is a non-200 answer from the platform; Code lets retry loops tell "wait and try again" from
// answers that waiting cannot change.
type httpStatusError struct {
	URL  string
	Code int
	Msg  string
}

func (e httpStatusError) Error() string { return fmt.Sprintf("%s: HTTP %d %s", e.URL, e.Code, e.Msg) }

// enrollRetryDelay is how long to wait before enrollment attempt attempt+1. A 4xx (plugin not in the catalog, wrong
// deployment key) will not change by itself: back off from 8s, doubling, up to five minutes, so a misconfigured
// replica does not hit the platform every 8 seconds forever (2026-10-01). Network errors and 5xx are the platform
// starting or restarting: keep 8s so the replica comes up as soon as it can.
func enrollRetryDelay(err error, attempt int) time.Duration {
	const base, ceiling = 8 * time.Second, 5 * time.Minute
	var he httpStatusError
	if !errors.As(err, &he) || he.Code < 400 || he.Code >= 500 {
		return base
	}
	d := base
	for i := 0; i < attempt && d < ceiling; i++ {
		d *= 2
	}
	return min(d, ceiling)
}
