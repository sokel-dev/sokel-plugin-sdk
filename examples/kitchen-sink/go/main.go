// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// The kitchen-sink reference plugin, Go implementation — **declared in a manifest, not a schema
// package**.
//
// ../manifest.yml is the one declaration behind all four shells (Go, Python, TypeScript, and the
// language-neutral JSON). This file is only the implementation, and it looks exactly like a
// schema-declared plugin's would: the generated API is the same either way, which is the point of
// allowing both.
//
//	go run ./examples/kitchen-sink/go   # with SOKEL_ENDPOINT and SOKEL_TOKEN set
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

func main() {
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "http://localhost:8088"),
		Token:    sokel.Env("TOKEN"),
		Name:     "kitchen-sink",
	})
	RegisterCredential(p)
	RegisterDoc(p)
	DeclareEvents(p)

	OnEchoAll(p, echoAll)
	OnFileDigest(p, fileDigest)
	OnHealthCheck(p, healthCheck)
	OnChatStream(p, chatStream)
	OnRowstoreQuery(p, rowstoreQuery)
	OnVectorstoreQuery(p, vectorstoreQuery)
	OnVectorstoreKeywordNgramKeywordQuery(p, keywordQuery)

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// echoAll hands every declared shape straight back: what you declare is what the runtime value is.
func echoAll(_ plugin.Ctx, in *EchoAllIn) (*EchoAllOut, error) {
	return &EchoAllOut{
		Text: in.Text, Count: in.Count, Mode: in.Mode, Tags: in.Tags,
		Profile: in.Profile, Points: in.Points, Labels: in.Labels,
		Summary: fmt.Sprintf("%d tags, %d points", len(in.Tags), len(in.Points)),
	}, nil
}

func fileDigest(ctx plugin.Ctx, in *FileDigestIn) (*FileDigestOut, error) {
	if in.File == nil {
		return nil, fmt.Errorf("no file given (attach one to the node's file input)")
	}
	b, err := ctx.Fetch(in.File)
	if err != nil {
		return nil, fmt.Errorf("reading the file failed: %w", err)
	}
	sum := sha256.Sum256(b)
	return &FileDigestOut{
		Name: in.File.Name, Sha256: hex.EncodeToString(sum[:]), Size: len(b),
		ExtraCount: len(in.Extras),
	}, nil
}

// healthCheck reports an unusable credential as ok=false rather than as an error: an error tells the
// platform only "the call failed", never whether the key expired or the network is down.
func healthCheck(_ plugin.Ctx, _ *HealthCheckIn) (*HealthCheckOut, error) {
	return &HealthCheckOut{OK: true, Message: "the reference plugin is always healthy"}, nil
}

// chatStream shows the streaming split: text frames while it runs, typed variables at the end.
// The increment and the accumulation are different fields — a later frame overwrites the same name,
// so sharing one would leave the consumer holding the last fragment only.
func chatStream(_ plugin.Ctx, in *ChatStreamIn, out plugin.Sink) error {
	n := in.Chunks
	if n <= 0 {
		n = 3
	}
	var full strings.Builder
	for i := 0; i < n; i++ {
		frag := fmt.Sprintf("%s #%d ", in.Prompt, i+1)
		full.WriteString(frag)
		out.Text(frag)
		time.Sleep(10 * time.Millisecond)
	}
	out.Vars(&ChatStreamOut{Reply: full.String(), Frames: n})
	return nil
}

// —— capability slots ——
//
// These fill the platform's rowstore / vectorstore interfaces. Their ids carry the capability
// (rowstore.query), which is why the generated names do too.

func rowstoreQuery(_ plugin.Ctx, in *RowstoreQueryIn) (*RowstoreQueryOut, error) {
	return &RowstoreQueryOut{Rows: map[string]any{"table": in.Table, "note": "the reference plugin returns nothing real"}}, nil
}

func vectorstoreQuery(_ plugin.Ctx, in *VectorstoreQueryIn) (*VectorstoreQueryOut, error) {
	return &VectorstoreQueryOut{Hits: []map[string]any{{"kb_id": in.KbID, "k": in.K, "score": 0.99}}}, nil
}

func keywordQuery(_ plugin.Ctx, in *VectorstoreKeywordNgramKeywordQueryIn) (*VectorstoreKeywordNgramKeywordQueryOut, error) {
	return &VectorstoreKeywordNgramKeywordQueryOut{Hits: []map[string]any{{"kb_id": in.KbID, "query": in.Query}}}, nil
}
