# 参与贡献

## 本地开发

```bash
go build ./...
go test ./...
gofmt -l .          # 必须无输出
```

## 改了 `schema/` 声明就要重新生成

契约是**编译期生成**的，不是运行期反射。示例插件的 `zz_*.go` 由 `sokel-gen` 产出：

```bash
go run ./cmd/sokel-gen generate ./examples
go run ./cmd/sokel-gen check ./examples    # CI 跑的就是这条
```

CI 用 `sokel-gen check` 拦「改了声明没重新生成」——这是 codegen 最常见的失效方式。

## 提交信息

`<type>: <描述>`，type 取 feat / fix / refactor / docs / test / chore / perf / ci。

## 让 agent 写插件

`skills/sokel-plugin-dev/` 是一个**独立完整**的 agent skill（不绑定任何一家 agent 产品）：
`SKILL.md` 是入口，`references/` 覆盖工具链、manifest 格式、平台侧接入，以及那些**坏了也不报错**
的规矩。（`.claude/skills/` 是指过去的软链，在本仓干活的 agent 会自动加载。）

`references/` 大部分是**生成的**，不是手写的：

```bash
skills/sokel-plugin-dev/scripts/sync-references.sh           # 重新生成
skills/sokel-plugin-dev/scripts/sync-references.sh --check   # CI 跑的就是这条
```

`manifest.md`、`manifest.schema.json` 与三份示例都由 `sokel-gen` 打印，而它 embed 的正是本仓那几个
文件——所以改了 `docs/manifest.md` 却忘了同步 skill，CI 会红，而不是留一份过期的等人去读。
手写的只有 `toolchain.md` / `platform.md` / `rules.md`，流程变了记得同步这三份。

## 提 PR 之前

- [ ] `go build ./...`、`go test ./...`、`gofmt -l .` 三项皆过
- [ ] 改了线协议形状？同步更新 `docs/` 里的对应说明
- [ ] 新增导出 API 带上 doc comment（说明**为什么**，不只是是什么）

## 发布

见 [RELEASING.md](RELEASING.md)：一个 tag 发三个 SDK（Go 靠 tag 本身，Python/Node 走 GitHub Actions）。
