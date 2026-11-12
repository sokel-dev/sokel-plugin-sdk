// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package payload transfers large, ephemeral plugin messages over bounded NATS chunks.
// The broker's per-group subject permissions apply to every chunk. Nothing is persisted.
package payload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
)

const (
	Protocol       = 1
	MaxBytes       = 512 << 20
	MaxChunkBytes  = 1 << 20
	DefaultTimeout = 120 * time.Second
)

// RequestFunc must use the caller's authorized inbox prefix. Returning only the body keeps the
// package compatible with both nats.Conn and the platform's custom reply multiplexer.
type RequestFunc func(context.Context, string, []byte) ([]byte, error)

type reference struct {
	Version    int    `json:"version"`
	Subject    string `json:"subject"`
	Size       int    `json:"size"`
	SHA256     string `json:"sha256"`
	ChunkBytes int    `json:"chunk_bytes"`
}

type envelope struct {
	Ref reference `json:"__sokel_payload"`
}

// Prefix derives the payload namespace from a normal operation subject. Group IDs occupy the
// final token; callers must not pass the additional .large transport suffix.
func Prefix(operationSubject string) (string, error) {
	parts := strings.Split(operationSubject, ".")
	if len(parts) < 4 || parts[0] != "sokel" || parts[1] != "plugin" {
		return "", errors.New("invalid plugin operation subject")
	}
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, ".*> \t\r\n") {
			return "", errors.New("invalid plugin operation subject")
		}
	}
	return "sokel.payload." + parts[len(parts)-1] + ".", nil
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validPrefix(prefix string) bool {
	p := strings.Split(prefix, ".")
	return len(p) == 4 && p[0] == "sokel" && p[1] == "payload" && validToken(p[2]) && p[3] == ""
}

// IsRef recognizes only the reserved outer envelope, never a business object that also has other
// fields. Invalid references still return true, so they are rejected instead of reaching a handler.
func IsRef(body []byte) bool {
	var outer map[string]json.RawMessage
	if json.Unmarshal(body, &outer) != nil || len(outer) != 1 {
		return false
	}
	_, ok := outer["__sokel_payload"]
	return ok
}

func boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}

// InlineLimit reserves room for NATS headers, notably the replica identity.
func InlineLimit(nc *nats.Conn) int { return int(nc.MaxPayload()) - 1024 }

// Transfer owns a temporary subscription and the original bytes until consumption or cancellation.
// Close must be deferred by the sender, including when publishing the reference fails.
type Transfer struct {
	mu     sync.Mutex
	sub    *nats.Subscription
	data   []byte
	next   int
	err    error
	done   chan struct{}
	cancel context.CancelFunc
}

func (t *Transfer) finishLocked(err error) {
	select {
	case <-t.done:
		return
	default:
	}
	t.err = err
	t.data = nil
	if t.sub != nil {
		_ = t.sub.Unsubscribe()
	}
	close(t.done)
	t.cancel()
}

// Close releases the producer immediately. It is safe to call more than once.
func (t *Transfer) Close() {
	if t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.finishLocked(context.Canceled)
	}
}

// Done closes after the last chunk is published, or after cancellation, expiry or a transfer error.
func (t *Transfer) Done() <-chan struct{} { return t.done }

// Wait waits for release and reports whether all bytes were consumed.
func (t *Transfer) Wait() error { <-t.done; t.mu.Lock(); defer t.mu.Unlock(); return t.err }

