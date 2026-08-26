/**
 * NATS 承载：插件**出站**连 broker（无入站端口、无公网 IP、无防火墙洞）。
 *
 * 流程与 Go / Python SDK 逐条对齐（协议 §1-§7）：发现 → 连接 → 注册（上报契约）→
 * 订阅（队列组）→ 心跳续约 → 分发调用 → 优雅下线。事件源插件另有 per-credential supervisor。
 */

import { connect, headers as natsHeaders } from "nats";
import type { Msg, NatsConnection } from "nats";
import { hostname } from "node:os";
import { readFileSync, writeFileSync } from "node:fs";
import { createHash, randomBytes } from "node:crypto";

import { OP_WEBHOOK } from "./contract.js";
import { env } from "./env.js";
import { CredEntry, SourceCtx, SourceSupervisor, desiredSourceCreds } from "./events.js";
import type { Plugin } from "./plugin.js";
import type { FileRuntime, Frame, SokelFile } from "./runtime.js";

/** 同组多副本共享队列组 → 每次调用只投一个副本（真·负载均衡）。 */
export const QUEUE_GROUP = "sokel-workers";
/** 回包/流帧自报副本身份。 */
export const INSTANCE_HEADER = "Sokel-Instance";
/** 1 MiB：字节不走操作 reply（受 max_payload 约束），走专用分块通道。 */
const FILE_CHUNK = 1 << 20;
const HEARTBEAT_MS = 20_000;
const RETRY_MS = 8_000;
const REQUEST_TIMEOUT_MS = 8_000;
const FILE_TIMEOUT_MS = 30_000;

const enc = new TextEncoder();
const dec = new TextDecoder();

/** 经同一条 NATS 连接与平台交换文件字节。不要求插件可达平台 HTTP —— 内网插件同样可用。 */
export class NatsFiles implements FileRuntime {
  constructor(private readonly nc: NatsConnection, private readonly token: string) {}

  async fetch(f: SokelFile): Promise<Uint8Array> {
    const id = f.id || (f.url ? f.url.split("/").pop()! : "");
    if (!id) throw new Error("文件引用缺少 id/url");
    const chunks: Buffer[] = [];
    for (let seq = 0; ; seq++) {
      const resp = await this.nc.request(
        "sokel.file.get",
        enc.encode(JSON.stringify({ token: this.token, id, seq })),
        { timeout: FILE_TIMEOUT_MS },
      );
      const r = JSON.parse(dec.decode(resp.data)) as { error?: string; data?: string; last?: boolean };
      if (r.error) throw new Error(r.error);
      chunks.push(Buffer.from(r.data ?? "", "base64"));
      if (r.last) return Buffer.concat(chunks);
    }
  }

  /** 整块字节。走 storeStream —— 分块协议只该有一份实现。 */
  async store(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    return this.storeStream(name, mime, (async function* () { yield data; })());
  }

  /** **边读边传**，内存占用恒为一个块。平台那侧本来就是逐块写进 blob 的。 */
  async storeStream(name: string, mime: string, src: AsyncIterable<Uint8Array>): Promise<SokelFile> {
    let uploadId = "";
    let seq = 0;
    let pending = Buffer.alloc(0);
    let done = false;
    const it = src[Symbol.asyncIterator]();
    // 攒够一块再发：上游给的分片大小由它自己定（fs 流默认 64KB），
    // 直接照发的话块数会翻十几倍，每块都是一次 request-reply。
    while (!done) {
      while (pending.length < FILE_CHUNK) {
        const r = await it.next();
        if (r.done) { done = true; break; }
        pending = Buffer.concat([pending, Buffer.from(r.value)]);
      }
      const chunk = pending.subarray(0, FILE_CHUNK);
      pending = pending.subarray(chunk.length);
      const last = done && pending.length === 0;
      const f = await this.putChunk(name, mime, uploadId, seq, last, chunk);
      if (f.uploadId) uploadId = f.uploadId;
      if (last) {
        if (!f.file) throw new Error("平台未返回文件引用");
        return f.file;
      }
      seq += 1;
    }
    throw new Error("上传未收到末块应答（不该发生）");
  }

