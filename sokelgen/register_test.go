package sokelgen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// 生成 OnXxx：把 schema 的 Meta、生成的契约、以及作者的 handler 三者接起来。
// 签名完全具体——类型安全在这里，sokel 库里零泛型。
func TestRenderRegister(t *testing.T) {
	ops := []OpIO{
		{OpID: "file_digest", SchemaType: "FileDigest", InType: "FileDigestIn", OutType: "FileDigestOut",
			Inputs: []Field{{Name: "file", Type: "file"}}},
		{OpID: "watch", SchemaType: "Watch", InType: "WatchIn", OutType: "WatchOut", Stream: true},
	}
	src, err := RenderRegister("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_register.go", src, 0); err != nil {
		t.Fatalf("生成的代码语法不合法: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		// 非流式：返回单值，最常见的形态
		`func OnFileDigest\(h plugin\.Host, fn func\(plugin\.Ctx, \*FileDigestIn\) \(\*FileDigestOut, error\)\)`,
		// 流式：拿到类型化的产出器，可多次发
		`func OnWatch\(h plugin\.Host, fn func\(plugin\.Ctx, \*WatchIn, \*WatchEmitter\) error\)`,
		`type WatchEmitter struct`,
		`func \(e \*WatchEmitter\) Vars\(v \*WatchOut\)`, // 类型安全的包装，库里那个收 any
		`h\.Register\(contract\.OperationOf\(&schema\.FileDigest\{}\)`,
		`contract\.BindInput`,
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("缺片段 %q\n---\n%s", want, src)
		}
	}
	// 契约取自 schema 声明本身（OperationOf），而不是把字段再抄一遍到生成物里——
	// 那样 schema 改了、生成物没跟上就会两边不一致。
	if strings.Contains(src, `Name: "file"`) {
		t.Errorf("契约不该在注册代码里重复展开\n---\n%s", src)
	}
}
