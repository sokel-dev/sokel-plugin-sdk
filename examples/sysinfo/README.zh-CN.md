# sysinfo 插件

返回「插件运行系统」的基础信息 JSON（hostname / os / arch / cpu / go 版本 / pid / 运行时长 / 内存 / 时间…）。

一份数据，两处出口：
- **HTTP**：`GET/POST` 任意路径 → JSON。用于在 sokel 画布「HTTP 节点」直接调用验证。
- **NATS 出站（可选）**：连入 NATS、订阅 subject，`request-reply` 回同一份 JSON（契合平台「远程出站」模型）。

## 运行

```bash
cd plugins/sysinfo
go run .                                    # 仅 HTTP，监听 :8710
# 或带 NATS：
NATS_URL=nats://127.0.0.1:4222 NATS_TOKEN=<接入token> go run .
```

环境变量：`HTTP_ADDR`(默认 `:8710`) · `PLUGIN_NAME` · `NATS_URL` · `NATS_TOKEN` · `NATS_SUBJECT`(默认 `sokel.plugin.sysinfo`)。

## 在画布中调用验证（HTTP 节点）

> 说明：平台当前的**真实出站通道是 HTTP**；NATS 骨干尚未落地（见 docs/architecture.md 路线），故画布验证走 HTTP 节点。

1. 启动插件：`go run .`（HTTP `:8710`）。
2. 画布拖入 **HTTP 节点**，配置：
   - URL：`http://localhost:8710/sysinfo`
   - 方法：`GET`
3. 单独运行该节点（或整流运行）→ 在「最近运行」看输出：
   - `body` = 系统基础信息对象（`os`/`arch`/`num_cpu`/`memory`…）
   - `status` = 200
4. 下游节点可引用 `HTTP节点.body.os`、`.num_cpu` 等字段。

## NATS 模式（平台 NATS 就绪后）

插件已实现 NATS `request-reply`：平台把请求发到 `NATS_SUBJECT`，插件回 JSON 到 `msg.Reply`。
平台侧需要 NATS 骨干 + 「插件调用」执行器才能从画布经 subject 调用（当前未实现）。
