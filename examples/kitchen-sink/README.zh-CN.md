# kitchen-sink（参考插件）

> 给**改代码的人**看的。给用户看的在 [`docs/kitchen-sink.md`](docs/kitchen-sink.md)。

一份声明、两种实现。它同时是三件东西：

1. **语法参考** —— 每种字段形态、每种可选能力都在 [`manifest.yml`](manifest.yml) 里出现一次；
2. **一致性套件** —— `python/` 与 `node/` 各实现一遍，两边上报的契约必须等于
   [`contract.golden.json`](contract.golden.json)，三处断言盯着（Go / Python / Node）；
3. **新插件的起点** —— 删掉用不上的部分即可。

## 结构

| 路径 | 作用 |
|---|---|
| `manifest.yml` | 契约声明（语言中立）。**改这里** |
| `contract.golden.json` | 契约的 golden。改了声明要一并更新（见下） |
| `docs/kitchen-sink.md` | 给用户的说明，生成时内联进两个生成物 |
| `python/main.py` | Python 实现；`python/sokel_gen.py` 是生成物，**别手改** |
| `node/src/main.ts` | Node 实现；`node/src/sokel.gen.ts` 是生成物，**别手改** |

## 改了声明之后

```bash
sokel-gen generate ./examples/kitchen-sink
sokel-gen export json ./examples/kitchen-sink > examples/kitchen-sink/contract.golden.json
```

两条都跑完再提交。`sokel-gen check ./examples` 是 CI 那一道——
「改了声明忘了重新生成」是 codegen 最常见的失效方式，而它不会有任何运行时症状。

## 跑起来

```bash
# Python
cd python && pip install -r requirements.txt
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx python main.py

# Node
cd node && npm install && npm run build
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx npm start
```

## 一致性套件跑在哪

| 语言 | 断言 |
|---|---|
| Go | `sokelgen.TestKitchenSink_MatchesGolden` —— 声明 → 契约 == golden |
| Python | `sdk-python/tests/test_register_payload.py` —— 生成物内嵌的 CONTRACT == golden |
| Node | `node/test/contract.test.ts` —— 同上，外加认证流保留操作在不在契约里 |

目标不是「保证同一个插件的多语言实现一致」（那种需求很少），而是
**保证各 SDK 对协议的理解一致**——一个参考插件就够，不必每个插件都跨语言。
