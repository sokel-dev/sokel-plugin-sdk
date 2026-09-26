// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// Errors used to travel as a bare string both ways: callers matched English text, and a plugin had no
// way to tell the platform "this is a credential problem" or "retrying may help" (F-328).
func TestErrorCodesTravelOnTheWire(t *testing.T) {
	cases := []struct {
		err       error
		code      string
		retryable bool
	}{
		{Retryable(errors.New("upstream 429")), "retryable", true},
		{CredentialInvalid(errors.New("401 from upstream")), "credential_invalid", false},
		{InvalidInput(errors.New("chat_id must be numeric")), "invalid_input", false},
		{fmt.Errorf("sending: %w", Retryable(errors.New("timeout"))), "retryable", true}, // wrapped still counts
		{errors.New("plain failure"), "", false},
	}
	for _, c := range cases {
		var got struct {
			Error     string `json:"error"`
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		}
		if err := json.Unmarshal(errorReply(c.err), &got); err != nil {
			t.Fatal(err)
		}
		if got.Error != c.err.Error() || got.Code != c.code || got.Retryable != c.retryable {
			t.Errorf("%v → %+v, want code=%q retryable=%v", c.err, got, c.code, c.retryable)
		}
		fr := errorFrame(c.err)
		if fr.Kind != "error" || string(fr.Code) != c.code || fr.Retryable != c.retryable {
			t.Errorf("stream error frame for %v = %+v", c.err, fr)
		}
	}
}

// Callers can tell the SDK's own failures apart without matching text.
func TestSDKErrorsAreComparable(t *testing.T) {
	if !errors.Is(errNoTransport{detail: "x"}, ErrNoTransport) {
		t.Error("errors.Is(err, ErrNoTransport) must hold for the platform's 'no transport' answer")
	}
	var e *Error
	if !errors.As(fmt.Errorf("x: %w", CredentialInvalid(errors.New("401"))), &e) || e.Code != CodeCredentialInvalid {
		t.Error("errors.As must reach a *sokel.Error through wrapping")
	}
}
