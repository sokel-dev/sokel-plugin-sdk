package payload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testReference(data []byte) []byte {
	hash := sha256.Sum256(data)
	b, _ := json.Marshal(envelope{Ref: reference{Protocol, "sokel.payload.grp_test.nonce", len(data), hex.EncodeToString(hash[:]), 4}})
	return b
}

func TestPrefixAndReferenceRecognition(t *testing.T) {
	for _, subject := range []string{"sokel.plugin.it.grp_test", "sokel.plugin.sokel/sandbox.grp_test"} {
		prefix, err := Prefix(subject)
		if err != nil || prefix != "sokel.payload.grp_test." {
			t.Fatalf("%s: %s %v", subject, prefix, err)
		}
	}
	for _, subject := range []string{"", "sokel.plugin.g", "other.plugin.name.g", "sokel.plugin.*.g", "sokel.plugin.na me.g", "sokel.plugin..g"} {
		if _, err := Prefix(subject); err == nil {
			t.Errorf("accepted bad subject %q", subject)
		}
	}
	for _, body := range []string{`null`, `[]`, `broken`, `{}`, `{"__sokel_payload":{},"business":true}`} {
		if IsRef([]byte(body)) {
			t.Errorf("mistook business body for envelope: %s", body)
		}
		out, err := Fetch(context.Background(), []byte(body), "", nil)
		if err == nil || out != nil {
			t.Fatalf("invalid reference accepted: %q %v", out, err)
		}
	}
	if !IsRef([]byte(`{"__sokel_payload":null}`)) {
		t.Fatal("invalid reserved envelope must be rejected by Fetch")
	}
}

func TestFetchValidatesBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"cross_group", func(r map[string]any) { r["subject"] = "sokel.payload.other.nonce" }},
		{"wildcard", func(r map[string]any) { r["subject"] = "sokel.payload.grp_test.*" }},
		{"extra_subject_token", func(r map[string]any) { r["subject"] = "sokel.payload.grp_test.nonce.extra" }},
		{"version", func(r map[string]any) { r["version"] = 2 }},
		{"negative_size", func(r map[string]any) { r["size"] = -1 }},
		{"oversize", func(r map[string]any) { r["size"] = MaxBytes + 1 }},
		{"fractional_size", func(r map[string]any) { r["size"] = 1.5 }},
		{"empty_chunk", func(r map[string]any) { r["chunk_bytes"] = 0 }},
		{"oversize_chunk", func(r map[string]any) { r["chunk_bytes"] = MaxChunkBytes + 1 }},
		{"bad_hash", func(r map[string]any) { r["sha256"] = "invalid" }},
		{"uppercase_hash", func(r map[string]any) { r["sha256"] = strings.Repeat("AB", 32) }},
		{"unknown_field", func(r map[string]any) { r["data"] = "no" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env map[string]map[string]any
			_ = json.Unmarshal(testReference([]byte("hello")), &env)
			tc.change(env["__sokel_payload"])
			body, _ := json.Marshal(env)
			calls := 0
			_, err := Fetch(context.Background(), body, "sokel.payload.grp_test.", func(context.Context, string, []byte) ([]byte, error) { calls++; return nil, nil })
			if err == nil || calls != 0 {
				t.Fatalf("invalid reference contacted broker: calls=%d err=%v", calls, err)
			}
		})
	}
	if _, err := Fetch(context.Background(), testReference(nil), "bad", nil); err == nil {
		t.Fatal("accepted invalid prefix")
	}
	if _, err := Fetch(context.Background(), testReference(nil), "sokel.payload.grp_test.", nil); err == nil {
		t.Fatal("accepted nil requester")
	}
}

func TestFetchChecksChunksChecksumAndCancellation(t *testing.T) {
	data := []byte("hello, world")
	for _, mode := range []string{"success", "truncated", "extra", "hash", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests, cancels := 0, 0
			got, err := Fetch(ctx, testReference(data), "sokel.payload.grp_test.", func(reqCtx context.Context, subject string, body []byte) ([]byte, error) {
				if bytes.Contains(body, []byte(`"cancel":true`)) {
					cancels++
					if reqCtx.Err() != nil {
						t.Fatal("cleanup inherited canceled context")
					}
					if deadline, ok := reqCtx.Deadline(); !ok || time.Until(deadline) > time.Second {
						t.Fatal("cleanup has no bounded deadline")
					}
					return nil, nil
				}
				var req struct {
					Offset int `json:"offset"`
				}
				_ = json.Unmarshal(body, &req)
				if req.Offset != requests*4 {
					t.Fatalf("bad sequential offset: %d", req.Offset)
				}
				requests++
				chunk := append([]byte(nil), data[req.Offset:min(req.Offset+4, len(data))]...)
				switch mode {
				case "truncated":
					return chunk[:len(chunk)-1], nil
				case "extra":
					return append(chunk, 1), nil
				case "hash":
					chunk[0] ^= 1
				case "error":
					return nil, errors.New("unavailable")
				case "cancel":
					cancel()
				}
				return chunk, nil
			})
			if mode == "success" {
				if err != nil || !bytes.Equal(got, data) || cancels != 0 {
					t.Fatalf("round trip: %q %v, cancels=%d", got, err, cancels)
				}
			} else if err == nil || cancels != 1 {
				t.Fatalf("bad transfer: %v, cancels=%d", err, cancels)
			}
		})
	}
	got, err := Fetch(context.Background(), testReference(nil), "sokel.payload.grp_test.", func(context.Context, string, []byte) ([]byte, error) { return nil, fmt.Errorf("unexpected request") })
	if err != nil || len(got) != 0 {
		t.Fatalf("empty reference: %q %v", got, err)
	}
}
