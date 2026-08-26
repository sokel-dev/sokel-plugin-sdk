package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// loaded：一个插件的声明读出来之后的全部素材。generate 与 export 共用同一条加载路径，
// 免得两边对「什么算一个操作」有各自的理解。
type loaded struct {
	pkg         *sokelgen.Package
	ops         []sokelgen.OpIO
	importPath  string
	schemaDir   string
	credTypes   []string
	eventTypes  []string
	commonTypes []string
}

func load(dir, schemaSub string) (*loaded, error) {
	schemaDir := filepath.Join(dir, schemaSub)
	pkg, err := sokelgen.LoadDir(schemaDir)
	if err != nil {
		return nil, err
	}
	types, err := pkg.SchemaOps()
	if err != nil {
		return nil, err
	}
	importPath, err := sokelgen.ImportPathOf(schemaDir)
	if err != nil {
		return nil, err
	}
	ops, err := sokelgen.LoadDeclarations(schemaDir, importPath, types)
	if err != nil {
		return nil, err
	}
	return &loaded{
		pkg: pkg, ops: ops, importPath: importPath, schemaDir: schemaDir,
		credTypes:  pkg.CredentialTypes(),
		eventTypes: pkg.EventTypes(), commonTypes: pkg.CommonFieldsTypes(),
	}, nil
}

// warn：两条声明质量审计。
//   - 弱类型：每处 opaque 都该是有意识的决定，不是图省事的默认值。
//   - 数组元素形状：漏声明是**静默**的（field.Array 的形状参数是 any，传描述也编译得过），
//     下游只看到一个不透明数组。
//
// 多插件时带上目录前缀，否则一堆警告看不出是谁的。
func (l *loaded) warn(dir string, prefixed bool) {
	for _, w := range []string{
		sokelgen.FormatOpaqueWarnings(sokelgen.AuditOpaque(l.ops)),
		sokelgen.FormatArrayWarnings(sokelgen.AuditArrays(l.ops)),
	} {
		if w == "" {
			continue
		}
		if prefixed {
			for _, line := range strings.Split(strings.TrimRight(w, "\n"), "\n") {
				fmt.Fprintf(os.Stderr, "%s: %s\n", dir, line)
			}
			continue
		}
		fmt.Fprint(os.Stderr, w)
	}
}

// manifest：把这个插件的 Go 声明拼成语言中立的 manifest（export yaml 用）。
// 凭证 / 事件 / 认证都要一并带上——只导操作的话，别的语言照着实现会缺半个插件。
func (l *loaded) manifest(dir string) (*sokelgen.Manifest, error) {
	var cred []sokelgen.Field
	if len(l.credTypes) > 0 {
		var err error
		if cred, err = sokelgen.LoadCredential(l.schemaDir, l.importPath, l.credTypes); err != nil {
			return nil, err
		}
	}
	var auth *sokelgen.AuthMeta
	if authTypes := l.pkg.AuthTypes(); len(authTypes) > 0 {
		var err error
		if auth, err = sokelgen.LoadAuth(l.schemaDir, l.importPath, authTypes); err != nil {
			return nil, err
		}
	}
	var events []sokelgen.EventIO
	var common []string
	if len(l.eventTypes) > 0 {
		var err error
		if events, common, err = sokelgen.LoadEvents(l.schemaDir, l.importPath, l.eventTypes, l.commonTypes); err != nil {
			return nil, err
		}
	}
	return sokelgen.ManifestFrom(filepath.Base(mustAbs(dir)), l.ops, cred, auth, events, common), nil
}