  private async putChunk(
    name: string, mime: string, uploadId: string, seq: number, last: boolean, chunk: Buffer,
  ): Promise<{ uploadId?: string; file?: SokelFile }> {
    {
      {
      const resp = await this.nc.request(
        "sokel.file.put",
        enc.encode(
          JSON.stringify({
            token: this.token,
            upload_id: uploadId,
            name,
            mime,
            seq,
            last,
            data: chunk.toString("base64"),
          }),
        ),
        { timeout: FILE_TIMEOUT_MS },
      );
      const r = JSON.parse(dec.decode(resp.data)) as { error?: string; upload_id?: string; file?: SokelFile };
      if (r.error) throw new Error(r.error);
      return { uploadId: r.upload_id, file: r.file };
      }
    }
  }
}

export class NatsTransport {
  async run(p: Plugin): Promise<void> {
    const target = await discover(p.endpoint, p.token);
    // broker 的传输层鉴权优先用 SOKEL_NATS_TOKEN；缺省回退接入 token（无鉴权 broker 会忽略它）
    const token = env("NATS_TOKEN") || p.token;
    const ca = env("NATS_CA");
    const nc = await connectForever({
      servers: [target],
      name: p.name,
      token: token || undefined,
      maxReconnectAttempts: -1, // 无限重连；订阅在重连后自动恢复
      reconnectTimeWait: 2_000,
      waitOnFirstConnect: true, // broker 未就绪时挂起等它，而不是启动失败退出
      ...(ca ? { tls: { caFile: ca } } : {}),
    });

    const host = hostname();
    const instanceId = stableInstanceId(p.token);
    const startedAt = new Date().toISOString();
    const files = new NatsFiles(nc, p.token);
    let notifySubject = "";

    const register = async (): Promise<{ subject: string; name: string; creds: CredEntry[] }> => {
      const body = p.registerPayload(instanceId, host, startedAt);
      const resp = await nc.request("sokel.register", enc.encode(JSON.stringify(body)), {
        timeout: REQUEST_TIMEOUT_MS,
      });
      const reg = JSON.parse(dec.decode(resp.data)) as {
        ok?: boolean;
        name?: string;
        subject?: string;
        notify_subject?: string;
        error?: string;
        credentials?: Array<{ id?: string; fields?: Record<string, string> }>;
        credential?: Record<string, string>;
        credential_id?: string;
      };
      if (!reg.ok || !reg.subject) throw new Error(`注册被拒：${reg.error ?? "平台未给 subject"}`);
      if (reg.notify_subject) notifySubject = reg.notify_subject;
      let creds = (reg.credentials ?? []).map((c) => new CredEntry(c.id ?? "", c.fields ?? {}));
      // 旧平台只有单数形态 → 折算成单元素集合（行为与旧版一致）
      if (creds.length === 0 && (reg.credential_id || reg.credential)) {
        creds = [new CredEntry(reg.credential_id ?? "", reg.credential ?? {})];
      }
      return { subject: reg.subject, name: reg.name || p.name, creds };
    };

    // 启动注册失败不退出（broker / 平台可能尚未就绪），固定间隔重试直到成功
    let first: Awaited<ReturnType<typeof register>>;
    for (;;) {
      try {
        first = await register();
        break;
      } catch (e) {
        console.warn(`[sokel] 注册失败（${errText(e)}），${RETRY_MS / 1000}s 后重试…`);
        await sleep(RETRY_MS);
      }
    }

    const sub = nc.subscribe(first.subject, { queue: QUEUE_GROUP });
    void (async () => {
      for await (const msg of sub) {
        void this.dispatch(p, nc, msg, files, instanceId);
      }
    })();
    console.log(
      `[sokel] 已接入平台：插件「${first.name}」就绪，副本 ${instanceId} 监听 ${first.subject}`,
    );

    let supervisor: SourceSupervisor | undefined;
    if (p.sources.length > 0) {
      supervisor = makeSupervisor(p, nc, files);
      supervisor.reconcile(desiredSourceCreds(first.creds));
      if (notifySubject) {
        // 凭证变更即时通知：普通订阅（非队列组），组内每个副本都要收——各自的分配集合都可能变
        const debounced = debounce(300, async () => {
          try {
            const { creds } = await register();
            supervisor!.reconcile(desiredSourceCreds(creds));
          } catch (e) {
            console.warn(`[sokel] 凭证变更 re-register 失败: ${errText(e)}`);
          }
        });
        const notifySub = nc.subscribe(notifySubject);
        void (async () => {
          for await (const _ of notifySub) debounced();
        })();
      }
    }

    // 心跳续约保持副本在线；SIGINT/SIGTERM（docker stop / Ctrl-C）→ 优雅下线：
    // 通知平台立即标记 offline（秒级感知），而非等心跳超时清扫（45s+）
    await new Promise<void>((resolve) => {
      const timer = setInterval(async () => {
        try {
          const { creds } = await register();
          // 每拍按最新分配集合 reconcile：分片迁移 / 凭证增删 / 字段刷新 → 起停或重启源实例
          supervisor?.reconcile(desiredSourceCreds(creds));
        } catch (e) {
          console.warn(`[sokel] 心跳续约失败: ${errText(e)}`);
        }
      }, HEARTBEAT_MS);
      const bye = (sig: string) => {
        clearInterval(timer);
        supervisor?.stopAll();
        nc.publish("sokel.unregister", enc.encode(JSON.stringify({ token: p.token, instance_id: instanceId })));
        void nc
          .flush() // 确保下线通知先于断连送达
          .then(() => nc.drain())
          .catch(() => undefined)
          .then(() => {
            console.log(`[sokel] 收到 ${sig}，已通知平台下线，退出`);
            resolve();
          });
      };
      process.once("SIGINT", () => bye("SIGINT"));
      process.once("SIGTERM", () => bye("SIGTERM"));
    });
  }

