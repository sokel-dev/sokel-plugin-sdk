# Sokel Plugin SDK — Node.js / TypeScript

用 TypeScript 写 [Sokel](https://github.com/sokel-dev) 插件。契约写在 `manifest.yml` 里（语言中立），
`sokel-gen` 把它生成成 TS 接口与类型化的注册口；SDK 负责注册、传输、凭证、文件、心跳与重连。

```ts
onIssuesList(p, async (ctx, in_) => {
  const issues = await client.listIssues(in_.project, in_.state);
  return { issues, count: issues.length };
});
```

`in_.project` 拼错是编译错误，不是线上的一次失败调用——代码里没有任何 `any`。

## 装

```bash
npm install @sokel-dev/plugin-sdk
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest   # 生成器
```

`sokel-gen` 是个单文件二进制（Go 写的），只在**生成时**用到；跑插件时不需要它。

## 四步

```bash
sokel-gen init -lang ts ./my-plugin
cd my-plugin
npm install
sokel-gen generate .     # manifest.yml → src/sokel.gen.ts
npm run build && npm start
```

1. **声明** —— `manifest.yml`：操作、事件、凭证、认证方式。格式见 [docs/manifest.md](../docs/manifest.md)。
2. **生成** —— `sokel-gen generate .` 产出 `src/sokel.gen.ts`：每个操作一对 `XxxIn` / `XxxOut` 接口
   和一个 `onXxx(p, fn)`；每个事件一个 payload 接口和一个 `triggerXxx(ctx, eventId, payload)`。
3. **实现** —— handler 签名完全具体，返回值可以是对象也可以是 Promise。
4. **连接** —— `await p.run()`。插件**出站**连平台：无入站端口、无公网 IP、无防火墙洞。

## 为什么不是 zod

zod schema 是运行时对象：用它声明契约意味着「契约只有跑起来才知道」，而且每种语言都得
自己解释一遍那套 DSL。声明留在 `manifest.yml`，TS 这边只要类型——类型在编译期，运行时零开销。

## 能力一览

| 要做的事 | 怎么写 |
|---|---|
| 读凭证 | `ctx.credentialAs<Credential>()` |
| 取入参文件的字节 | `await ctx.fetch(in_.file)` |
| 产出文件 | `await ctx.upload(name, mime, bytes)` → 放进出参 |
| 流式产出 | `out.text(...)` 逐帧给人看，`out.vars({...})` 给下游 |
| 推事件 | `await triggerMessage(ctx, eventId, {...})` |
| 常驻事件源 | `p.registerSource(id, label, fn)`，循环里判 `ctx.stopped` |
| 平台代收 webhook | `p.registerWebhook(fn)`，返回 `ok()` / `text(401, "...")` |
| 协作式认证 | `p.registerAuth({ start, poll, submit })` |
| 会话型凭证刷新 | `await ctx.updateCredential({ session: "…" })` |
| 自报运行态 | `ctx.reportStatus("auth_required", "…")` |

## 配置

SDK 读 `SOKEL_` 前缀的环境变量：

| 变量 | 必填 | 含义 |
|---|---|---|
| `SOKEL_ENDPOINT` | 是 | `nats://broker:4222`，或 `https://` 平台地址（经 `/connect-info` 发现 broker） |
| `SOKEL_TOKEN` | 是 | 接入组 token（`skp_…`），平台据此认「插件 + 工作空间」 |
| `SOKEL_NATS_TOKEN` | 否 | broker 的传输层鉴权 |
| `SOKEL_NATS_CA` | 否 | `tls://` broker 的自定义 CA |
| `SOKEL_INSTANCE_ID` | 否 | 固定副本身份（默认按 token 指纹落盘复用） |
| `SOKEL_REGION` | 否 | 副本的地域标注 |

凭证从不由插件存储：平台随每次调用把解析后的字段下发下来。

## 例子

[`examples/kitchen-sink`](../examples/kitchen-sink) 覆盖了全部形态——每种字段、文件、流式、
事件、webhook、协作式认证各一份，Node 与 Python 实现的是同一份声明。

```bash
cd examples/kitchen-sink/node
npm install && npm run build
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx npm start
```

## 开发本 SDK

```bash
pnpm install
pnpm test        # tsc + node --test
```

## License

Apache-2.0.
