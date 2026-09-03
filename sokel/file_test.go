// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	nats "github.com/nats-io/nats.go"
	"time"
)

// fakeFiles records every chunk, pinning down the chunking behaviour. The real implementation goes over
// NATS; only how it is split matters here.
type fakeFiles struct {
	chunks [][]byte
	peak   int // the largest chunk received in one go; streaming means this equals the chunk size
}

func (f *fakeFiles) fetch(context.Context, *File) ([]byte, error) { return nil, nil }
func (f *fakeFiles) store(ctx context.Context, name, mime string, data []byte) (*File, error) {
	return f.storeReader(ctx, name, mime, bytes.NewReader(data))
}
func (f *fakeFiles) storeReader(_ context.Context, name, mime string, r io.Reader) (*File, error) {
	buf := make([]byte, fileChunk)
	total := 0
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			f.chunks = append(f.chunks, append([]byte(nil), buf[:n]...))
			if n > f.peak {
				f.peak = n
			}
			total += n
		}
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
	}
	return &File{ID: "f_1", Name: name, Mime: mime, Size: int64(total)}, nil
}

// A large file must be **streamed while read**: memory use is always one chunk, independent of the
// file's size. Reading it all first would mean a 2 GB video on a NAS demanding 2 GB resident — not a bit
// slower, but the plugin process bursting.
func TestUploadReaderStreamsInChunks(t *testing.T) {
	rt := &fakeFiles{}
	ctx := natsCtx{Context: context.Background(), rt: rt}
	size := fileChunk*3 + 12345
	f, err := ctx.UploadReader("big.mp4", "video/mp4", strings.NewReader(strings.Repeat("x", size)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Size != int64(size) {
		t.Errorf("size mismatch: %d, want %d", f.Size, size)
	}
	if len(rt.chunks) != 4 {
		t.Errorf("should split into 4 chunks, 3 full and 1 tail, got %d", len(rt.chunks))
	}
	if rt.peak > fileChunk {
		t.Errorf("no chunk should exceed %d, got %d", fileChunk, rt.peak)
	}
	// Splitting must lose nothing and misplace nothing
	var got bytes.Buffer
	for _, c := range rt.chunks {
		got.Write(c)
	}
	if got.Len() != size {
		t.Errorf("reassembled %d bytes, want %d", got.Len(), size)
	}
}

// Upload (whole bytes) and UploadReader must produce identical chunks: the two routes should share one
// chunking implementation.
func TestUploadAndUploadReaderAgree(t *testing.T) {
	data := []byte(strings.Repeat("y", fileChunk+7))
	a, b := &fakeFiles{}, &fakeFiles{}
	if _, err := (natsCtx{Context: context.Background(), rt: a}).Upload("f", "application/octet-stream", data); err != nil {
		t.Fatal(err)
	}
	if _, err := (natsCtx{Context: context.Background(), rt: b}).UploadReader("f", "application/octet-stream", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(len(a.chunks), a.peak) != fmt.Sprint(len(b.chunks), b.peak) {
		t.Errorf("the two routes chunk differently: %d/%d vs %d/%d", len(a.chunks), a.peak, len(b.chunks), b.peak)
	}
}

// The platform re-registers its file subjects on every restart; uploads that land in that
// window get nats.ErrNoResponders and used to fail outright (observed in production: seven
// merge_sgml uploads failing at the exact second the new platform process started). A short
// bounded retry rides out the window; anything else still fails fast.
func TestFilePutRetriesNoResponders(t *testing.T) {
	old := filePutRetryGap
	filePutRetryGap = time.Millisecond
	t.Cleanup(func() { filePutRetryGap = old })
	calls := 0
	req := func(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
		calls++
		if calls <= 2 {
			return nil, nats.ErrNoResponders
		}
		return &nats.Msg{Data: []byte(`{"file":{"id":"f_x"}}`)}, nil
	}
	msg, err := requestFileChunk(req, []byte(`{}`))
	if err != nil {
		t.Fatalf("should have ridden out the no-responders window: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 2 retries then success, got %d calls", calls)
	}
	if string(msg.Data) == "" {
		t.Error("response lost")
	}
}

func TestFilePutGivesUpAfterBoundedRetries(t *testing.T) {
	old := filePutRetryGap
	filePutRetryGap = time.Millisecond
	t.Cleanup(func() { filePutRetryGap = old })
	calls := 0
	req := func(string, []byte, time.Duration) (*nats.Msg, error) {
		calls++
		return nil, nats.ErrNoResponders
	}
	if _, err := requestFileChunk(req, []byte(`{}`)); err == nil {
		t.Fatal("a genuinely absent platform must still surface an error")
	}
	if calls != filePutAttempts {
		t.Errorf("expected exactly %d bounded attempts, got %d", filePutAttempts, calls)
	}
}

func TestFilePutDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	req := func(string, []byte, time.Duration) (*nats.Msg, error) {
		calls++
		return nil, nats.ErrTimeout
	}
	if _, err := requestFileChunk(req, []byte(`{}`)); err == nil {
		t.Fatal("timeouts are not the restart window; fail fast")
	}
	if calls != 1 {
		t.Errorf("non-no-responders errors must not retry, got %d calls", calls)
	}
}

// The broker address is not forever: a platform that restarts with an embedded broker, or an
// operator that moves the broker, leaves replicas faithfully redialing an address where nobody
// will ever listen again (MaxReconnects(-1); observed: a 6-day-old container permanently
// orphaned after a broker switch, failing every round). Once the connection has been down long
// enough, re-run discovery: if it points elsewhere, exit so the supervisor restarts us onto the
// fresh address. Same address (or discovery itself failing) means keep waiting -- the outage is
// the broker's, not the address's.
var errNoPlatform = fmt.Errorf("connect-info unreachable")

func TestRediscoverOutcome(t *testing.T) {
	same := func() (access, error) { return access{URL: "nats://a:4222"}, nil }
	moved := func() (access, error) { return access{URL: "nats://b:4222"}, nil }
	broken := func() (access, error) { return access{}, errNoPlatform }
	// "the platform has no transport" is an ANSWER, not a failure. Waiting on it forever was the
	// bug: a misconfigured platform NATS left every replica orphaned in silence.
	noTransport := func() (access, error) { return access{}, errNoTransport{detail: "NATS not configured"} }

	if exit, _ := rediscoverOutcome(nats.RECONNECTING, 30*time.Second, time.Minute, moved, "nats://a:4222"); exit {
		t.Error("below the threshold nothing should happen (normal reconnect jitter)")
	}
	if exit, _ := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, broken, "nats://a:4222"); exit {
		t.Error("discovery failing means the platform is down too; keep waiting")
	}
	if exit, _ := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, same, "nats://a:4222"); exit {
		t.Error("same address: the broker is down, not moved; keep waiting")
	}
	// A CLOSED connection is terminal: the client will not come back on its own, whatever the
	// threshold says. Waiting on it was a real symptom -- "connection closed" every 8s forever
	// after this group's credentials changed under us.
	exitC, reasonC := rediscoverOutcome(nats.CLOSED, time.Second, time.Minute, same, "nats://a:4222")
	if !exitC {
		t.Error("a CLOSED connection must trigger an exit immediately, not wait for the threshold")
	}
	if !strings.Contains(reasonC, "credentials") {
		t.Errorf("the exit reason should point at credentials, the usual cause: %q", reasonC)
	}
	exitNT, reasonNT := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, noTransport, "nats://a:4222")
	if !exitNT {
		t.Error("the platform answering \"no transport\" must trigger an exit, not an endless wait")
	}
	if !strings.Contains(reasonNT, "no transport") {
		t.Errorf("the exit reason must name what the platform said: %q", reasonNT)
	}
	exit, reason := rediscoverOutcome(nats.RECONNECTING, 2*time.Minute, time.Minute, moved, "nats://a:4222")
	if !exit {
		t.Fatal("a moved broker must trigger an exit so the supervisor reconnects us")
	}
	if reason == "" {
		t.Error("the exit must say where the broker went")
	}
}
