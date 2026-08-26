// sokel-gen：Sokel 插件的契约工具链。
//
// 契约在 schema/ 包里用 builder 声明，sokel-gen 把它编译运行取值，再渲染成各语言的产物。
// 所以「声明写错」是编译期错误，不是某次调用才发现的运行期意外。
//
//	sokel-gen                       生成当前目录的契约代码（//go:generate 用这个形态）
//	sokel-gen init <目录>            从零建一个插件骨架
//	sokel-gen generate [目录...]     生成；给的目录下有多个插件时自动全扫
//	sokel-gen check [目录...]        只校验生成物是否最新，不写文件（CI 用）
//	sokel-gen export <格式> [目录]   导出契约：json（语言中立）/ ts / python
//	sokel-gen migrate [目录]         从旧 struct+tag 反向生成 schema 声明
//	sokel-gen docs [主题]            印出写法说明 / JSON Schema / 参考声明（编进二进制，离线可读）
//	sokel-gen example [语言]         印出参考插件的声明与两种语言的实现
//
// 多插件是**按 schema/ 目录发现**的，不是按 //go:generate 指令——漏写指令的插件
// `go generate ./...` 会静默跳过（实报：四个内置插件这么漏了半年），按目录发现漏不掉。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sokel-gen:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	// 无参 = 生成当前目录。现存插件的 //go:generate 全是这个形态，别破坏它。
	if len(args) == 0 {
		return generate([]string{"."}, "schema", false, "")
	}
	switch cmd := args[0]; cmd {
	case "init":
		return runInit(args[1:])
	case "generate", "check":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		schema := fs.String("schema", "schema", "schema 包目录（相对插件根目录）")
		lang := fs.String("lang", "", "manifest 插件的生成语言：ts / python（缺省读 codegen.lang）")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		dirs := fs.Args()
		if len(dirs) == 0 {
			dirs = []string{"."}
		}
		return generate(dirs, *schema, cmd == "check", *lang)
	case "docs":
		return runDocs(args[1:])
	case "example":
		return runExample(args[1:])
	case "export":
		return runExport(args[1:])
	case "migrate":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return migrate(dir)
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("未知子命令 %q", cmd)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `sokel-gen —— Sokel 插件的契约工具链

用法：
  sokel-gen                       生成当前目录的契约代码（//go:generate 用这个形态）
  sokel-gen init <目录>            从零建一个插件骨架
  sokel-gen generate [目录...]     生成；给的目录下有多个插件时自动全扫
  sokel-gen check [目录...]        只校验生成物是否最新，不写文件（CI 用）
  sokel-gen export <格式> [目录]   导出契约：json / yaml（语言中立声明）/ ts / python
  sokel-gen migrate [目录]         从旧 struct+tag 反向生成 schema 声明
  sokel-gen docs [主题]            印出 sokel.yaml 的写法说明（manifest / schema / example）
  sokel-gen example [语言]         印出覆盖全部形态的参考插件（yaml / python / node）

选项（generate / check）：
  -schema <名>   schema 包目录，默认 schema
  -lang <语言>   manifest（sokel.yaml）插件的生成语言：ts / python

插件有两种声明入口，产出同一份契约：
  schema/ 包      Go 插件（编译期校验，可复用已有 Go 类型）
  sokel.yaml      语言中立（Python / Node 插件），生成 ts / python 的类型化外壳

例：
  sokel-gen init ./my-plugin
  sokel-gen check ./plugin-builtin        # 一次校验该目录下所有插件
  sokel-gen export json > contract.json
  sokel-gen export yaml ./plugins/gitlab       # Go 声明 → 语言中立的 sokel.yaml
  sokel-gen generate -lang python ./my-plugin  # sokel.yaml → 类型化 Python 外壳
`)
	agentHint(w)
}

// generate：把每个给定目录展开成插件列表，逐个生成或校验。
//
// check 模式**跑完全部再报**，而不是撞见第一个就退——CI 里一次看清所有过期的插件，
// 比修一个跑一轮快得多。
func generate(dirs []string, schemaSub string, check bool, lang string) error {
	var plugins []string
	for _, d := range dirs {
		found, err := discover(d, schemaSub)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return fmt.Errorf("%s 下没找到插件（判据：目录里有 %s/ 子目录，或一份 sokel.yaml）", d, schemaSub)
		}
		plugins = append(plugins, found...)
	}
	sort.Strings(plugins)

	var stale []string
	for _, p := range plugins {
		if err := generateAny(p, schemaSub, check, len(plugins) > 1, lang); err != nil {
			if !check {
				return fmt.Errorf("%s: %w", p, err)
			}
			stale = append(stale, fmt.Sprintf("  %s —— %v", p, err))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("以下插件的生成物已过期（改了 schema 没重新生成）：\n%s\n修复：sokel-gen generate %s",
			strings.Join(stale, "\n"), strings.Join(dirs, " "))
	}
	if len(plugins) > 1 {
		verb := "已生成"
		if check {
			verb = "均为最新"
		}
		fmt.Printf("sokel-gen: %d 个插件%s\n", len(plugins), verb)
	}
	return nil
}

// discover：找出 root 下的插件。判据是「目录里有 schema/ 子目录，或一份 sokel.yaml」。
// 找到即不再往下钻——插件内部不会再嵌插件，继续走只会撞进它自己的子包。
//
// 按目录发现而不是按 //go:generate 指令：漏写指令的插件 `go generate ./...` 会**静默跳过**
// （实报：四个内置插件这么漏了半年），而契约漂了是不会有任何症状的。
func isPluginDir(dir, schemaSub string) bool {
	if fi, err := os.Stat(filepath.Join(dir, schemaSub)); err == nil && fi.IsDir() {
		return true
	}
	mf, err := sokelgen.FindManifest(dir)
	return err == nil && mf != ""
}

func discover(root, schemaSub string) ([]string, error) {
	if isPluginDir(root, schemaSub) {
		return []string{root}, nil
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "testdata":
			return filepath.SkipDir
		}
		if isPluginDir(path, schemaSub) {
			found = append(found, path)
			return filepath.SkipDir
		}
		return nil
	})
	return found, err
}

func runExport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("export 要指定格式：json / ts / python")
	}
	format := args[0]
	switch format {
	case "json", "yaml", "ts", "python":
	default:
		return fmt.Errorf("未知格式 %q（可选 json / yaml / ts / python）", format)
	}
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	schema := fs.String("schema", "schema", "schema 包目录（相对插件根目录）")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	return export(dir, *schema, format)
}