func mustAbs(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// generateAny：按声明入口分流。manifest 优先——一个目录同时有 sokel.yaml 与 schema/ 时，
// 那多半是 Go 插件顺手导出了一份 manifest 给别人看，生成物仍以 Go 那条路为准。
func generateAny(dir, schemaSub string, check, quiet bool, lang string) error {
	if fi, err := os.Stat(filepath.Join(dir, schemaSub)); err == nil && fi.IsDir() {
		return generateOne(dir, schemaSub, check, quiet)
	}
	mf, err := sokelgen.FindManifest(dir)
	if err != nil {
		return err
	}
	if mf == "" {
		return fmt.Errorf("%s 既没有 %s/ 也没有 sokel.yaml", dir, schemaSub)
	}
	return generateManifest(mf, check, quiet, lang)
}

// generateOne 生成（或校验）一个插件的 zz_*.go。
func generateOne(dir, schemaSub string, check, quiet bool) error {
	l, err := load(dir, schemaSub)
	if err != nil {
		return err
	}
	l.warn(dir, quiet)

	// 主包可能还不存在（新插件从零开始：main.go 要用生成的类型，而生成又要读主包名）。
	// 这种鸡生蛋的情况按惯例默认 main，别让作者为了跑通生成器先写一个假文件。
	pkgName := "main"
	if mainPkg, err := sokelgen.LoadDir(dir); err == nil && mainPkg.Name != "" {
		pkgName = mainPkg.Name
	}
	sch := sokelgen.SchemaRef{Import: l.importPath, Name: l.pkg.Name}
	// 声明与生成物在**同一个包**（-schema . ）：内核自带契约声明时就是这种形态
	// （httpcore/plugin 里 schema.go 与 zz_*.go 同包）。此时不能自己 import 自己，
	// 类型也不该带包名前缀。
	if filepath.Clean(schemaSub) == "." {
		pkgName = l.pkg.Name
		sch = sokelgen.SchemaRef{}
	}

	files := map[string]func() (string, error){
		"zz_types.go":    func() (string, error) { return sokelgen.RenderTypes(pkgName, sch, l.ops) },
		"zz_register.go": func() (string, error) { return sokelgen.RenderRegister(pkgName, sch, l.ops) },
	}
	// 声明了凭证契约才有这一份（凭证也可以继续用 main 包里的 struct + sokel.WithCredential[T]，
	// 字段简单的插件不必为此开 schema 声明；需要 select / 默认值时再升级）。
	if len(l.credTypes) > 0 {
		credFields, cerr := sokelgen.LoadCredential(l.schemaDir, l.importPath, l.credTypes)
		if cerr != nil {
			return cerr
		}
		files["zz_credential.go"] = func() (string, error) { return sokelgen.RenderCredential(pkgName, sch, credFields) }
	}
	// 认证方式（凭证怎么拿到的）：声明了才生成。
	if authTypes := l.pkg.AuthTypes(); len(authTypes) > 0 {
		meta, aerr := sokelgen.LoadAuth(l.schemaDir, l.importPath, authTypes)
		if aerr != nil {
			return aerr
		}
		files["zz_auth.go"] = func() (string, error) { return sokelgen.RenderAuth(pkgName, *meta) }
	}
	// 事件源插件才有这一份（没声明事件就不生成，免得留一个空文件让人以为漏了什么）。
	if len(l.eventTypes) > 0 {
		events, common, eerr := sokelgen.LoadEvents(l.schemaDir, l.importPath, l.eventTypes, l.commonTypes)
		if eerr != nil {
			return eerr
		}
		files["zz_events.go"] = func() (string, error) { return sokelgen.RenderEvents(pkgName, sch, events, common) }
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		src, err := files[name]()
		if err != nil {
			return err
		}
		path := filepath.Join(dir, name)
		if check {
			// 「源码改了却忘了重新生成」是 codegen 最常见的失效方式，CI 拦这一道。
			old, rerr := os.ReadFile(path)
			if rerr != nil {
				return fmt.Errorf("%s 不存在", name)
			}
			if string(old) != src {
				return fmt.Errorf("%s 已过期", name)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("写入 %s: %w", path, err)
		}
	}
	if quiet {
		return nil
	}
	verb := "已生成"
	if check {
		verb = "是最新的"
	}
	fmt.Printf("sokel-gen: %s %s（%d 个操作）\n", verb, strings.Join(names, " / "), len(l.ops))
	return nil
}

// export 把同一份 IR 渲染成非 Go 的产物，写标准输出。
//
//	json    语言中立的契约本身——刻意不带 Go 类型名，给别的语言的生成器吃
//	ts      前端用的执行契约表，供其核对手写的 UI schema
//	python  pydantic 模型
func export(dir, schemaSub, format string) error {
	// manifest 插件：声明本身就是语言中立的，直接按它渲染，不必先有 Go 代码
	if mf, ferr := sokelgen.FindManifest(dir); ferr == nil && mf != "" {
		if _, serr := os.Stat(filepath.Join(dir, schemaSub)); serr != nil {
			return exportManifest(mf, format)
		}
	}
	l, err := load(dir, schemaSub)
	if err != nil {
		return err
	}
	l.warn(dir, false)
	switch format {
	case "json":
		b, err := sokelgen.ExportContract(l.ops)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	case "ts":
		src, err := sokelgen.RenderTS(l.importPath, l.ops)
		if err != nil {
			return err
		}
		fmt.Print(src)
	case "python":
		src, err := sokelgen.RenderPython(l.ops)
		if err != nil {
			return err
		}
		fmt.Print(src)
	case "yaml":
		// Go 声明 → 语言中立的 manifest：其他语言的作者据此照抄一份契约，
		// 不必读 Go 代码，也不必让 Go 那份声明成为事实标准。
		m, merr := l.manifest(dir)
		if merr != nil {
			return merr
		}
		out, merr := sokelgen.RenderManifestYAML(m)
		if merr != nil {
			return merr
		}
		fmt.Print(out)
	}
	return nil
}

// exportManifest：manifest 插件的导出（json = 契约本身；ts / python = 类型化外壳）。
func exportManifest(path, format string) error {
	m, err := sokelgen.LoadManifest(path)
	if err != nil {
		return err
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		return err
	}
	switch format {
	case "json":
		b, jerr := sokelgen.ExportManifestJSON(m, doc)
		if jerr != nil {
			return jerr
		}
		fmt.Println(string(b))
	case "yaml":
		out, yerr := sokelgen.RenderManifestYAML(m)
		if yerr != nil {
			return yerr
		}
		fmt.Print(out)
	default:
		src, rerr := renderManifest(m, doc, format)
		if rerr != nil {
			return rerr
		}
		fmt.Print(src)
	}
	return nil
}

// migrate 从旧的 struct+tag 契约反向生成 schema 声明代码，输出到标准输出供人工过目。
// 不直接写文件：生成的是迁移起点，需要人判断哪些字段该补结构、哪些该写清 Opaque 理由。
func migrate(dir string) error {
	pkg, err := sokelgen.LoadDir(dir)
	if err != nil {
		return err
	}
	ops, err := pkg.Ops()
	if err != nil {
		return err
	}
	src, err := sokelgen.RenderSchema("schema", ops)
	if err != nil {
		return err
	}
	fmt.Print(src)
	return nil
}
