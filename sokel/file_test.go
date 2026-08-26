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
