package sokelgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImportPathOf 推断某目录的 Go 导入路径：向上找 go.mod 取 module，再拼相对路径。
//
// 不用 go list：那要跑 go 命令且慢；这里只需要一个字符串，读 go.mod 就够，
// 也让生成器在没有完整构建环境时仍能工作（真正需要构建的是后面运行 schema 那一步）。
func ImportPathOf(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root := abs
	for {
		if b, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
			mod := moduleName(string(b))
			if mod == "" {
				return "", fmt.Errorf("%s/go.mod 里没找到 module 声明", root)
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return mod, nil
			}
			return mod + "/" + filepath.ToSlash(rel), nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("从 %s 向上找不到 go.mod", dir)
		}
		root = parent
	}
}

func moduleName(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
