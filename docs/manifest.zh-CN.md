# `manifest.yml` —— 语言中立的契约声明

一个插件的契约可以从两条入口声明，产出**同一份 IR**：

```
schema/ 包（Go builder）──┐
                          ├─▶ IR ─▶ 渲染 Go / TypeScript / Python
manifest.yml（本文）────────┘
```

Go 插件用 `schema/` 包：契约是可执行的 Go 代码，方法名写错即编译失败，还能复用已有的 Go 类型。
Python / Node 插件用 `manifest.yml`：声明几个字段不该以「先装一套 Go 工具链去读 builder 的 API」为前提。

顶层那几个键跟着协议文档写 snake_case（`events_common` / `doc_url`），字段里的键跟着
协议 §5 的 Field 写 camelCase（`valueType` / `oneOf` / `itemType` / `timeoutSec`）——
两种拼法都认（`eventsCommon` 与 `events_common` 等价），照着协议抄一行下来不会撞上「unknown field」。

YAML 与 JSON 是**同一种格式**（`manifest.json` 一样认）：YAML 先转成 JSON 再按同一组规则解码，
所以不存在「YAML 支持而 JSON 不支持」的键。解码时**未知键当场报错**——
拼错的 `lable:` 不会被静默丢掉，那正是声明式格式最典型的失效方式。

## 离线拿到这份说明

文档、Schema 与参考声明都编进了 `sokel-gen` 二进制，不需要这个仓库、也不需要联网：

```bash
sokel-gen docs            # 本文
sokel-gen docs schema     # JSON Schema
sokel-gen example         # 覆盖全部形态的参考声明（照着改）
sokel-gen example python  # 与那份声明配套的 Python 实现
sokel-gen example node    # 配套的 TypeScript 实现
```

让 AI 自己写插件时给它这四条就够：`docs` 查写法 → `example` 对照 →
`init -lang python|ts` 建骨架 → `generate` 生成并校验（声明有问题会一次报全）。

## 编辑器补全与校验

本目录的 [`sokel.schema.json`](sokel.schema.json) 是这份格式的 JSON Schema。
在 `manifest.yml` 第一行挂上它，VS Code（YAML 扩展）与 JetBrains 就会**边写边校验**：
键名补全、枚举候选、拼错当场标红——而不是等跑 `sokel-gen` 才知道。

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/sokel-dev/sokel-plugin-sdk/main/docs/sokel.schema.json
```

`sokel-gen init` 生成的骨架已经带了这一行。离线或想钉住版本时，把 URL 换成本地相对路径即可。

Schema 与解析器是同一格式的两份定义，所以有一条测试盯着它们不漂
（`TestJSONSchemaMatchesParser`：解析器认的键 schema 必须有、schema 列的键解析器必须认）。

## 文件位置

`sokel-gen` 按目录发现插件，判据是「目录里有 `schema/` 子目录，**或**一份 `manifest.yml`」。
候选文件名按序为 `manifest.yml` / `manifest.yml` / `manifest.json`；同时存在多份会报错（多半是改名没删干净）。

## 骨架

```bash
sokel-gen init -lang python ./my-plugin   # 或 -lang ts
cd my-plugin && sokel-gen generate .
```

## 顶层结构

```yaml
plugin:                 # 身份与说明书
  name: gitlab
  label: GitLab
  desc: 仓库、MR、Issue、CI
  version: 1.0.0
  doc: docs/gitlab.md   # 使用说明 markdown 的**路径**（相对本文件），生成时内联进生成物
  doc_url: https://…    # 已有文档站时用它，别抄一份进来

capabilities:           # 可选能力自报：同一个操作「做到什么程度」
  recency: false

credential:             # 凭证契约 + 凭证是怎么拿到的
  auth: { kind: input }
  fields: [ <Field>… ]

events_common: [chat_id]  # 每个事件都有的字段，触发时平铺到输入顶层
events:                   # 事件契约
  - { id: message, label: 收到消息, fields: [ <Field>… ] }

