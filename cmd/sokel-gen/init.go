package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runInit：从零建一个插件骨架。
//
// 建出来的东西**当场就能跑通全链**：go mod tidy → sokel-gen → go build。
// 骨架里那个 hello 操作不是占位注释，是一个真的、生成得出来、编译得过的操作——
// 「先有一个跑得通的东西再改」比「照着文档从空目录拼」少一整轮试错。
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	module := fs.String("module", "", "go module 路径（默认取目录名）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("用法：sokel-gen init <目录> [-module <路径>]")
	}
	dir := fs.Arg(0)
	name := filepath.Base(filepath.Clean(dir))
	if name == "." || name == "/" || name == "" {
		return fmt.Errorf("请给一个具体的目录名，如 sokel-gen init ./my-plugin")
	}
	mod := *module
	if mod == "" {
		mod = name
	}

	files := scaffold(name)
	// 先整体检查再动手：宁可一个字节都不写，也不要写一半留下个残骸。
	for rel := range files {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return fmt.Errorf("%s 已存在——init 不覆盖任何文件", filepath.Join(dir, rel))
		}
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("写入 %s: %w", path, err)
		}
	}

	// go.mod 交给 go 自己建，别手写：版本与 go 指令行由工具链决定，手写迟早对不上。
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); os.IsNotExist(err) {
		cmd := exec.Command("go", "mod", "init", mod)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go mod init 失败: %w\n%s", err, out)
		}
	}

	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	fmt.Printf("sokel-gen: 已在 %s 建好插件骨架（%d 个文件 + go.mod）\n\n", dir, len(rels))
	fmt.Printf(`下一步：
  cd %s
  go mod tidy      # 拉 SDK
  sokel-gen          # 由 schema/ 生成 zz_*.go
  go build ./...

改契约就改 schema/schema.go，然后重跑 sokel-gen。
`, dir)
	return nil
}

// scaffold 返回 相对路径 → 内容。
//
// 两份文档都在里面且都不是空文件：README.md 给改代码的人，docs/<名>.md 给用户
// （后者被 doc.go embed 进二进制，随注册握手上报，界面「使用说明」显示的就是它）。
// 缺哪一份都会在评审时被打回，那就别让它一开始就缺。
func scaffold(name string) map[string]string {
	const sdk = "github.com/sokel-dev/sokel-plugin-sdk"
	r := strings.NewReplacer("{{name}}", name, "{{sdk}}", sdk)

	return map[string]string{
		"schema/schema.go": r.Replace(`// Package schema：{{name}} 的契约声明。
//
// 只声明，不含实现——契约是对外接口，应当能被单独评审。
// 一个操作 = 一个类型 + 三个方法（Meta / Inputs / Outputs）。
// 方法名写错（Input 而不是 Inputs）会直接编译失败，这是用 builder 而非 struct tag 的理由之一。
package schema

import (
	"{{sdk}}/contract"
	"{{sdk}}/contract/field"
)

// Hello 打个招呼——换成你自己的第一个操作。
type Hello struct{}

func (Hello) Meta() contract.Meta {
	return contract.Meta{ID: "hello", Label: "打招呼"}
}

func (Hello) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("name").Label("名字").Desc("要跟谁打招呼"),
	}
}

func (Hello) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("greeting").Label("招呼语"),
	}
}
`),

		"main.go": r.Replace(`// {{name}} —— Sokel 插件。
//
// 插件是**出站拨入**的：它主动连回平台，不需要开放入站端口或公网 IP。
//
// 运行：
//
//	SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx ./{{name}}
package main

//go:generate go run {{sdk}}/cmd/sokel-gen

import (
	"fmt"
	"log"

	"{{sdk}}/sokel"
)

func main() {
	token := sokel.Env("TOKEN")
	if token == "" {
		log.Fatal("请设置 SOKEL_TOKEN（插件管理里该插件的接入 token）")
	}
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "nats://localhost:4222"),
		Token:    token,
		Name:     "{{name}}",
	})
	p.SetDoc(usageDoc, "") // 使用说明随握手上报，界面「使用说明」显示它

	// OnHello 由 sokel-gen 从 schema/ 生成；HelloIn / HelloOut 同理。
	// 改了声明就重跑 sokel-gen，这里的签名会跟着变，改漏了编译不过。
	OnHello(p, func(ctx sokel.Ctx, in *HelloIn) (*HelloOut, error) {
		return &HelloOut{Greeting: fmt.Sprintf("你好，%s", in.Name)}, nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
`),

		"doc.go": r.Replace(`package main

// 使用说明：真的 markdown 文件，编译期 embed 进来。

import _ "embed"

//go:embed docs/{{name}}.md
var usageDoc string
`),

		"docs/" + name + ".md": r.Replace(`# {{name}}

> 给**用户**看的：这个插件能做什么、要填什么、有什么坑。
> 给改代码的人看的在 README.md。

## 能做什么

- **打招呼**：给一个名字，回一句招呼语。

## 配置

| 环境变量 | 必填 | 说明 |
|---|---|---|
| ` + "`SOKEL_ENDPOINT`" + ` | 是 | broker 地址，如 ` + "`nats://broker:4222`" + ` |
| ` + "`SOKEL_TOKEN`" + ` | 是 | 接入组 token（` + "`skp_…`" + `） |
`),

		"README.md": r.Replace(`# {{name}}

> 给**改代码的人**看的。给用户看的在 ` + "`docs/{{name}}.md`" + `。

## 结构

| 文件 | 作用 |
|---|---|
| ` + "`schema/schema.go`" + ` | 契约声明——有哪些操作、收什么回什么。**改这里** |
| ` + "`zz_*.go`" + ` | 由声明生成的类型与注册函数。**别手改** |
| ` + "`main.go`" + ` | handler 实现 + 连回平台的接线 |
| ` + "`docs/{{name}}.md`" + ` | 给用户的说明，编译期 embed 进二进制 |

## 开发

` + "```bash" + `
sokel-gen            # 改了 schema/ 就重新生成
go build ./...
sokel-gen check      # CI 用：校验生成物是否最新
` + "```" + `

契约是**编译期生成**的，不是运行期反射：声明写错在编译期就被拦住。
改了声明忘了重新生成，` + "`sokel-gen check`" + ` 会红——这是 codegen 最常见的失效方式。
`),
	}
}
