/**
 * 事件源：插件主动把外部事件推给平台起 workflow（协议 §7）。
 *
 * 与操作的区别：操作是 request/reply（平台调插件），事件是 fire-and-forget（插件推平台）。
 *
 * 多 bot 单实例（协议 v1.3）：平台每次注册/心跳下发「分配给本副本的凭证子集」，
 * supervisor 按它 reconcile —— 每个凭证一套源实例，凭证被移除就取消，字段变了就重启。
 */

import type { FileRuntime, SokelFile } from "./runtime.js";
import { toVars } from "./runtime.js";

export const TRIGGER_SUBJECT = "sokel.trigger";
export const CREDENTIAL_UPDATE_SUBJECT = "sokel.credential.update";

/** 注册回包 credentials 列表项 —— 分配给本副本的一个 bot 身份。 */
export class CredEntry {
  constructor(readonly id: string = "", readonly fields: Record<string, string> = {}) {}

  /** 字段的稳定签名：reconcile 据此判定「字段变更 → 重启该源实例」。 */
  sig(): string {
    return Object.keys(this.fields)
      .sort()
      .map((k) => `${k}=${this.fields[k]}`)
      .join("\n");
  }
}

export interface SourceState {
  source_id: string;
  credential_id?: string;
  status: string;
  error?: string;
  since: string;
}

/** 源实例运行态（源 × 凭证）。随注册/心跳上报，面板据此展示每个 bot。 */
export class StateBoard {
  private readonly m = new Map<string, SourceState>();

  constructor(private readonly now: () => string = () => new Date().toISOString()) {}

  set(sourceId: string, credId: string, status: string, error = ""): void {
    const st: SourceState = { source_id: sourceId, status, since: this.now() };
    if (credId) st.credential_id = credId;
    if (error) st.error = error;
    this.m.set(`${sourceId}|${credId}`, st);
  }

  /**
   * 只在该实例仍是 running 时改写。
   *
   * 源自报过状态（如 auth_required）之后正常返回，收尾时不该把那句话盖掉——
   * 盖掉之后面板上只剩一个「已退出」，而「为什么退出」正是要看的那一半。
   */
  setIfRunning(sourceId: string, credId: string, status: string, error = ""): void {
    const cur = this.m.get(`${sourceId}|${credId}`);
    if (!cur || cur.status === "running") this.set(sourceId, credId, status, error);
  }

  removeCred(credId: string): void {
    for (const [k, v] of this.m) {
      if ((v.credential_id ?? "") === credId) this.m.delete(k);
    }
  }

  snapshot(): SourceState[] {
    return [...this.m.values()].sort((a, b) =>
      a.source_id === b.source_id
        ? (a.credential_id ?? "").localeCompare(b.credential_id ?? "")
        : a.source_id.localeCompare(b.source_id),
    );
  }
}

export type Publish = (subject: string, data: Uint8Array) => void | Promise<void>;

/** 常驻事件源 / webhook 的上下文：推事件、读凭证、回写凭证、上传附件、自报状态。 */
export class SourceCtx {
  readonly credential: Record<string, string>;
  readonly credentialId: string;
  readonly sourceId: string;
  /** stopped：该源实例被 reconcile 停止时置真。长轮询循环 while (!ctx.stopped)。 */
  stopped = false;

  private readonly token: string;
  private readonly publish: Publish;
  private readonly validEvents: Set<string>;
  private readonly board?: StateBoard;
  private readonly files?: FileRuntime;

  constructor(opts: {
    token: string;
    publish: Publish;
    validEvents?: string[];
    credential?: Record<string, string>;
    credentialId?: string;
    sourceId?: string;
    board?: StateBoard;
    files?: FileRuntime;
  }) {
    this.token = opts.token;
    this.publish = opts.publish;
    this.validEvents = new Set(opts.validEvents ?? []);
    this.credential = opts.credential ?? {};
    this.credentialId = opts.credentialId ?? "";
    this.sourceId = opts.sourceId ?? "";
    this.board = opts.board;
    this.files = opts.files;
  }

