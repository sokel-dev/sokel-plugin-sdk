// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package demoschema is the generator's test corpus, covering the builders' main shapes. It sits under
// internal because only sokelgen's own tests use it. The labels are deliberately non-ASCII: they are
// what exercises the generator's escaping.
package demoschema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// FileDigest digests a file.
type FileDigest struct{}

func (FileDigest) Meta() sokel.Meta {
	return sokel.Meta{ID: "file_digest", Label: "文件摘要", TimeoutSec: 30}
}
func (FileDigest) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.File("file").Label("文件"),
		field.Enum("algo", field.Opt("md5"), field.Opt("sha256", "SHA-256")).Label("算法").Default("md5"),
		field.Object("extra", "调用方透传，形状由上游决定").Label("附加"),
	}
}
func (FileDigest) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.String("md5").Label("MD5"),
		field.Number("size").Label("字节数"),
	}
}

// SysInfo reports system information, with a pointer receiver and no inputs.
type SysInfo struct{}

func (s *SysInfo) Meta() sokel.Meta { return sokel.Meta{ID: "system_info", Label: "系统信息"} }
func (s *SysInfo) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{field.Strings("hosts").Label("主机").Optional()}
}

// OSInfo is defined once: the contract derives its Shape from this type, and the implementation uses
// the same type.
type OSInfo struct {
	Name string `sokel:"name" label:"系统名"`
	Arch string `sokel:"arch" label:"架构"`
}

func (s *SysInfo) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Json("os", OSInfo{}).Label("系统"),
	}
}
