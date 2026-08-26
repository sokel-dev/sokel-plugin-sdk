package main

// `sokel-gen docs` / `sokel-gen example`：把写一个插件需要读的东西直接印到 stdout。
//
// 读它的往往不是人。让 AI 照着写插件时，它能跑命令、拿 stdout，却未必能访问 GitHub，
// 也未必手边有这个仓库——所以格式说明、JSON Schema 与一份覆盖全形态的参考声明
// 都编进了二进制（见仓库根的 embed.go），一条命令就能拿到。

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/sokel-dev/sokel-plugin-sdk"
)

// runDocs 打印格式说明。`docs example` 转给 runExample —— 两种猜法都能用，
// 猜错一次就要去翻 help 的工具，AI 用起来会卡在这种地方。
func runDocs(args []string) error {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}
	switch topic {
	case "", "manifest", "format":
		fmt.Print(sdk.ManifestDoc)
	case "schema":
		fmt.Println(strings.TrimRight(sdk.Schema, "\n"))
	case "example":
		return runExample(args[1:])
	case "list":
		fmt.Print(docsTopics)
	default:
		return fmt.Errorf("未知主题 %q —— 可选 manifest（缺省）/ schema / example，或 `sokel-gen docs list`", topic)
	}
	return nil
}

// runExample 打印参考插件：一份声明 + 两种语言的实现，都是仓库里真在跑的那份。
func runExample(args []string) error {
	which := ""
	if len(args) > 0 {
		which = args[0]
	}
	switch which {
	case "", "yaml", "manifest":
		fmt.Print(exampleBanner)
		fmt.Print(sdk.ExampleManifest)
	case "python", "py":
		fmt.Print(sdk.ExamplePython)
	case "ts", "node", "typescript":
		fmt.Print(sdk.ExampleNode)
	case "go":
		// Go 插件的契约不写在 sokel.yaml 里，指过去比印一份半吊子的例子有用
		return fmt.Errorf("Go 插件的契约写在 schema/ 包里，不是 sokel.yaml——`sokel-gen init ./my-plugin` 出来的骨架就是那种形态")
	default:
		return fmt.Errorf("未知实现 %q —— 可选 yaml（缺省）/ python / node", which)
	}
	return nil
}

const exampleBanner = `# 以下是参考插件 kitchen-sink 的完整声明（每种字段形态、文件、流式、事件、
# webhook、协作式认证各一份）。照抄时记得改 plugin.name 与 codegen.out 的路径。
#
# 配套实现：sokel-gen example python / sokel-gen example node
# 格式说明：sokel-gen docs        JSON Schema：sokel-gen docs schema
`

const docsTopics = `sokel-gen docs [主题]

  manifest   sokel.yaml 的写法说明（缺省）——字段类型、oneOf/valueType/opaque、
             事件与公共字段、凭证与认证流、生成与校验
  schema     JSON Schema：编辑器补全用，也可喂给会读 schema 的工具
  example    覆盖全部形态的参考声明（= sokel-gen example）

sokel-gen example [语言]

  yaml       参考插件的声明（缺省）
  python     配套的 Python 实现
  node       配套的 TypeScript 实现
`

// 写给「让 AI 自己做插件」这条路的开场白：init 建骨架、docs 查写法、example 对照、
// generate 生成并校验。四条命令能把一个插件从零写完。
func agentHint(w *os.File) {
	fmt.Fprint(w, `
让 AI 自己写插件时，把这四条给它：
  sokel-gen docs                  # sokel.yaml 怎么写（完整格式说明）
  sokel-gen example               # 一份覆盖全部形态的真实声明，照着改
  sokel-gen init -lang python|ts <目录>   # 建骨架（已带 schema 注解与两份文档）
  sokel-gen generate <目录>        # 生成类型化外壳；声明有问题会一次报全
`)
}
