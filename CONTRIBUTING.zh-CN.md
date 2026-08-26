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

## 提 PR 之前

- [ ] `go build ./...`、`go test ./...`、`gofmt -l .` 三项皆过
- [ ] 改了线协议形状？同步更新 `docs/` 里的对应说明
- [ ] 新增导出 API 带上 doc comment（说明**为什么**，不只是是什么）

## 发布

见 [RELEASING.md](RELEASING.md)：一个 tag 发三个 SDK（Go 靠 tag 本身，Python/Node 走 GitHub Actions）。