operations:               # 操作契约
  - id: issues_list
    label: Issue 列表
    desc: …
    stream: false         # 流式：逐帧回复
    timeoutSec: 120       # 建议超时；重活务必声明，平台默认只有 60s
    inputs: [ <Field>… ]
    outputs: [ <Field>… ]

codegen:                  # 生成目标；可以是一个，也可以是一组
  - { lang: python, out: sokel_gen.py }
  - { lang: ts, out: src/sokel.gen.ts }
```

`plugin.label` / `desc` / `version` 不进注册握手（平台的展示名来自插件目录），
但会进生成物：**声明了的东西必须在某处看得见**，否则改了它连 `sokel-gen check` 都不会红。
`version` 另有实效——生成的 `new_plugin()` / `newPlugin()` 拿它作副本自报的版本。

一个值得知道的后果：平台判断「有新版本」用的是目录版本与已装/自报版本的**逐字符相等比较**，
不是 semver 排序。所以版本串在所有出现处（manifest、发行 manifest、手动 `SetVersion`）必须
逐字节一致——一处发 `v1.0.1`、另一处发 `1.0.1`，所有部署都会常亮一个怎么更新都消不掉的
「可更新」徽标。

## Field

字段形态与线协议 §5 一一对应：

| 键 | 说明 |
|---|---|
| `name` | 契约名，也是运行值里的键。**改名等于换字段**（画布里的引用会断） |
| `label` / `desc` | 显示名与说明；`desc` 在 opaque 时是**必填的理由** |
| `type` | `string` / `text` / `number` / `boolean` / `file` / `json` / `array` / `enum` / `secret` |
| `required` | 必填。生成的类型据此决定「有没有默认值」 |
| `default` | 默认值（给了默认就不是必填） |
| `options` | `enum` 的候选：裸字符串，或 `{value, label}`（值是代码、人看不懂时才给显示名） |
| `fields` | `json` 的子字段 / `array` 的元素字段（递归） |
| `valueType` | 动态键：键运行期才知道、值类型统一。与 `fields` **互斥** |
| `itemType` | 数组元素的标量类型（`string` / `number` / `boolean` / `file`） |
| `goType` | 给这个结构**起个名字**；同名再次出现时可省略 `fields`，直接引用（见下） |
| `opaque` | 声明「无结构」。只有 `json` / `array` 能标，且**必须写 `desc` 说明理由** |
| `oneOf` | 结构联合：接受列出的几种结构之一 |
| `types` | 标量联合（如 `number｜string`）：变量绑定接受其中任一 |

### 书写糖

| 写法 | 等价于 |
|---|---|
| `type: int` | `type: number` + `goType: int`（生成 `int` 而不是浮点） |
| `type: files` | `type: array` + `itemType: file` |
| `type: strings` | `type: array` + `itemType: string` |
| `type: ints` | `type: array` + `itemType: number` + `goType: int` |

### 结构声明一次，之后按名字引用

```yaml
inputs:
  - { name: profile, type: json, goType: Profile, fields: [ { name: nick, type: string } ] }
outputs:
  - { name: profile, type: json, goType: Profile }     # 不必把字段抄第二遍
```

抄第二遍才是风险：两份会漂，而漂了之后平台看到的是两个同名、同形状、内容却不同的结构。
引用了一个谁也没定义过的名字会**报错**，不会生成一个空壳类型。

### `opaque` 必须给理由

```yaml
- name: extra
  type: json
  opaque: true
  desc: 调用方透传，形状由上游决定
```

「图省事」与「确实没有结构」在文件里长得一模一样，理由是唯一能把两者分开的东西。
没有 `desc` 的 `opaque` 直接判错。

### `oneOf`：运行值就是分支本身

```yaml
- name: doc
  type: json
  oneOf:
    - { name: DocObject, type: json, fields: [ { name: title, type: string, required: true } ] }
    - { name: Block, type: array, fields: [ { name: kind, type: string, required: true } ] }
```

不带 discriminator 包装——否则下游的引用路径要多一层。
生成的类型是 `Union[DocObject, List[Block]]`（Python）/ `DocObject | Block[]`（TS），
由 handler 自己按形状判别。

## 事件与公共字段

```yaml
events_common: [chat_id]
events:
  - { id: message,   fields: [ { name: chat_id, type: string, required: true }, … ] }
  - { id: heartbeat, fields: [ { name: chat_id, type: string, required: true }, … ] }