  private async dispatch(
    p: Plugin,
    nc: NatsConnection,
    msg: Msg,
    files: NatsFiles,
    instanceId: string,
  ): Promise<void> {
    if (!msg.reply) return;
    let call: Record<string, any>;
    try {
      call = JSON.parse(dec.decode(msg.data));
    } catch {
      nc.publish(msg.reply, enc.encode(JSON.stringify({ error: "调用帧解不开" })));
      return;
    }
    const op = (call.operation as string) ?? "";
    const tag = traceTag(call.trace ?? {});

    // 平台代收 webhook 的特殊帧：分发前拦截（老 SDK 没这段会走 unknown operation，
    // 平台把它翻译成「插件未注册 webhook 处理器」）
    if (op === OP_WEBHOOK) {
      console.log(`[sokel] ← webhook 入站${tag}`);
      const sctx = new SourceCtx({
        token: p.token,
        publish: (subject, data) => nc.publish(subject, data),
        validEvents: p.contract.eventIds(),
        credential: call.credential,
        credentialId: call.credential_id ?? "",
        sourceId: "webhook",
        files,
      });
      const out = await p.handleWebhook(sctx, call.input ?? {});
      nc.publish(msg.reply, enc.encode(JSON.stringify(out)));
      return;
    }

    const h = natsHeaders();
    h.set(INSTANCE_HEADER, instanceId);
    const started = Date.now();
    console.log(`[sokel] ← ${op} 开始${tag}`);

    if (p.contract.isStream(op)) {
      // 流式：逐帧发布到回复通道，末尾必发终止帧
      const publishFrame = (f: Frame) => nc.publish(msg.reply!, enc.encode(JSON.stringify(f)), { headers: h });
      try {
        await p.dispatch(call, publishFrame, files);
        console.log(`[sokel] ✓ ${op} 完成(${Date.now() - started}ms)${tag}`);
      } catch (e) {
        console.warn(`[sokel] ✗ ${op} 失败(${Date.now() - started}ms)${tag}: ${errText(e)}`);
        publishFrame({ kind: "error", text: errText(e) });
      }
      publishFrame({ kind: "end" });
      return;
    }

    try {
      const vars = await p.dispatchBuffered(call, files);
      console.log(`[sokel] ✓ ${op} 完成(${Date.now() - started}ms)${tag}`);
      nc.publish(msg.reply, enc.encode(JSON.stringify(vars)), { headers: h });
    } catch (e) {
      console.warn(`[sokel] ✗ ${op} 失败(${Date.now() - started}ms)${tag}: ${errText(e)}`);
      nc.publish(msg.reply, enc.encode(JSON.stringify({ error: errText(e) })), { headers: h });
    }
  }
}

