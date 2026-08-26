# Sokel 插件 SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/sokel-dev/sokel-plugin-sdk.svg)](https://pkg.go.dev/github.com/sokel-dev/sokel-plugin-sdk)
[![PyPI](https://img.shields.io/pypi/v/sokel-plugin-sdk?label=pypi)](https://pypi.org/project/sokel-plugin-sdk/)
[![npm](https://img.shields.io/npm/v/@sokel-dev/plugin-sdk?label=npm)](https://www.npmjs.com/package/@sokel-dev/plugin-sdk)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[English](README.md) · 简体中文

用 **Go、Python 或 TypeScript** 写 [Sokel](https://github.com/sokel-dev) 插件。你只声明操作收什么、
回什么，注册、传输、凭证、文件传输、心跳重连全由 SDK 兜住。

```go
OnIssuesList(p, func(ctx sokel.Ctx, in *IssuesListIn) (*IssuesListOut, error) {
    issues, err := client.ListIssues(ctx, in.Project, in.State)
    if err != nil {
        return nil, err
    }
    return &IssuesListOut{Issues: issues, Count: len(issues)}, nil
})
```

这个 handler 的签名是**从你的声明生成的**。你的代码里不会出现任何 `map[string]any`，
也不存在第二份需要人肉同步的契约。

另外两种语言同样如此，只是声明写在语言中立的 `sokel.yaml` 里，而不是一个 Go 包：

```python
async def issues_list(ctx: Ctx, in_: IssuesListIn) -> IssuesListOut:
    issues = await client.list_issues(in_.project, in_.state)
    return IssuesListOut(issues=issues, count=len(issues))
```

```ts
onIssuesList(p, async (ctx, in_) => {
  const issues = await client.listIssues(in_.project, in_.state);
  return { issues, count: issues.length };
});
```

## 插件是出站拨入的

插件**主动连回平台**，不是反过来。不需要开放入站端口、不需要公网 IP、不需要在防火墙上开洞。
装在你家里 NAS 上的插件，和跑在云上的一样能被平台调用——这也是为什么「本地的编码 agent」
这种只能跑在你自己机器上的东西，也能做成插件。

## 选哪个 SDK

| 语言 | 安装 | 契约声明在 | 从哪开始 |
|---|---|---|---|
| Go | `go get github.com/sokel-dev/sokel-plugin-sdk` | `schema/` 包（Go builder） | 见下文 |
| Python | `pip install sokel-plugin-sdk` | `sokel.yaml` | [sdk-python/README.md](sdk-python/README.md) |
| TypeScript | `npm install @sokel-dev/plugin-sdk` | `sokel.yaml` | [sdk-node/README.md](sdk-node/README.md) |

三者说同一套 JSON-over-NATS 线协议，上报**同一份契约 JSON**：参考插件
[`examples/kitchen-sink`](examples/kitchen-sink) 的声明只有一份、实现有两份，
两边都要断言等于同一个 golden 文件——各 SDK 对协议的理解因此漂不开。

## 安装

库：

```bash
go get github.com/sokel-dev/sokel-plugin-sdk
```

`sokel-gen` 命令行工具——建插件骨架、从声明生成类型化代码：

```bash
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest
```

需要 Go 1.25 以上。也可以不装，直接用
`go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen`——`//go:generate` 里用的就是这个形态，
好处是版本由你的 `go.mod` 钉住，而不是由你上次装了哪个版本决定。

## 怎么工作

四步，顺序固定：**声明 → 生成 → 实现 → 连回平台**。

别从空目录开始，先要一个跑得通的骨架：

```bash
sokel-gen init ./my-plugin
cd my-plugin && go mod tidy && sokel-gen && go build ./...
```

它会建好 `schema/`、`main.go`、编译期 embed 的用户说明，以及给改代码的人和给用户的两份文档，
里面有一个真的、已经接通全链的操作。下面讲的就是 `init` 给你的东西——把它改成你自己的插件。

**1. 声明**契约——入参、出参、事件、凭证字段，都在 `schema/` 包里：

```go
package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/field"
)

type IssuesList struct{}

func (IssuesList) Meta() contract.Meta {
	return contract.Meta{ID: "issues_list", Label: "Issue 列表"}
}

func (IssuesList) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("project").Label("项目"),
		field.Enum("state",
			field.Opt("opened", "开着的"),
			field.Opt("closed", "已关闭")).Default("opened"),
	}
}

func (IssuesList) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.Array("issues", []Issue{}).Label("Issue 列表"),
		field.Int("count").Label("本页条数"),
	}
}
```

**2. 生成**类型化的 Go：

```go
//go:generate go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen
```

```bash
go generate ./...
```

产出 `zz_types.go`（各操作的 `In`/`Out` struct）与 `zz_register.go`（每个操作一个 `OnXxx` 函数）。
**别手改它们。**

契约是**编译期生成的，不是运行期反射**。声明写错在编译期就被拦住，而不是等到某次调用才发现。
`sokel-gen check` 校验生成物是不是最新的——接进 CI，因为「改了声明忘了重新生成」正是 codegen
最常见的失效方式。

**3. 实现** handler——签名完全具体，编译器替你检查。

**4. 连回平台**：

```go
p := sokel.New(sokel.Config{
	Endpoint: sokel.Env("ENDPOINT"),
	Token:    sokel.Env("TOKEN"),
	Name:     "my-plugin",
})
OnIssuesList(p, handleIssuesList)
log.Fatal(p.Run())
```

## 配置

SDK 只认 `SOKEL_` 前缀的环境变量：

| 变量 | 必填 | 含义 |
|---|---|---|
| `SOKEL_ENDPOINT` | 是 | `nats://broker:4222`，或一个 `https://` 平台地址（由它发现 broker） |
| `SOKEL_TOKEN` | 是 | 接入组 token，标识「插件 + 工作空间」 |
| `SOKEL_NATS_TOKEN` | 否 | broker 层鉴权，broker 要求时才配 |
| `SOKEL_NATS_CA` | 否 | `tls://` broker 的自定义 CA |
| `SOKEL_INSTANCE_ID` | 否 | 固定副本身份，重启后复用 |
| `SOKEL_REGION` | 否 | 副本的区域标签 |

**插件从不落地凭证**。每次调用由平台把解析好的字段随 payload 注入，用
`sokel.CredentialAs[T]` 类型化读取。

## 包

| 包 | 是什么 |
|---|---|
| `sokel` | 运行时：注册、分发、产出结果、文件、事件、webhook |
| `contract` | 契约类型——字段声明、元数据、凭证与事件的形状 |
| `contract/field` | 声明字段的 builder（`field.String`、`field.Enum` …） |
| `sokelgen` | `sokel-gen` 背后的代码生成器 |
| `cmd/sokel-gen` | 命令行工具，见下 |
| `pluginenv` | 读 `SOKEL_` 环境变量 |

## `sokel-gen` 命令行

| 命令 | 作用 |
|---|---|
| `sokel-gen` | 生成当前目录——`//go:generate` 用的就是这个形态 |
| `sokel-gen init <目录>` | 建一个开箱即跑的插件骨架（`-lang go｜python｜ts`） |
| `sokel-gen generate [目录...]` | 生成；给的目录下有多个插件时自动全扫（`-lang ts｜python` 指定 manifest 插件的目标） |
| `sokel-gen check [目录...]` | 只校验生成物是否最新，不写文件——CI 用 |
| `sokel-gen export <json\|yaml\|ts\|python> [目录]` | 把契约导成别的形态 |
| `sokel-gen migrate [目录]` | 把旧的 struct+tag 插件转成 `schema/` 声明 |
| `sokel-gen docs [主题]` | 印出 `sokel.yaml` 写法说明 / JSON Schema / 参考声明 |
| `sokel-gen example [语言]` | 印出参考插件：声明、Python 实现、TypeScript 实现 |

`generate` 与 `check` 支持 `-schema <名>`，用于声明包不叫 `schema` 的情况。

插件是**按有没有 `schema/` 目录或 `sokel.yaml`** 发现的，不是靠读 `//go:generate` 指令。这个区别很要紧：
`go generate ./...` 会**静默跳过**漏写指令的插件，而被跳过的插件契约会一路漂移、没有任何红。
第一方插件里就有四个曾长期处于这个状态。

```bash
sokel-gen check ./plugins        # 一条命令扫完该目录下所有插件
```

`check` 会**跑完全部再报**，CI 里一次看清所有过期的插件，而不是修一个跑一轮。

### 文档编在二进制里

写法说明、JSON Schema、以及一份覆盖全部契约形态的参考声明都**编进了 `sokel-gen`**，
不需要检出仓库、也不需要联网：

```bash
sokel-gen docs        # sokel.yaml 怎么写
sokel-gen example     # 一份用满全部形态的真实声明，照着改
```

这主要是给 AI 用的：把四条命令交给它（`docs` → `example` → `init -lang python|ts` →
`generate`）就足以写出一个能跑的插件，而 `generate` 会把声明里的问题一次报全。

## 示例

| 示例 | 演示什么 |
|---|---|
| [`examples/sysinfo`](examples/sysinfo) | 一个完整的 Go 插件：两个操作、文件入参、embed 的用户说明 |
| [`examples/kitchen-sink`](examples/kitchen-sink) | 全部契约形态各一份——声明只有一份，Python 与 TypeScript 各实现一遍，都对同一个 golden 断言 |

```bash
cd examples/sysinfo
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx go run .
```

## 一份声明，多种产出

契约有两条等价入口，产出同一份**语言中立的中间表示**：

```
schema/ 包（Go builder）──┐
                          ├──▶ IR ──┬──▶ 类型化 Go      zz_types.go / zz_register.go
sokel.yaml（语言中立）────┘          ├──▶ 类型化 Python  sokel_gen.py（pydantic 模型）
                                     ├──▶ 类型化 TS      sokel.gen.ts（interface）
                                     ├──▶ export json   契约本身
                                     └──▶ export yaml   反向：Go 声明 → sokel.yaml
```

Go 插件用 `schema/` 包：契约是可执行的 Go 代码，方法名写错即编译失败，还能复用已有的 Go 类型。
Python / TypeScript 插件用 `sokel.yaml`：声明几个字段不该以「先学一遍 Go builder 的 API」为前提。

```bash
sokel-gen init -lang python ./my-plugin   # 或 -lang ts
sokel-gen generate ./my-plugin            # sokel.yaml → 类型化模型与注册口
sokel-gen export yaml ./plugins/gitlab    # 反向：Go 声明 → sokel.yaml
```

格式见 [docs/manifest.md](docs/manifest.md)。YAML 与 JSON 是**同一种格式**（同一条解析路径），
未知键当场报错，而不是静默丢掉一个字段。

导出的 JSON **刻意不带 Go 类型名**——它承载的是契约，不是 Go 的实现细节。
这一点之所以成立，是因为线协议本身就是 **NATS 上的 JSON**、字节走 base64：没有 gob、没有
protobuf、没有任何 Go 专属编码。**这个 SDK 是该协议的一个实现，而不是协议的定义。**
剩下的目标是 Rust，而新增一个语言 = 在现有 IR 上加一个渲染器 + 一份运行时，不是再写一个解析器。

## 发布

**一个 tag 发三个 SDK**，版本一致——Go 靠 tag 本身，Python 与 Node 走
[`.github/workflows/release.yml`](.github/workflows/release.yml)。
完整步骤与一次性的仓库配置见 [RELEASING.md](RELEASING.md)。

```bash
# 改 sdk-node/package.json 与 sdk-python/pyproject.toml 的版本号，然后
git tag v0.3.0 && git push origin main --tags
```

流水线里每一道闸都对应一种**发出去才会发现**的失效：版本与 tag 不一致、生成物过期、
漏了构建步骤导致发出一个空包（装上去照样成功，import 才炸）。

## 现状

Sokel 平台本身尚未开源。在那之前，这个 SDK 可以用来了解插件模型、提前把插件写好——
但插件需要一个跑着的 Sokel 实例才能拨进去。

## 许可证

Apache-2.0，见 [LICENSE](LICENSE)。
