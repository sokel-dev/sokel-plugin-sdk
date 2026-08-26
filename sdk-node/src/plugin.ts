/**
 * Plugin：注册表 + 分发。传输无关——NATS 那一层只负责收发字节。
 *
 * 分发这段是**最值得单测的一段**（未知操作、流式与非流式的回复形态、webhook 帧的拦截），
 * 所以它一行都不碰 NATS：测试塞一个假 sink 就能跑完整条路径。
 */

import { CAP_WEBHOOK, Contract, OP_AUTH_POLL, OP_AUTH_START, OP_AUTH_SUBMIT } from "./contract.js";
import type { ContractData } from "./contract.js";
import type { AuthHandlers } from "./auth.js";
import { CONFIRMED, PENDING } from "./auth.js";
import { SourceCtx, StateBoard } from "./events.js";
import type { Source } from "./events.js";
import { BufferSink, Ctx, Emitter } from "./runtime.js";
import type { FileRuntime, Sink } from "./runtime.js";
import { env, envOr } from "./env.js";
import { WebhookRequest, responseFrame } from "./webhook.js";
import type { WebhookFrame, WebhookResponse } from "./webhook.js";

export interface Config {
  contract: ContractData;
  name?: string;
  endpoint?: string;
  token?: string;
  version?: string;
}

/** 平台下发的一次调用（协议 §4）。 */
export interface Call {
  operation?: string;
  input?: Record<string, unknown>;
  credential?: Record<string, string>;
  credential_id?: string;
  trace?: Record<string, string>;
}

export type Invoke = (ctx: Ctx, input: Record<string, unknown>, out: Emitter<unknown>) => Promise<void>;

export type WebhookHandler = (
  ctx: SourceCtx,
  req: WebhookRequest,
) => Promise<WebhookResponse | void> | WebhookResponse | void;

export class Plugin {
  readonly contract: Contract;
  readonly name: string;
  readonly endpoint: string;
  readonly token: string;
  readonly version: string;
  readonly board = new StateBoard();
  readonly sources: Source[] = [];

  private readonly ops = new Map<string, Invoke>();
  private webhookFn?: WebhookHandler;
  managed = false;

  constructor(cfg: Config) {
    this.contract = new Contract(cfg.contract);
    this.name = cfg.name || cfg.contract.name || "sokel-plugin";
    this.endpoint = cfg.endpoint || envOr("ENDPOINT", "http://localhost:8088");
    this.token = cfg.token || env("TOKEN");
    // 版本三档：显式参数 > 契约里声明的（sokel.yaml 的 plugin.version）> 环境变量 > 兜底
    this.version = cfg.version || cfg.contract.version || env("VERSION") || "sdk-node";
  }

  /** 低阶注册：fn(ctx, input, emitter)。生成的 onXxx 就是它的类型化外壳。 */
  register(opId: string, fn: Invoke): void {
    if (!opId.includes(".") && !this.contract.operation(opId)) {
      throw new Error(`操作 "${opId}" 不在契约里 —— 先在 sokel.yaml 的 operations 声明它，再重新生成`);
    }
    if (this.ops.has(opId)) throw new Error(`操作 "${opId}" 重复注册`);
    this.ops.set(opId, fn);
  }

  /** 注册常驻事件源；run() 时每个「源 × 凭证」起一个任务。 */
  registerSource(id: string, label: string, fn: (ctx: SourceCtx) => Promise<void>): void {
    this.sources.push({ id, label, fn });
  }

  /** 注册 webhook 处理器（一个插件一个：按 header / path 自行分流上游事件类型）。 */
  registerWebhook(fn: WebhookHandler): void {
    this.webhookFn = fn;
    // 能力位不靠自报靠事实：注册了处理器就是支持，作者忘声明不该让入口按钮消失
    this.contract.data.capabilities = { ...(this.contract.data.capabilities ?? {}), [CAP_WEBHOOK]: true };
  }

  get hasWebhook(): boolean {
    return this.webhookFn !== undefined;
  }

