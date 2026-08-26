// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// The sysinfo plugin reports basic facts about the machine it runs on.
//
// Written with the sokel SDK: the contract is declared in schema/, the generated OnXxx functions
// give fully concrete handler signatures, and nothing here touches the transport — only
// Config.Endpoint decides how it is actually deployed.
//
// Run it:
//
//	SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx go run .
package main

//go:generate go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen
// The contract comes from zz_types.go / zz_register.go, generated at build time rather than
// reflected at runtime. Change schema/ and regenerate.

import (
	"crypto/md5"
	"encoding/hex"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/sokel-dev/sokel-plugin-sdk/examples/sysinfo/schema"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
)

var startedAt = time.Now()

func main() {
	token := sokel.Env("TOKEN")
	if token == "" {
		log.Fatal("set SOKEL_TOKEN (the plugin's access token, from the plugin admin page)")
	}
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "http://localhost:8088"),
		Token:    token,
		Name:     "sysinfo-plugin",
	})
	p.SetDoc(usageDoc, "") // the user-facing doc (docs/*.md): reported at registration and shown in the UI

	OnSystemInfo(p, func(ctx sokel.Ctx, in *SystemInfoIn) (*SystemInfoOut, error) {
		now := time.Now()
		host, _ := os.Hostname()
		o := &SystemInfoOut{
			OK: true, Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH,
			NumCPU: runtime.NumCPU(), GoVersion: runtime.Version(), PID: os.Getpid(),
			Goroutines: runtime.NumGoroutine(), UptimeSeconds: int(now.Sub(startedAt).Seconds()),
			StartedAt: startedAt.UTC().Format(time.RFC3339), Now: now.UTC().Format(time.RFC3339),
			Echo: in.Note,
		}
		if in.IncludeMemory {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			o.Memory = schema.Memory{AllocBytes: m.Alloc, SysBytes: m.Sys, HeapAllocBytes: m.HeapAlloc}
		}
		return o, nil
	})

	// File digest: shows a file input — in.File.Blob(ctx) fetches the bytes lazily.
	OnFileDigest(p, func(ctx sokel.Ctx, in *FileDigestIn) (*FileDigestOut, error) {
		b, err := in.File.Blob(ctx)
		if err != nil {
			return nil, err
		}
		sum := md5.Sum(b)
		return &FileDigestOut{Filename: in.File.Name, MD5: hex.EncodeToString(sum[:]), Size: len(b)}, nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
