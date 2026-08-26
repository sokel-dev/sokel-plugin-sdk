// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"io"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// File is a file reference. It is defined in the plugin package because it is only data; fetching
// the bytes needs a transport.
//
//	input:  in.SomeFile.Blob(ctx) fetches the bytes lazily (the SDK pulls them in chunks over NATS).
//	output: f, _ := ctx.Upload("r.json", mime, data) then out.Vars(Out{Report: f}).
type File = plugin.File

// Fetch implements plugin.Ctx: it pulls the bytes in chunks through the platform's file layer.
// File.Blob calls it.
func (c natsCtx) Fetch(f *File) ([]byte, error) {
	if c.rt == nil {
		return nil, errors.New("file runtime not ready")
	}
	return c.rt.fetch(c.Context, f)
}

// Upload produces a file: the bytes go back to the platform and the returned reference goes into
// the output struct.
func (c natsCtx) Upload(name, mime string, data []byte) (*File, error) {
	if c.rt == nil {
		return &File{Name: name, Mime: mime, Size: int64(len(data)), Data: data}, nil
	}
	return c.rt.store(c.Context, name, mime, data)
}

// UploadReader streams while reading: memory stays at one chunk (1MB) regardless of file size.
// Anything above a few hundred megabytes belongs here — Upload reads the whole file into memory
// first, which bursts the plugin process.
func (c natsCtx) UploadReader(name, mime string, r io.Reader) (*File, error) {
	if c.rt == nil { // bare ctx (tests): read it in and hand the bytes back directly
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return &File{Name: name, Mime: mime, Size: int64(len(b)), Data: b}, nil
	}
	return c.rt.storeReader(c.Context, name, mime, r)
}

// fileRuntime is the fetch/store backend for file bytes, injected by the transport.
type fileRuntime interface {
	fetch(ctx context.Context, f *File) ([]byte, error)
	store(ctx context.Context, name, mime string, data []byte) (*File, error)
	storeReader(ctx context.Context, name, mime string, r io.Reader) (*File, error)
}

// natsFiles exchanges file bytes with the platform over the existing NATS connection, 1MB per chunk,
// one request-reply each. The plugin never needs HTTP access to the platform, so a plugin behind NAT
// works the same way.
type natsFiles struct {
	nc    *nats.Conn
	token string
}

const fileChunk = 1 << 20

func (n natsFiles) fetch(_ context.Context, f *File) ([]byte, error) {
	id := f.ID
	if id == "" { // a reference carrying only a url: take its last path segment as the id
		if i := strings.LastIndex(f.URL, "/"); i >= 0 {
			id = f.URL[i+1:]
		}
	}
	if id == "" {
		return nil, errors.New("the file reference has neither id nor url")
	}
	var out []byte
	for seq := 0; ; seq++ {
		req, _ := json.Marshal(map[string]any{"token": n.token, "id": id, "seq": seq})
		resp, err := n.nc.Request("sokel.file.get", req, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("fetching chunk %d: %w", seq, err)
		}
		var r struct {
			Error string `json:"error"`
			Data  string `json:"data"`
			Last  bool   `json:"last"`
		}
		if err := json.Unmarshal(resp.Data, &r); err != nil {
			return nil, err
		}
		if r.Error != "" {
			return nil, errors.New(r.Error)
		}
		b, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
		if r.Last {
			return out, nil
		}
	}
}

// store takes whole bytes. It delegates to storeReader: the chunking protocol should exist once.
func (n natsFiles) store(ctx context.Context, name, mime string, data []byte) (*File, error) {
	return n.storeReader(ctx, name, mime, bytes.NewReader(data))
}

// storeReader **streams while reading**; memory stays at one chunk.
//
// A few hundred megabytes (a video or an archive on a NAS) would burst the plugin process if read in
// whole first. The platform already writes into its blob writer chunk by chunk — the bottleneck was
// only ever on the plugin side.
func (n natsFiles) storeReader(_ context.Context, name, mime string, r io.Reader) (*File, error) {
	uploadID := ""
	buf := make([]byte, fileChunk)
	for seq := 0; ; seq++ {
		nRead, rerr := io.ReadFull(r, buf)
		// ReadFull returns only when the buffer is full or the input ended; a short final read is a
		// normal end, not an error
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("reading chunk %d: %w", seq, rerr)
		}
		last := rerr == io.EOF || rerr == io.ErrUnexpectedEOF
		// An empty file still makes one round (last=true, 0 bytes), or the platform has no session to
		// close out
		if nRead == 0 && seq > 0 {
			last = true
		}
		req, _ := json.Marshal(map[string]any{
			"token": n.token, "upload_id": uploadID, "name": name, "mime": mime,
			"seq": seq, "last": last, "data": base64.StdEncoding.EncodeToString(buf[:nRead]),
		})
		resp, err := n.nc.Request("sokel.file.put", req, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("uploading chunk %d: %w", seq, err)
		}
		var rr struct {
			Error    string `json:"error"`
			UploadID string `json:"upload_id"`
			File     *File  `json:"file"`
		}
		if err := json.Unmarshal(resp.Data, &rr); err != nil {
			return nil, err
		}
		if rr.Error != "" {
			return nil, errors.New(rr.Error)
		}
		if rr.UploadID != "" {
			uploadID = rr.UploadID
		}
		if last {
			if rr.File == nil {
				return nil, errors.New("the platform returned no file reference")
			}
			return rr.File, nil
		}
	}
}