// Prepare returns an inline body when it fits, otherwise a small reference plus its producer.
// The bytes passed in must not be modified until the transfer completes.
func Prepare(ctx context.Context, nc *nats.Conn, prefix string, data []byte) ([]byte, *Transfer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(data) > MaxBytes {
		return nil, nil, fmt.Errorf("payload_too_large: payload exceeds %d byte limit", MaxBytes)
	}
	limit := InlineLimit(nc)
	if limit < 1 {
		return nil, nil, errors.New("broker payload limit is too small for chunk transfer")
	}
	if len(data) <= limit {
		return data, nil, nil
	}
	if !validPrefix(prefix) {
		return nil, nil, errors.New("invalid payload group prefix")
	}
	ctx, cancel := boundedContext(ctx)
	t := &Transfer{data: data, done: make(chan struct{}), cancel: cancel}
	chunkBytes := min(MaxChunkBytes, limit)
	subject := prefix + nuid.Next()
	t.mu.Lock()
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		t.mu.Lock()
		defer t.mu.Unlock()
		select {
		case <-t.done:
			return
		default:
		}
		if err := ctx.Err(); err != nil {
			t.finishLocked(err)
			return
		}
		var req struct {
			Offset *int `json:"offset"`
			Cancel bool `json:"cancel"`
		}
		dec := json.NewDecoder(bytes.NewReader(m.Data))
		dec.DisallowUnknownFields()
		err := dec.Decode(&req)
		if err == nil && dec.Decode(new(any)) != io.EOF {
			err = errors.New("invalid payload chunk request")
		}
		if err == nil && req.Cancel && req.Offset == nil {
			_ = m.Respond(nil)
			t.finishLocked(context.Canceled)
			return
		}
		if err != nil || req.Offset == nil || req.Cancel || *req.Offset != t.next || m.Reply == "" {
			bad := nats.NewMsg(m.Reply)
			bad.Header.Set("Status", "400")
			bad.Header.Set("Description", "invalid payload chunk request")
			_ = m.RespondMsg(bad)
			t.finishLocked(errors.New("invalid payload chunk request"))
			return
		}
		end := min(t.next+chunkBytes, len(t.data))
		if err := m.Respond(t.data[t.next:end]); err != nil {
			t.finishLocked(err)
			return
		}
		t.next = end
		if end == len(t.data) {
			t.finishLocked(nil)
		}
	})
	t.sub = sub
	t.mu.Unlock()
	if err != nil {
		t.Close()
		return nil, nil, err
	}
	if err := nc.FlushWithContext(ctx); err != nil {
		t.Close()
		return nil, nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			t.finishLocked(ctx.Err())
			t.mu.Unlock()
		case <-t.done:
		}
	}()
	hash := sha256.Sum256(data)
	body, err := json.Marshal(envelope{Ref: reference{Protocol, subject, len(data), hex.EncodeToString(hash[:]), chunkBytes}})
	if err != nil {
		t.Close()
		return nil, nil, err
	}
	return body, t, nil
}

// Fetch validates and resolves a reference on an explicitly negotiated payload path. Each chunk is
// requested only after its predecessor is verified, keeping broker buffers bounded.
func Fetch(ctx context.Context, body []byte, prefix string, request RequestFunc) (out []byte, err error) {
	if !IsRef(body) {
		return nil, errors.New("invalid payload reference")
	}
	var env envelope
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, errors.New("invalid payload reference")
	}
	r := env.Ref
	if !validPrefix(prefix) || !strings.HasPrefix(r.Subject, prefix) || !validToken(strings.TrimPrefix(r.Subject, prefix)) {
		return nil, errors.New("payload subject is outside the current group")
	}
	if r.Version != Protocol || r.Size < 0 || r.Size > MaxBytes || r.ChunkBytes < 1 || r.ChunkBytes > MaxChunkBytes {
		return nil, errors.New("invalid payload version, size or chunk size")
	}
	hash, decodeErr := hex.DecodeString(r.SHA256)
	if decodeErr != nil || len(hash) != sha256.Size || r.SHA256 != strings.ToLower(r.SHA256) {
		return nil, errors.New("invalid payload checksum")
	}
	if request == nil {
		return nil, errors.New("payload request function is required")
	}
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	defer func() {
		if err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			defer cleanupCancel()
			_, _ = request(cleanupCtx, r.Subject, []byte(`{"cancel":true}`))
		}
	}()
	// Grow as chunks arrive, rather than allocating an advertised half-gigabyte before a peer has
	// provided any data. The final length is bounded and each chunk length is checked first.
	for len(out) < r.Size {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, _ := json.Marshal(struct {
			Offset int `json:"offset"`
		}{len(out)})
		chunk, err := request(ctx, r.Subject, req)
		if err != nil {
			return nil, fmt.Errorf("fetching payload chunk: %w", err)
		}
		if len(chunk) != min(r.ChunkBytes, r.Size-len(out)) {
			return nil, errors.New("payload chunk length mismatch")
		}
		out = append(out, chunk...)
	}
	got := sha256.Sum256(out)
	if !bytes.Equal(got[:], hash) {
		return nil, errors.New("payload checksum mismatch")
	}
	return out, nil
}
