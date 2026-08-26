// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// The contract declaration for the sysinfo example: what each operation takes and returns.
// Declaration only — no implementation, so the contract can be reviewed on its own.

package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// Memory statistics: a nested json output whose sub-fields expand under their sokel names.
type Memory struct {
	AllocBytes     uint64 `sokel:"alloc_bytes" label:"Allocated"`
	SysBytes       uint64 `sokel:"sys_bytes" label:"From OS"`
	HeapAllocBytes uint64 `sokel:"heap_alloc_bytes" label:"Heap allocated"`
}

// FileDigest hashes an uploaded file.
type FileDigest struct{}

func (FileDigest) Meta() sokel.Meta {
	return sokel.Meta{ID: "file_digest"}
}

func (FileDigest) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.File("file").Label("File").Desc("Any file; its md5 and size are computed"),
	}
}

func (FileDigest) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.String("filename").Label("File name"),
		field.String("md5").Label("MD5"),
		field.Int("size").Label("Bytes"),
	}
}

// SystemInfo reports basic facts about the machine the plugin runs on.
type SystemInfo struct{}

func (SystemInfo) Meta() sokel.Meta {
	return sokel.Meta{ID: "system_info"}
}

func (SystemInfo) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Bool("include_memory").Label("Include memory stats").Desc("Turn it off and the output omits memory").Default(true),
		field.String("note").Label("Note (echoed)").Desc("Echoed back verbatim, to check that inputs reach outputs field by field").Optional(),
	}
}

func (SystemInfo) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Bool("ok").Label("OK"),
		field.String("hostname").Label("Hostname"),
		field.String("os").Label("OS"),
		field.String("arch").Label("Architecture"),
		field.Int("num_cpu").Label("CPU cores"),
		field.String("go_version").Label("Go version"),
		field.Int("pid").Label("PID"),
		field.Int("goroutines").Label("Goroutines"),
		field.Int("uptime_seconds").Label("Uptime (s)"),
		field.String("started_at").Label("Started at"),
		field.String("now").Label("Now"),
		field.String("echo").Label("Echoed note"),
		field.Json("memory", Memory{}).Label("Memory stats").Optional(),
	}
}