/** 每个凭证一套源实例：ctx 绑定该凭证，trigger 自动回带其 credential_id。 */
function makeSupervisor(p: Plugin, nc: NatsConnection, files: NatsFiles): SourceSupervisor {
  return new SourceSupervisor((cred) => {
    const ctxs: SourceCtx[] = [];
    for (const src of p.sources) {
      const sctx = new SourceCtx({
        token: p.token,
        publish: (subject, data) => nc.publish(subject, data),
        validEvents: p.contract.eventIds(),
        credential: cred.fields,
        credentialId: cred.id,
        sourceId: src.id,
        board: p.board,
        files,
      });
      ctxs.push(sctx);
      console.log(`[sokel] 事件源「${src.id}」启动（credential=${cred.id || "(无凭证)"}）`);
      p.board.set(src.id, cred.id, "running");
      void src
        .fn(sctx)
        .then(() => {
          if (!sctx.stopped) p.board.setIfRunning(src.id, cred.id, "exited");
        })
        .catch((e) => {
          if (sctx.stopped) return; // reconcile 停止：状态已由 stop() 整体移除
          console.warn(`[sokel] 事件源「${src.id}」退出（credential=${cred.id || "(无凭证)"}）: ${errText(e)}`);
          p.board.set(src.id, cred.id, "error", errText(e));
        });
    }
    return () => {
      // JS 没有任务取消：约定由源循环自己看 ctx.stopped 退出（长轮询在下一拍即感知）
      for (const c of ctxs) c.stopped = true;
      p.board.removeCred(cred.id);
    };
  });
}

/**
 * 统一 https 端点 → 经平台 /connect-info 发现真实承载地址。
 * 直填 nats:// / tls:// 时跳过发现（本地开发 / 离线场景）。
 */
export async function discover(endpoint: string, token: string): Promise<string> {
  const ep = (endpoint ?? "").trim();
  if (ep.startsWith("nats://") || ep.startsWith("tls://")) return ep;
  if (!ep.startsWith("http://") && !ep.startsWith("https://")) {
    throw new Error(`端点 "${endpoint}" 不合法：应为平台地址（https://…）或 nats://`);
  }
  const url = ep.replace(/\/+$/, "") + "/api/v1/connect-info";
  const resp = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
  if (!resp.ok) throw new Error(`平台发现失败 ${url}: HTTP ${resp.status}`);
  const info = (await resp.json()) as { transports?: Record<string, string> };
  const target = info.transports?.nats;
  if (!target) throw new Error("平台未提供可用承载（connect-info.transports 为空）");
  return target;
}

/**
 * 副本的稳定身份：重启复用，否则平台侧每次重启都多出一行永远 offline 的幽灵实例。
 * 1) SOKEL_INSTANCE_ID；2) 工作目录里按 token 指纹命名的落盘文件；3) 落盘失败 → host-pid。
 */
export function stableInstanceId(token: string): string {
  const explicit = env("INSTANCE_ID");
  if (explicit) return explicit;
  let file = ".sokel-instance-id";
  if (token) file += "." + createHash("sha256").update(token).digest("hex").slice(0, 8);
  try {
    const existing = readFileSync(file, "utf8").trim();
    if (existing) return existing;
  } catch {
    /* 首次运行：往下走，生成并落盘 */
  }
  const id = `${hostname()}-${randomBytes(4).toString("hex")}`;
  try {
    writeFileSync(file, id + "\n");
  } catch {
    return `${hostname()}-${process.pid}`;
  }
  return id;
}

async function connectForever(opts: Parameters<typeof connect>[0]): Promise<NatsConnection> {
  for (;;) {
    try {
      return await connect(opts);
    } catch (e) {
      console.warn(`[sokel] 连接平台失败（${errText(e)}），${RETRY_MS / 1000}s 后重试…`);
      await sleep(RETRY_MS);
    }
  }
}

function debounce(ms: number, fn: () => void): () => void {
  let timer: NodeJS.Timeout | undefined;
  return () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(fn, ms);
  };
}

function traceTag(trace: Record<string, string>): string {
  const parts = ([["run_id", "run"], ["workflow_id", "wf"], ["node_id", "node"]] as const)
    .filter(([key]) => trace[key])
    .map(([key, label]) => `${label}=${trace[key]}`);
  return parts.length > 0 ? ` [${parts.join(" ")}]` : "";
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
