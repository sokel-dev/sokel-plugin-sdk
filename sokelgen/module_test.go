package sokelgen

import (
	"strings"
	"testing"
)

func TestImportPathOf(t *testing.T) {
	// 本包自身：go-sdk 的 module + 相对路径
	got, err := ImportPathOf(".")
	if err != nil {
		t.Fatal(err)
	}
	if got != "github.com/sokel-dev/sokel-plugin-sdk/sokelgen" {
		t.Errorf("本包导入路径: %q", got)
	}
	// 子目录
	sub, err := ImportPathOf("internal/demoschema")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sub, "/sokelgen/internal/demoschema") {
		t.Errorf("子包导入路径: %q", sub)
	}
	// 找不到 go.mod 要报可读错误，而不是返回一个瞎拼的路径
	if _, err := ImportPathOf("/"); err == nil {
		t.Error("根目录没有 go.mod，应报错")
	}
}
