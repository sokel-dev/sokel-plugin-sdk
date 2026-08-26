package main

// manifest（sokel.yaml / sokel.json）那条入口的生成与校验。
//
// 与 schema/ 那条入口的关系：**产出同一份契约，入口各随语言惯例**。
// Go 插件把契约写成 Go 代码（编译期检查、可以有循环和常量）；Python / Node 插件
// 把契约写成 YAML（不必为了声明几个字段先装一套 Go 工具链去读 builder 的 API）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// defaultOut：各语言生成物的默认文件名。写死而不是让人填——
// 名字统一之后，读别人的插件不必先找「生成的那个文件叫什么」。
var defaultOut = map[string]string{
	"ts":     "sokel.gen.ts",
	"python": "sokel_gen.py",
}

// generateManifest 生成（或校验）一个 manifest 插件的生成物。
func generateManifest(manifestPath string, check, quiet bool, langFlag string) error {
	m, err := sokelgen.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		return err
	}
	targets := m.Codegen
	if langFlag != "" {
		// -lang 指定时只生成那一种；manifest 里给了 out 就用它，没给用默认名
		picked := sokelgen.CodegenList{{Lang: langFlag}}
		for _, t := range m.Codegen {
			if t.Lang == langFlag {
				picked = sokelgen.CodegenList{t}
			}
		}
		targets = picked
	}
	if len(targets) == 0 {
		return fmt.Errorf("%s 没说要生成哪种语言 —— 在 codegen 里写 lang: ts / python，或加 -lang 参数", manifestPath)
	}

	var stale []string
	for _, t := range targets {
		src, rerr := renderManifest(m, doc, t.Lang)
		if rerr != nil {
			return rerr
		}
		out := t.Out
		if out == "" {
			out = defaultOut[t.Lang]
		}
		path := filepath.Join(m.Dir(), out)
		if check {
			// 「改了声明却忘了重新生成」是 codegen 最常见的失效方式，CI 拦这一道
			old, ferr := os.ReadFile(path)
			switch {
			case ferr != nil:
				stale = append(stale, fmt.Sprintf("%s 不存在（改了 %s 后没生成？）", out, filepath.Base(manifestPath)))
			case string(old) != src:
				stale = append(stale, out+" 已过期")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("建目录 %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("写入 %s: %w", path, err)
		}
		if !quiet {
			fmt.Printf("sokel-gen: 已生成 %s（%d 个操作，%d 个事件）\n", out, len(m.Operations), len(m.Events))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%s", strings.Join(stale, "；"))
	}
	if check && !quiet {
		fmt.Printf("sokel-gen: %s 的生成物均为最新（%d 个操作）\n", filepath.Base(manifestPath), len(m.Operations))
	}
	return nil
}

func renderManifest(m *sokelgen.Manifest, doc, lang string) (string, error) {
	switch lang {
	case "ts":
		return sokelgen.RenderTSPlugin(m, doc)
	case "python":
		return sokelgen.RenderPythonPlugin(m, doc)
	case "go":
		// Go 插件的契约走 schema/ 包声明——那条路能表达 manifest 表达不了的东西
		// （复用已有的 Go 类型、oneOf 指向真实类型），而不是反过来。
		return "", fmt.Errorf("Go 插件请用 schema/ 包声明契约（sokel-gen init 给的就是那种），manifest 目前生成 ts / python")
	}
	return "", fmt.Errorf("未知语言 %q（ts / python）", lang)
}
