# Sokel Plugin SDK — Python

用 Python 写 [Sokel](https://github.com/sokel-dev) 插件。契约写在 `sokel.yaml` 里（语言中立），
`sokel-gen` 把它生成成 pydantic 模型与类型化的注册口；SDK 负责注册、传输、凭证、文件、心跳与重连。

```python
async def issues_list(ctx: Ctx, in_: IssuesListIn) -> IssuesListOut:
    issues = await client.list_issues(in_.project, in_.state)
    return IssuesListOut(issues=issues, count=len(issues))

on_issues_list(p, issues_list)
```

`in_.project` 拼错是 IDE 里的红线，不是线上的一次失败调用——代码里没有任何 `dict["key"]`。

## 装

```bash
pip install sokel-plugin-sdk
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest   # 生成器
```

`sokel-gen` 是个单文件二进制（Go 写的），只在**生成时**用到；跑插件时不需要它。

## 四步

```bash
sokel-gen init -lang python ./my-plugin
cd my-plugin
pip install -r requirements.txt
sokel-gen generate .     # sokel.yaml → sokel_gen.py
python main.py
```

1. **声明** —— `sokel.yaml`：操作、事件、凭证、认证方式。格式见 [docs/manifest.md](../docs/manifest.md)。
2. **生成** —— `sokel-gen generate .` 产出 `sokel_gen.py`：每个操作一对 `XxxIn` / `XxxOut` 模型
   和一个 `on_xxx(p, fn)`；每个事件一个 payload 模型和一个 `trigger_xxx(ctx, event_id, payload)`。
3. **实现** —— handler 签名完全具体，可以是 `async def` 也可以是普通函数。
4. **连接** —— `asyncio.run(p.run())`。插件**出站**连平台：无入站端口、无公网 IP、无防火墙洞。

## 能力一览

| 要做的事 | 怎么写 |
|---|---|
| 读凭证 | `credential(ctx)` → 生成的 `Credential` 模型 |
| 取入参文件的字节 | `await ctx.fetch(in_.file)` |
| 产出文件 | `await ctx.upload(name, mime, data)` → 放进出参 |
| 流式产出 | `out.text(...)` 逐帧给人看，`out.vars(Out(...))` 给下游 |
| 推事件 | `await trigger_message(ctx, event_id, MessageEvent(...))` |
| 常驻事件源 | `p.register_source(id, label, fn)`，循环里判 `ctx.stopping.is_set()` |
| 平台代收 webhook | `p.register_webhook(fn)`，返回 `ok()` / `text(401, "...")` |
| 协作式认证 | `p.register_auth(start=…, poll=…, submit=…)` |
| 会话型凭证刷新 | `await ctx.update_credential({"session": "…"})` |
| 自报运行态 | `ctx.report_status("auth_required", "…")` |

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
事件、webhook、协作式认证各一份，Python 与 Node 实现的是同一份声明。

```bash
cd examples/kitchen-sink/python
pip install -r requirements.txt
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx python main.py
```

## 开发本 SDK

```bash
uv venv && uv pip install -e '.[dev]'
python -m pytest -q
```

## License

Apache-2.0.