```

公共字段必须在**每个**事件里都存在且类型一致，否则生成期报错。
不做「取交集」的推断：新增一个事件少写了某字段，公共字段就会悄悄缩水，存量工作流跟着断——
而那时没人会想到是这里。也不能与保留字（`_event` / `event` / `input` / `credential_id`）或事件 id 撞名。

## 凭证与认证

```yaml
credential:
  auth: { kind: qr }                                   # 或 input / oauth
  fields:
    - { name: api_key, label: API Key, type: secret, required: true }
```

`kind` 决定**步骤**，不需要也不允许手写：

| kind | 步骤 | 谁实现 |
|---|---|---|
| `qr` | start + poll | 插件（扫码出题、轮询确认） |
| `input` | start + poll + submit | 插件（多一步用户回填） |
| `oauth` | 无 | **平台代答**（client_secret 在平台手里，插件构造不出同意页地址） |

`kind: oauth` 时必须给 `provider`，可给 `scopes`。
声明了 auth，契约里会自动多出 `auth.start` / `auth.poll` / `auth.submit` 三个内部操作——
平台面板按契约构造请求，缺了它面板就不知道该发什么参数。
业务操作 id 限定 `^[a-z][a-z0-9_]*$`，带点号的命名空间归平台，撞不上。

### `health_check`：约定 id 的凭证体检

凭证会失效（密钥被撤销、cookie 过期）。平台不猜，它调一个**约定 id** 的操作来问插件：

```yaml
- id: health_check    # 凭证页的「测试」与「检查凭证」调的就是它
  label: 凭证体检
  inputs: []
  outputs:
    - { name: ok, label: 可用, type: boolean, required: true }
    - { name: message, label: 说明, type: string }
```

**不可用要返回 `ok=false`，不是抛错**——抛错的话平台只能说「调用失败」，而
「密钥过期，去重新授权」与「连不上，去查网络」是两条完全不同的处理路径。
没声明这个操作也可以，只是那个插件的凭证只能靠实际调用来发现失效。

### `list_models`：约定 id 的上游模型列表

LLM 系插件可以用另一个**约定 id** 回答「这条凭证真正看得到哪些模型」——平台「部署编辑」的
模型名下拉调的就是它：

```yaml
- id: list_models     # 部署编辑的模型名下拉调的就是它
  label: 上游模型列表
  internal: true
  inputs: []
  outputs:
    - name: models
      label: 模型列表
      type: array
      required: true
      fields:
        - { name: id, label: 模型 id, type: string, required: true }
        - { name: label, label: 展示名, type: string }
```

返回请按 id 稳定排序——下拉每次同步都会重渲染，顺序乱跳会被读成「上游变了」。
没有列表端点的厂商也应声明该操作、运行时返回明确报错，而不是不声明：
契约在场与否正是平台判断「该不该显示下拉」的依据。

## 生成与校验

```bash
sokel-gen generate ./my-plugin      # 按 codegen 生成（可多目标）
sokel-gen generate -lang ts .       # 只生成某一种
sokel-gen check ./plugins           # CI：改了声明没重新生成就红
sokel-gen export json ./my-plugin   # 契约本身（语言中立）
sokel-gen export yaml ./go-plugin   # 反向：Go 的 schema/ 声明 → manifest.yml
```

`export yaml` 是给「用另一种语言实现同一个插件」用的：拿第一方 Go 插件的声明直接开工，
不必读 Go 代码，也不必让 Go 那份声明事实上成为标准。

## 一份覆盖全部形态的例子

[`examples/kitchen-sink/manifest.yml`](../examples/kitchen-sink/manifest.yml) 把上面每一种形态都用了一遍，
并在 `python/` 与 `node/` 各实现了一遍——两边上报的契约必须等于同一份
[`contract.golden.json`](../examples/kitchen-sink/contract.golden.json)，有测试盯着。
