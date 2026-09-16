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

`.claude/skills/sokel-plugin-dev/` 是给编码 agent 的 skill：干活的顺序（起壳 → 声明 → 生成 →
实现 → 用 manifest 装进平台 → 跑起来），以及那些**坏了也不报错**的规矩。在本仓里干活的 agent
会自动加载它；它依赖的四条命令（`sokel-gen docs` / `example` / `init` / `generate`）
`sokel-gen help` 也会打印，所以只拿到二进制的 agent 同样走得通。

流程变了就同步它，与 `docs/manifest.md`、平台侧的插件开发文档保持一致。

## 提 PR 之前

- [ ] `go build ./...`、`go test ./...`、`gofmt -l .` 三项皆过
- [ ] 改了线协议形状？同步更新 `docs/` 里的对应说明
- [ ] 新增导出 API 带上 doc comment（说明**为什么**，不只是是什么）

## 发布

见 [RELEASING.md](RELEASING.md)：一个 tag 发三个 SDK（Go 靠 tag 本身，Python/Node 走 GitHub Actions）。
