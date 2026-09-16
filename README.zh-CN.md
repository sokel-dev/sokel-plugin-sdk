# Sokel 插件 SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/sokel-dev/sokel-plugin-sdk.svg)](https://pkg.go.dev/github.com/sokel-dev/sokel-plugin-sdk)
[![PyPI](https://img.shields.io/pypi/v/sokel-plugin-sdk?label=pypi)](https://pypi.org/project/sokel-plugin-sdk/)
[![npm](https://img.shields.io/npm/v/@sokel-dev/plugin-sdk?label=npm)](https://www.npmjs.com/package/@sokel-dev/plugin-sdk)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[English](README.md) · 简体中文

用 **Go、Python 或 TypeScript** 写 [Sokel](https://github.com/sokel-dev) 插件，让别人在工作流画布上
拖出你的操作。

插件就是一个应答调用的进程。你声明每个操作收什么、回什么；注册、传输、凭证下发、文件传输、
心跳重连全由 SDK 兜住。你写的就是真正属于你的那部分——调你要接的那个服务。

```go
OnIssuesList(p, func(ctx sokel.Ctx, in *IssuesListIn) (*IssuesListOut, error) {
    issues, err := client.ListIssues(ctx, in.Project, in.State)
    if err != nil {
        return nil, err
    }
    return &IssuesListOut{Issues: issues, Count: len(issues)}, nil
})
```

这个签名是**从你的声明生成的**。你的代码里不会出现 `map[string]any`，不用手写入参解析，
也没有第二份契约要人去同步。Python 与 TypeScript 是同一套做法——同样的生成形状、同样的保证，
只是语法不同。

## 它为什么是这个样子

**契约是声明出来的，不是运行期反射出来的。** 有哪些操作、每个操作的字段、有哪些事件、凭证要填
什么，都先写下来再变成代码。声明写错是编译失败，而不是等某次调用时才冒出来——那时你早已不在
现场。平台照着同一份声明渲染画布，所以用户配的东西与你 handler 收到的东西不可能对不上。

**插件是出站拨入的。** 插件连平台，不是反过来——不需要开入站端口、不需要公网 IP、不用在防火墙上
开洞。装在你地下室 NAS 上的插件，平台调起来和装在云上的一模一样；这也正是「跑在你自己笔记本上的
编码 agent」这种天生本地的东西也能做成插件的原因。

**契约与语言无关。** 三个 SDK 说的是同一套 JSON-over-NATS 协议，上报同一份契约 JSON。
参考插件 [`examples/kitchen-sink`](examples/kitchen-sink) 声明一次、每种语言各实现一遍，
全部对着同一份 golden 断言——所以几个 SDK 不可能在「怎么理解协议」上悄悄跑偏。
**本 SDK 是那套协议的一个实现，不是协议本身。**

## 让 agent 替你写插件

写插件这件事的形状特别适合交给编码 agent：每一步都有命令、有报错、有可验证的结果。
为此准备了两样东西。

**一份可安装的 skill。** [`skills/sokel-plugin-dev/`](skills/sokel-plugin-dev) 独立完整，
不绑定某一家 agent 产品——入口是 `SKILL.md`，references 覆盖工具链、manifest 格式、怎么装进平台，
以及那些**坏了也不报错**的规矩。把这个目录拷进你的 agent 放 skill 的地方即可；Claude Code 就是
`~/.claude/skills/` 或项目里的 `.claude/skills/`。它的 references 大部分由本仓文档生成、并由 CI
盯着，所以你装到的那份不会落后于工具链。

**不想装就给它四条命令。** agent 能跑命令、能读输出，但常常既没有这个仓库、也上不了 GitHub。
所以格式说明、JSON Schema 与一份覆盖全部契约形态的参考声明，都编进了 `sokel-gen` 二进制：

```bash
sokel-gen docs                            # manifest.yml 怎么写
sokel-gen example                         # 覆盖每一种形态的参考声明
sokel-gen init <目录> -lang python|ts|go    # 起一个当场就能跑的壳
sokel-gen generate <目录>                  # 生成类型化外壳，问题一次全报
```

有两句话值得写进 prompt——agent 默认就会做错，而且做错了没人吭声：

- **夹具必须是真抓的。** 自己造的夹具会按 agent 对这个 API 的理解长，所以它永远是绿的。
- **契约里没有的字段到不了 handler，而且不报错。** 症状是「我传了，没生效」——插件最典型的
  静默失效方式。

## 怎么声明契约

两条入口，对 Go 来说**哪条都不是「正路」**：

| 你写的是 | 怎么生成 | 什么时候选它 |
|---|---|---|
| `manifest.yml` | `sokel-gen generate -lang go\|python\|ts <目录>` | 就一个语言中立的文件，还正好是你要发布的那份。**三种语言都能用** |
| `schema/` 包 | `sokel-gen generate <目录>` | 只有 Go 有：契约是可执行的 Go，方法名写错即编译失败，已有的 Go 类型直接复用 |

Go 这边**两条路生成出来的 API 一模一样**——`OnXxx` / `RegisterCredential` / `DeclareEvents` /
`TriggerXxx`——所以你的实现看不出契约是哪条路来的，插件在两者之间搬家，实现一行都不用动。
反方向用 `sokel-gen export yaml`：把 `schema/` 包导成 manifest。

没有特别理由就**默认写 manifest**：一个文件、正好是要发布的那份，而且插件在语言之间移植时声明
不用重写。格式见 [docs/manifest.md](docs/manifest.md)——YAML 与 JSON 是**同一种格式**、走同一条
解析路径，未知键当场报错而不是被静默丢掉。

## 上手

```bash
sokel-gen init ./my-plugin                 # 或 -lang python|ts，或 -lang go -manifest
cd my-plugin && sokel-gen generate . && go build ./...
```

出来的东西**当场就能跑通**：里面那个 `hello` 是真操作，不是占位注释，而且两份文档都给你备好了。
从一个能跑的东西开始，省掉一整轮「这几块到底怎么拼」的试错。

往后就是这个循环：改声明 → 重新生成 → 实现 → 跑。CI 里跑的是 `sokel-gen check`：
改了声明却没重新生成，它会红——而这正是 codegen 最常见的失效方式，且运行期一点症状都没有。

插件需要一个平台来拨入。在平台上建行、拿接入 token、把进程跑起来，见 skill 里的
[`references/platform.md`](skills/sokel-plugin-dev/references/platform.md)。

## 安装

`sokel-gen` **只在写插件时用得上**——跑插件的机器永远不需要它，不管你用哪种语言。
**预编译二进制不需要 Go 环境**：写 Python / TypeScript 插件的人，契约就是一个 YAML 文件，
不该为它先装一套 Go 工具链。从本仓 releases 取对应平台的压缩包
（darwin / linux / windows × amd64 / arm64），把 `sokel-gen` 放进 `PATH`，
`sokel-gen version` 能确认装对了。

已经有 Go 1.23+ 的话，`go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest`
等效；而 `go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen`——`//go:generate` 用的那种
形态——好处是版本由你的 `go.mod` 钉住，而不是由你上次装了哪个版本决定。

库本身：

| 语言 | 装 | 从哪开始 |
|---|---|---|
| Go | `go get github.com/sokel-dev/sokel-plugin-sdk` | 本页 |
| Python | `pip install sokel-plugin-sdk` | [sdk-python/README.md](sdk-python/README.md) |
| TypeScript | `npm install @sokel-dev/plugin-sdk` | [sdk-node/README.md](sdk-node/README.md) |

## 配置

全部来自 `SOKEL_` 前缀的环境变量。`SOKEL_TOKEN` / `SOKEL_DEPLOY_KEY` / `SOKEL_ACCESS`
**必须恰好设一个**——它们是证明同一件事的三种方式。

| 变量 | 必需 | 含义 |
|---|---|---|
| `SOKEL_ENDPOINT` | 是 | 平台的 `https://` 地址。SDK 从它发现 broker 凭据，broker 换了地方还能重新发现。直接写 `nats://broker:4222` 仍然认（旧形态），但就没有重新发现了 |
| `SOKEL_TOKEN` | 三选一 | 接入组 token（`skp_…`），标识「哪个插件 + 哪个空间」 |
| `SOKEL_DEPLOY_KEY` | 三选一 | 随平台发行的容器用的零接触接入：开机自己 enroll、自己换出接入 token |
| `SOKEL_ACCESS` | 三选一 | 平台导出的离线接入包，给完全够不着平台 HTTP 端点的副本用，整个跳过发现 |
| `SOKEL_NATS_CA` | 否 | `tls://` broker 的自定义 CA |
| `SOKEL_INSTANCE_ID` | 否 | 把副本身份钉住，重启不变 |
| `SOKEL_REGION` | 否 | 副本列表里显示的区域标签 |
| `SOKEL_VERSION` | 否 | 副本自报版本的兜底值 |

**部署配置放这儿，凭证不放。** 凭证由平台随每次调用下发，插件从不保存。分辨的判据：
这个值换一台机器部署同一个插件，还成立吗？不成立就是环境的事。

## 工具链

| 命令 | 干什么 |
|---|---|
| `sokel-gen` | 对当前目录生成——`//go:generate` 用的就是这个形态 |
| `sokel-gen init <目录>` | 起一个当场能编译能跑的插件（`-lang go｜python｜ts`、`-manifest`） |
| `sokel-gen generate [目录...]` | 生成；给一个装着很多插件的目录会自动逐个走 |
| `sokel-gen check [目录...]` | 只校验生成物是不是最新的，什么都不写——给 CI 用 |
| `sokel-gen export <json\|yaml\|ts\|python> [目录]` | 把契约导成另一种形态 |
| `sokel-gen migrate [目录]` | 把老的 struct+tag 插件转成 `schema/` 声明 |
| `sokel-gen docs [主题]` | `manifest.yml` 格式说明 / JSON Schema / 参考声明 |
| `sokel-gen example [语言]` | 参考插件的声明与各语言实现 |
| `sokel-gen version` | 这是哪个版本的工具链 |

插件是**按「有没有 `schema/` 目录或 `manifest.yml`」发现的**，不是读 `//go:generate` 那行——
漏写一条指令，`go generate ./...` 会静默跳过那个插件，它的契约就此漂移而没有任何东西会红。
本仓有四个第一方插件曾长期处在这个状态，直到加上这条判据。`check` 会**跑完所有插件再报告**，
所以一次 CI 就能看到全部过期的那些，而不是修一个跑一次。

## 包

| 包 | 是什么 |
|---|---|
| `sokel` | 运行时：注册、分发、产出结果、文件、事件、Webhook |
| `contract` | 契约类型——字段规格、元信息、凭证与事件形状 |
| `contract/field` | 声明字段的 builder（`field.String`、`field.Enum`…） |
| `sokelgen` | `sokel-gen` 背后的代码生成器 |
| `cmd/sokel-gen` | 命令行 |
| `pluginenv` | 读 `SOKEL_` 那些环境变量 |

## 示例

| 示例 | 展示什么 |
|---|---|
| [`examples/sysinfo`](examples/sysinfo) | 一个完整的 Go 插件：两个操作、一个文件入参、内嵌的用户说明 |
| [`examples/kitchen-sink`](examples/kitchen-sink) | 一次覆盖全部契约形态——声明一次，Go、Python、TypeScript **各实现一遍**，全部对着同一份 golden 断言 |

## 一份声明，多种产出

```
schema/ 包（Go builder）────────┐
                                ├──▶ IR ──┬──▶ 类型化 Go      zz_types.go / zz_register.go / …
manifest.yml（语言中立）────────┘         ├──▶ 类型化 Python  sokel_gen.py（pydantic 模型）
                                          ├──▶ 类型化 TS      sokel.gen.ts（interface）
                                          ├──▶ export json   契约本身
                                          └──▶ export yaml   从 Go 声明导出的 manifest
```

导出的 JSON **刻意不带 Go 类型名**：它携带的是契约，不是实现细节。线协议是 JSON over NATS、
字节走 base64——没有 gob、没有 protobuf、没有任何 Go 特有的东西。剩下的目标是 Rust SDK，
而加一个语言是「在现有 IR 上加个渲染器 + 一个运行时」，不是再写一个解析器。

## 发版

一个 tag 同时发三个 SDK 与 `sokel-gen` 二进制：Go 靠 tag 本身，Python 与 TypeScript 走
[`.github/workflows/release.yml`](.github/workflows/release.yml)。步骤与一次性的 registry
配置见 [RELEASING.md](RELEASING.md)。

那条流水线上的每一道闸，都对应一种**发出去之后才会暴露**的故障：tag 与包的版本对不上、
生成物是旧的、某个包漏了构建步骤于是发了个空壳。npm 与 PyPI 都**不允许删版本**，
发坏了只能再发一个盖住它。

## 状态

Sokel 平台本身还没有开源。在那之前，这个 SDK 可以用来读懂插件模型、把插件先写好——
但插件要跑起来，需要一个在运行的 Sokel 实例可供拨入。

## 许可

Apache-2.0，见 [LICENSE](LICENSE)。
