// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

// A replica whose reply inbox the broker refuses (SDK older than the platform's per-group inboxes, or a
// connection from before the platform restarted) sees every request of its own time out. The timeout must
// carry the violation and what to do, not just "nats: timeout" (2026-09-29: a TTS plugin's audio upload
// failed that way for a day; the violation sat in the replica's log, the timeout in the platform's error).
func TestTimeoutCarriesThePermissionViolation(t *testing.T) {
	violation := `nats: permissions violation: Permissions Violation for Subscription to "_INBOX.abc"`
	rt := natsFiles{token: "skp_x",
		req:       func(string, []byte, time.Duration) (*nats.Msg, error) { return nil, nats.ErrTimeout },
		violation: func() string { return violation },
	}
	_, err := natsCtx{Context: context.Background(), rt: rt}.Upload("a.wav", "audio/wav", []byte("bytes"))
	if err == nil {
		t.Fatal("a timed-out upload must fail")
	}
	if !errors.Is(err, nats.ErrTimeout) {
		t.Errorf("the timeout must stay recognisable (errors.Is): %v", err)
	}
	for _, want := range []string{"_INBOX.abc", "rebuild the plugin with the current SDK", "restart the replica"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should say %q — got: %v", want, err)
		}
	}
}

func TestTimeoutWithoutViolationStaysPlain(t *testing.T) {
	rt := natsFiles{token: "skp_x",
		req:       func(string, []byte, time.Duration) (*nats.Msg, error) { return nil, nats.ErrTimeout },
		violation: func() string { return "" },
	}
	_, err := natsCtx{Context: context.Background(), rt: rt}.Upload("a.wav", "audio/wav", []byte("bytes"))
	if err == nil || strings.Contains(err.Error(), "rebuild") {
		t.Errorf("without a violation the timeout must not blame the SDK: %v", err)
	}
	if got := explainTimeout(errors.New("other"), "x"); got.Error() != "other" {
		t.Errorf("only timeouts are explained, got %v", got)
	}
}