  /**
   * 挂上认证流的实现。形态（qr / input / oauth）在 sokel.yaml 里声明，这里只给实现。
   *
   * kind=oauth 的 start/poll 由**平台代答**（client_secret 在平台手里，插件构造不出
   * 同意页地址），所以那种插件一个 handler 都不用写。
   */
  registerAuth(h: AuthHandlers): void {
    const declared = this.contract.data.auth_flow;
    const steps = declared?.steps ?? [];
    const requireStep = (step: string) => {
      if (!steps.includes(step)) {
        throw new Error(
          `契约里的认证流没有 "${step}" 这一步（当前 ${steps.join("/") || "未声明"}）——` +
            "步骤由 credential.auth.kind 定死，实现多于声明就是一份永远不会被调用的代码",
        );
      }
    };
    if (h.start) {
      requireStep("start");
      this.ops.set(OP_AUTH_START, async (ctx, _in, out) => {
        const ch = await h.start!(ctx);
        if (!ch) throw new Error("认证流 start 未返回挑战");
        out.vars({
          auth_id: ch.authId || `auth_${Date.now()}`,
          challenge: { kind: ch.kind ?? declared?.kind ?? "", qr_image: ch.qrImage ?? "", prompt: ch.prompt ?? "" },
          expires_in: ch.expiresIn ?? 0,
        });
      });
    }
    if (h.poll) {
      requireStep("poll");
      this.ops.set(OP_AUTH_POLL, async (ctx, input, out) => {
        const st = (await h.poll!(ctx, String(input.auth_id ?? ""))) ?? { status: PENDING };
        const vars: Record<string, unknown> = { status: st.status };
        // session 只在 confirmed 时带出去；pending 时带一个 null 会让平台反复覆写凭证行
        if (st.status === CONFIRMED && st.session) vars.session = st.session;
        out.vars(vars);
      });
    }
    if (h.submit) {
      requireStep("submit");
      this.ops.set(OP_AUTH_SUBMIT, async (ctx, input, out) => {
        await h.submit!(ctx, String(input.auth_id ?? ""), String(input.input ?? ""));
        out.vars({ ok: true });
      });
    }
  }

  /** 声明本插件支持/不支持哪些**可选**能力（同一个操作做到什么程度）。 */
  setCapabilities(caps: Record<string, boolean>): void {
    this.contract.data.capabilities = { ...(this.contract.data.capabilities ?? {}), ...caps };
  }

  /**
   * 注册 / 心跳的载荷（协议 §3）。
   *
   * 单独一个方法是为了能测：**声明了却没上报**是这套自报机制最典型的静默失效——
   * 插件侧一切正常、平台侧什么也没发生，作者只能对着一个没反应的界面排查。
   */
  registerPayload(instanceId: string, host: string, startedAt: string): Record<string, unknown> {
    const body: Record<string, unknown> = {
      token: this.token,
      instance_id: instanceId,
      host,
      // 进程启动时刻：注册与每拍心跳重发同一值，平台据此区分「重新上线的新副本」与「一直活着的老副本」
      started_at: startedAt,
      version: this.version,
      transport: "nats",
      managed: this.managed,
      ...this.contract.payload(),
    };
    const region = env("REGION");
    if (region) body.region = region;
    if (this.sources.length > 0) body.source_states = this.board.snapshot();
    return body;
  }

  find(opId: string): Invoke | undefined {
    const fn = this.ops.get(opId);
    if (fn) return fn;
    // 单操作插件：operation 省略（或对不上）时默认唯一操作，与 Go SDK 同一条兜底
    const business = [...this.ops.keys()].filter((k) => !k.includes("."));
    return business.length === 1 ? this.ops.get(business[0]) : undefined;
  }

  /** 跑一次调用，把各帧交给 sink。异常原样抛出，由传输层翻成 error 帧 / error 回复。 */
  async dispatch(call: Call, sink: Sink, files?: FileRuntime): Promise<void> {
    const opId = call.operation ?? "";
    const fn = this.find(opId);
    if (!fn) throw new Error(`unknown operation "${opId}"`);
    const ctx = new Ctx({ credential: call.credential, trace: call.trace, files });
    await fn(ctx, call.input ?? {}, new Emitter<unknown>(sink));
  }

  /** 非流式：缓冲各帧，合并 variables 作为单次回复。 */
  async dispatchBuffered(call: Call, files?: FileRuntime): Promise<Record<string, unknown>> {
    const buf = new BufferSink();
    await this.dispatch(call, buf.sink, files);
    return buf.vars;
  }

  /**
   * 处理一帧 __webhook__。应答带 events 计数——平台的 webhook 面板靠它回答
   * 「请求到了但为什么没起工作流」这一问。
   */
  async handleWebhook(sctx: SourceCtx, frame: WebhookFrame): Promise<Record<string, unknown>> {
    if (!this.webhookFn) return { status: 0, error: "插件未注册 webhook 处理器" };
    const req = new WebhookRequest(frame ?? {});
    let events = 0;
    const counted = new Proxy(sctx, {
      get(target, prop, recv) {
        if (prop === "trigger") {
          return async (event: string, eventId: string, payload: unknown) => {
            await target.trigger(event, eventId, payload);
            events += 1;
          };
        }
        const v = Reflect.get(target, prop, recv);
        return typeof v === "function" ? v.bind(target) : v;
      },
    });
    try {
      const resp = (await this.webhookFn(counted, req)) ?? { status: 200 };
      return responseFrame(resp, events);
    } catch (e) {
      return { status: 0, error: `${e instanceof Error ? e.message : e}` };
    }
  }

  /** 连接平台、注册、心跳、分发调用。返回的 Promise 在收到 SIGINT/SIGTERM 后完成。 */
  async run(): Promise<void> {
    const { NatsTransport } = await import("./nats.js");
    await new NatsTransport().run(this);
  }
}