  /** 凭证按类型化形状读出（与操作侧 Ctx.credentialAs 同义）。 */
  credentialAs<T extends object>(): Partial<T> {
    return this.credential as Partial<T>;
  }

  /**
   * 推一条事件（fire-and-forget）。
   *
   * event 必须是已声明的事件 id —— 拼错在这里当场报错，而不是变成一条平台侧无人认领的
   * 消息（那种失败没有任何症状：插件日志正常，工作流就是不起）。
   * eventId 是幂等键，平台按 (plugin, event, eventId) 去重。
   */
  async trigger(event: string, eventId: string, payload: unknown): Promise<void> {
    if (this.validEvents.size > 0 && !this.validEvents.has(event)) {
      throw new Error(`未声明的事件 "${event}"（先在 sokel.yaml 的 events 里声明）`);
    }
    const msg: Record<string, unknown> = {
      token: this.token,
      event,
      payload: toVars(payload),
    };
    if (eventId) msg.event_id = eventId;
    if (this.credentialId) msg.credential_id = this.credentialId;
    await this.publish(TRIGGER_SUBJECT, encode(msg));
  }

  /**
   * 把 patch 回写到本实例绑定的平台凭证（会话型凭证运行中刷新用）。
   * 平台是唯一凭证存储方，插件本地从不落地凭证。
   */
  async updateCredential(patch: Record<string, string>): Promise<void> {
    if (!this.credentialId) throw new Error("本源实例未绑定凭证，无可回写目标");
    if (Object.keys(patch).length === 0) return;
    Object.assign(this.credential, patch);
    await this.publish(
      CREDENTIAL_UPDATE_SUBJECT,
      encode({ token: this.token, credential_id: this.credentialId, patch }),
    );
  }

  /** 自报运行态（如 session 失效 → auth_required），随心跳上报，面板亮「待登录」。 */
  reportStatus(status: string, msg = ""): void {
    this.board?.set(this.sourceId, this.credentialId, status, msg);
  }

  async upload(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    if (!this.files) return { name, mime, size: data.length, data };
    return this.files.store(name, mime, data);
  }

  async fetch(f: SokelFile): Promise<Uint8Array> {
    if (f.data) return f.data;
    if (!this.files) throw new Error("文件运行时未就绪");
    return this.files.fetch(f);
  }
}

function encode(v: unknown): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(v));
}

/** 一个常驻事件源。fn 在 SDK 起的任务里跑，内部用 ctx.trigger 推事件。 */
export interface Source {
  id: string;
  label: string;
  fn: (ctx: SourceCtx) => Promise<void>;
}

/** per-credential 源实例监督器：按平台下发的凭证集合起停/重启。 */
export class SourceSupervisor {
  private readonly running = new Map<string, { stop: () => void; sig: string }>();

  constructor(private readonly start: (c: CredEntry) => () => void) {}

  reconcile(desired: CredEntry[]): void {
    const want = new Map(desired.map((c) => [c.id, c]));
    // 停：不在期望集合，或字段变更（先停后起 = 重启）
    for (const [id, r] of [...this.running]) {
      const c = want.get(id);
      if (c && c.sig() === r.sig) continue;
      r.stop();
      this.running.delete(id);
    }
    // 起：期望但未运行
    for (const [id, c] of want) {
      if (this.running.has(id)) continue;
      this.running.set(id, { stop: this.start(c), sig: c.sig() });
    }
  }

  stopAll(): void {
    for (const r of this.running.values()) r.stop();
    this.running.clear();
  }
}

/** 空（无凭证插件）→ 一个空凭证裸实例，与有凭证时同一条代码路径。 */
export function desiredSourceCreds(creds: CredEntry[]): CredEntry[] {
  return creds.length > 0 ? creds : [new CredEntry()];
}
