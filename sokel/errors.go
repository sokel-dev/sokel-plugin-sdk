// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"encoding/json"
	"errors"
)

// ErrorCode classifies a handler error for the platform. It travels as `code` next to the message in
// the reply (and in a streaming `error` frame), so the platform can act on it without reading the text.
type ErrorCode string

const (
	// CodeRetryable: a transient failure (rate limit, upstream 5xx, timeout) — trying again may work.
	CodeRetryable ErrorCode = "retryable"
	// CodeCredentialInvalid: the credential was rejected upstream (revoked, expired, wrong key).
	// Retrying will not help; someone has to fix the credential.
	CodeCredentialInvalid ErrorCode = "credential_invalid"
	// CodeInvalidInput: the input is wrong for this operation. Retrying the same input will not help.
	CodeInvalidInput ErrorCode = "invalid_input"
)

// Error is a handler error with a code. Build one with Retryable, CredentialInvalid or InvalidInput;
// any error that wraps one (fmt.Errorf("…: %w", err)) still carries its code.
type Error struct {
	Code ErrorCode
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Retryable marks err as transient: the platform may retry the call.
func Retryable(err error) error { return &Error{Code: CodeRetryable, Err: err} }

// CredentialInvalid marks err as "the credential was rejected".
func CredentialInvalid(err error) error { return &Error{Code: CodeCredentialInvalid, Err: err} }

// InvalidInput marks err as "the input is wrong for this operation".
func InvalidInput(err error) error { return &Error{Code: CodeInvalidInput, Err: err} }

// ErrNoTransport: the platform answered that it offers no transport (as opposed to a network failure).
var ErrNoTransport = errors.New("sokel: the platform offers no transport")

// Is lets errors.Is(err, ErrNoTransport) match the detailed form discovery returns.
func (e errNoTransport) Is(target error) bool { return target == ErrNoTransport }

func codeOf(err error) (ErrorCode, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Code == CodeRetryable
	}
	return "", false
}

// errorReply is the non-streaming reply for a failed call: {"error", "code"?, "retryable"?}.
func errorReply(err error) []byte {
	code, retry := codeOf(err)
	b, _ := json.Marshal(struct {
		Error     string    `json:"error"`
		Code      ErrorCode `json:"code,omitempty"`
		Retryable bool      `json:"retryable,omitempty"`
	}{err.Error(), code, retry})
	return b
}

// errorFrame is the streaming counterpart: an `error` frame carrying the same code.
func errorFrame(err error) frame {
	code, retry := codeOf(err)
	return frame{Kind: "error", Text: err.Error(), Code: code, Retryable: retry}
}
