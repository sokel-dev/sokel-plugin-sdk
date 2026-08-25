package sokelgen

import (
	"strings"
	"testing"
)

// 新形态下生成器要找的不再是 sokel.Register 调用，而是**实现了 sokel.Schema 的类型**
// （Meta / Inputs / Outputs 三个方法齐全）。
// 判定按方法集而非显式接口断言：作者不会写 `var _ sokel.Schema = FileDigest{}`，
// 强制他写等于凭空加一道仪式。
const schemaSrc = `package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// FileDigest 文件摘要。
type FileDigest struct{}

func (FileDigest) Meta() sokel.Meta { return sokel.Meta{ID: "file_digest"} }
func (FileDigest) Inputs() []sokel.FieldSpec { return []sokel.FieldSpec{field.File("file")} }
func (FileDigest) Outputs() []sokel.FieldSpec { return []sokel.FieldSpec{field.String("md5")} }

// SysInfo 指针接收者也算。
type SysInfo struct{}

func (s *SysInfo) Meta() sokel.Meta { return sokel.Meta{ID: "system_info"} }
func (s *SysInfo) Inputs() []sokel.FieldSpec { return nil }
func (s *SysInfo) Outputs() []sokel.FieldSpec { return nil }

// Helper 只有部分方法 —— 不是 Schema，不该被误认。
type Helper struct{}

func (Helper) Meta() sokel.Meta { return sokel.Meta{} }

// 普通类型
type Row struct{ Name string }
`

func TestFindSchemas(t *testing.T) {
	pkg, err := Load(schemaSrc)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	got := pkg.SchemaTypes()
	if len(got) != 2 {
		t.Fatalf("应找到 2 个 Schema 类型，got %v", got)
	}
	// 确定性：按名字排序，否则生成物每次顺序都不同
	if got[0] != "FileDigest" || got[1] != "SysInfo" {
		t.Errorf("应按名字排序且值/指针接收者都算: %v", got)
	}
	for _, bad := range []string{"Helper", "Row"} {
		for _, g := range got {
			if g == bad {
				t.Errorf("%s 不该被认成 Schema", bad)
			}
		}
	}
}

// 一个 schema 包里没有任何 Schema 类型 → 报错，别产出空文件让人困惑。
func TestFindSchemasEmpty(t *testing.T) {
	pkg, _ := Load(`package schema

type Row struct{ Name string }
`)
	if _, err := pkg.SchemaOps(); err == nil || !strings.Contains(err.Error(), "Schema") {
		t.Errorf("无 Schema 类型应报可读错误: %v", err)
	}
}
