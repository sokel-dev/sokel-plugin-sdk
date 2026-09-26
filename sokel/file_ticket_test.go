// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

// A file moved while handling a call carries that call's ticket back, so a replica serving the
// whole platform files its output under the calling workspace and reads only that workspace's
// files. Without the ticket the platform cannot tell which workspace the upload belongs to.
func TestFileTransfersCarryTheCallTicket(t *testing.T) {
	var frames []map[string]any
	rt := natsFiles{token: "skp_x", req: func(subj string, data []byte, _ time.Duration) (*nats.Msg, error) {
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		m["_subject"] = subj
		frames = append(frames, m)
		if subj == "sokel.file.get" {
			return &nats.Msg{Data: []byte(`{"data":"aGk=","last":true}`)}, nil
		}
		return &nats.Msg{Data: []byte(`{"upload_id":"fu_1","file":{"id":"f_1"}}`)}, nil
	}}
	ctx := natsCtx{Context: withFileTicket(context.Background(), "ticket-ws-dev"), rt: rt}
	if _, err := ctx.Upload("a.txt", "text/plain", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.Fetch(&File{ID: "f_1"}); err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 {
		t.Fatalf("expected an upload and a fetch frame, got %d", len(frames))
	}
	for _, f := range frames {
		if f["ticket"] != "ticket-ws-dev" {
			t.Errorf("%v frame did not carry the call ticket: %v", f["_subject"], f)
		}
	}
	// Outside a call there is no ticket and the frame says so (empty), rather than inventing one.
	frames = nil
	bare := natsCtx{Context: context.Background(), rt: rt}
	_, _ = bare.Upload("b.txt", "text/plain", []byte("x"))
	if len(frames) == 0 || frames[0]["ticket"] != "" {
		t.Errorf("a transfer outside a call must not carry a ticket: %v", frames)
	}
}
