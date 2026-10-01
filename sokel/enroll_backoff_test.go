package sokel

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// A replica asking to enroll a plugin the platform does not have used to retry every 8 seconds forever, and the
// platform's log carried a 404 every 8 seconds (2026-10-01). Answers that waiting cannot change (4xx) back off up
// to five minutes; network errors and 5xx (the platform restarting) keep retrying every 8 seconds.
func TestEnrollRetryDelay(t *testing.T) {
	notFound := fmt.Errorf("enroll: %w", httpStatusError{Code: 404, Msg: "no auto-enrollable plugin x"})
	cases := []struct {
		name    string
		err     error
		attempt int
		want    time.Duration
	}{
		{"network error stays at 8s", errors.New("dial tcp: connection refused"), 10, 8 * time.Second},
		{"5xx stays at 8s", httpStatusError{Code: 502}, 10, 8 * time.Second},
		{"first 404", notFound, 0, 8 * time.Second},
		{"second 404 doubles", notFound, 1, 16 * time.Second},
		{"fourth 404", notFound, 3, 64 * time.Second},
		{"capped at five minutes", notFound, 20, 5 * time.Minute},
		{"401 backs off too", httpStatusError{Code: 401}, 2, 32 * time.Second},
	}
	for _, c := range cases {
		if got := enrollRetryDelay(c.err, c.attempt); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// The error text is unchanged by the typed error (operators grep for it).
func TestHTTPStatusErrorText(t *testing.T) {
	e := httpStatusError{URL: "http://p/api/v1/plugins/enroll", Code: 404, Msg: "not found"}
	if got := e.Error(); got != "http://p/api/v1/plugins/enroll: HTTP 404 not found" {
		t.Errorf("error text changed: %q", got)
	}
}
