// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Plugin: the registry plus dispatch. Transport-agnostic — the NATS layer only moves bytes.
 *
 * Dispatch is the part **most worth unit-testing** (unknown operations, the reply shape for
 * streaming versus non-streaming, intercepting the webhook frame), so it touches no NATS at all: a
 * fake sink is enough to exercise the whole path.
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

/** One call delivered by the platform (protocol §4). */
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
    // Version, in order of precedence: explicit argument > the contract's plugin.version >
    // environment variable > fallback
    this.version = cfg.version || cfg.contract.version || env("VERSION") || "sdk-node";
  }

  /** Low-level registration: fn(ctx, input, emitter). The generated onXxx is its typed shell. */
  register(opId: string, fn: Invoke): void {
    if (!opId.includes(".") && !this.contract.operation(opId)) {
      throw new Error(`operation "${opId}" is not in the contract — declare it under operations in manifest.yml and regenerate`);
    }
    if (this.ops.has(opId)) throw new Error(`operation "${opId}" registered twice`);
    this.ops.set(opId, fn);
  }

  /** Register a long-running event source; run() starts one task per source × credential. */
  registerSource(id: string, label: string, fn: (ctx: SourceCtx) => Promise<void>): void {
    this.sources.push({ id, label, fn });
  }

  /** Register the webhook handler (one per plugin: route upstream event types yourself by header
   * or path). */
  registerWebhook(fn: WebhookHandler): void {
    this.webhookFn = fn;
    // The capability follows the fact, not a declaration: registering a handler *is* support.
    // Forgetting to declare it should never make the entry-point button disappear.
    this.contract.data.capabilities = { ...(this.contract.data.capabilities ?? {}), [CAP_WEBHOOK]: true };
  }

  get hasWebhook(): boolean {
    return this.webhookFn !== undefined;
  }

  /**
   * Attach the auth flow's implementation. The shape (qr / input / oauth) is declared in manifest.yml;
   * only the implementation goes here.
   *
   * For kind=oauth the platform answers start/poll itself — the client secret lives there and a
   * plugin cannot build the consent URL — so such a plugin writes no handler at all.
   */
  registerAuth(h: AuthHandlers): void {
    const declared = this.contract.data.auth_flow;
    const steps = declared?.steps ?? [];
    const requireStep = (step: string) => {
      if (!steps.includes(step)) {
        throw new Error(
          `the contract's auth flow has no "${step}" step (it has ${steps.join("/") || "none"}) — ` +
            "the steps follow from credential.auth.kind, and implementing more than was declared " +
            "means writing code that will never be called",
        );
      }
    };
    if (h.start) {
      requireStep("start");
      this.ops.set(OP_AUTH_START, async (ctx, _in, out) => {
        const ch = await h.start!(ctx);
        if (!ch) throw new Error("the auth flow's start returned no challenge");
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
        // Only carry the session once confirmed: handing back a null while pending makes the
        // platform rewrite the credential row over and over.
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

  /** Declare which **optional** capabilities this plugin has — how far a given operation goes. */
  setCapabilities(caps: Record<string, boolean>): void {
    this.contract.data.capabilities = { ...(this.contract.data.capabilities ?? {}), ...caps };
  }

  /**
   * The registration / heartbeat payload (protocol §3).
   *
   * It is a separate method so it can be tested: **declared but never reported** is the classic
   * silent failure of a self-reporting mechanism. Everything looks fine on the plugin side, nothing
   * happens on the platform side, and the author is left staring at an inert UI.
   */
  registerPayload(instanceId: string, host: string, startedAt: string): Record<string, unknown> {
    const body: Record<string, unknown> = {
      token: this.token,
      instance_id: instanceId,
      host,
      // Process start time: registration and every heartbeat resend the same value, which is how
      // the platform tells "a new replica came up" from "the old one is still alive".
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
    // Single-operation plugin: when `operation` is missing (or unknown), fall back to the only one
    // there is — the same fallback the Go SDK has.
    const business = [...this.ops.keys()].filter((k) => !k.includes("."));
    return business.length === 1 ? this.ops.get(business[0]) : undefined;
  }

  /** Run one call, handing frames to the sink. Exceptions propagate; the transport turns them into
   * an error frame or an error reply. */
  async dispatch(call: Call, sink: Sink, files?: FileRuntime): Promise<void> {
    const opId = call.operation ?? "";
    const fn = this.find(opId);
    if (!fn) throw new Error(`unknown operation "${opId}"`);
    const ctx = new Ctx({ credential: call.credential, trace: call.trace, files });
    await fn(ctx, call.input ?? {}, new Emitter<unknown>(sink));
  }

  /** Non-streaming: buffer the frames and merge the variables into a single reply. */
  async dispatchBuffered(call: Call, files?: FileRuntime): Promise<Record<string, unknown>> {
    const buf = new BufferSink();
    await this.dispatch(call, buf.sink, files);
    return buf.vars;
  }

  /**
   * Handle one __webhook__ frame. The reply carries an events count: that is how the platform's
   * webhook panel answers "the request arrived, so why did no workflow start?".
   */
  async handleWebhook(sctx: SourceCtx, frame: WebhookFrame): Promise<Record<string, unknown>> {
    if (!this.webhookFn) return { status: 0, error: "the plugin registered no webhook handler" };
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

  /** Connect, register, heartbeat and dispatch calls. The promise settles on SIGINT/SIGTERM. */
  async run(): Promise<void> {
    const { NatsTransport } = await import("./nats.js");
    await new NatsTransport().run(this);
  }
}
