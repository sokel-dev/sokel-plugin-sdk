// Package demoschema 是生成器的验证语料：覆盖 builder 的主要形态。
// 放在 internal 下，只给 sokelgen 自己的测试用。
package demoschema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// FileDigest 文件摘要。
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

// SysInfo 系统信息（指针接收者 + 空入参）。
type SysInfo struct{}

func (s *SysInfo) Meta() sokel.Meta { return sokel.Meta{ID: "system_info", Label: "系统信息"} }
func (s *SysInfo) Inputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{field.Strings("hosts").Label("主机").Optional()}
}

// OSInfo 结构定义只有一处 —— 契约用 Shape 从它推导，实现里也用它。
type OSInfo struct {
	Name string `sokel:"name" label:"系统名"`
	Arch string `sokel:"arch" label:"架构"`
}

func (s *SysInfo) Outputs() []sokel.FieldSpec {
	return []sokel.FieldSpec{
		field.Json("os", OSInfo{}).Label("系统"),
	}
}
