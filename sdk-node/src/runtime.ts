/**
 * 运行时形状：文件引用、产出帧、调用上下文。
 *
 * 与传输无关——NATS 只提供「取字节 / 存字节 / 发帧」三个回调，剩下的语义都在这里。
 * 测试因此不需要 broker：塞一个假的文件运行时与假的 sink 就能跑完整条分发路径。
 */

/** 文件引用（画布里只流转引用，不内联字节）。 */
export interface SokelFile {
  id?: string;
  url?: string;
  name?: string;
  mime?: string;
  size?: number;
  /** data：无平台文件层时（单元测试）直接携带的字节，不参与序列化。 */
  data?: Uint8Array;
}

/** 文件字节的取/存后端，由传输层注入。 */
export interface FileRuntime {
  fetch(f: SokelFile): Promise<Uint8Array>;
  store(name: string, mime: string, data: Uint8Array): Promise<SokelFile>;
}

export const FRAME_TEXT = "text";
export const FRAME_JSON = "json";
export const FRAME_VARS = "variables";

export interface Frame {
  kind: string;
  text?: string;
  json?: unknown;
  vars?: Record<string, unknown>;
}

export type Sink = (f: Frame) => void;

/** 类型化产出器。多次调用 = 多帧（流式）；非流式由 SDK 缓冲合并成一次回复。 */
export class Emitter<Out> {
  constructor(private readonly sink: Sink) {}

  /** 人类可读文本（展示 / tracing）。 */
  text(s: string): void {
    this.sink({ kind: FRAME_TEXT, text: s });
  }

  /** 结构化 JSON（展示 / tracing）。 */
  json(v: unknown): void {
    this.sink({ kind: FRAME_JSON, json: v });
  }

  /** 类型化输出变量（进下游节点）。可多次调用，后帧覆盖同名字段。 */
  vars(out: Out): void {
    const m = toVars(out);
    if (Object.keys(m).length > 0) this.sink({ kind: FRAME_VARS, vars: m });
  }
}

export function toVars(value: unknown): Record<string, unknown> {
  if (value === null || value === undefined) return {};
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`输出必须是对象，收到 ${typeof value}`);
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    if (v !== undefined) out[k] = v;
  }
  return out;
}

/** 非流式汇聚：只收 variables 帧，合并成一个输出对象（text/json 仅流式展示用）。 */
export class BufferSink {
  readonly vars: Record<string, unknown> = {};

  readonly sink: Sink = (f: Frame) => {
    if (f.kind !== FRAME_VARS) return;
    Object.assign(this.vars, f.vars ?? {});
  };
}

/** 操作 handler 的上下文：凭证、追踪、文件取存。 */
export class Ctx {
  readonly credential: Record<string, string>;
  private readonly traceMap: Record<string, string>;
  private readonly files?: FileRuntime;

  constructor(opts: {
    credential?: Record<string, string>;
    trace?: Record<string, string>;
    files?: FileRuntime;
  } = {}) {
    this.credential = opts.credential ?? {};
    this.traceMap = opts.trace ?? {};
    this.files = opts.files;
  }

  /**
   * 平台下发的追踪上下文（run_id / workflow_id / node_id）。
   *
   * 非工作流调用（试调用、健康检查）没有这些值，返回空串——**调用方要把空串当
   * 「没有重试语义」处理**，而不是当成一个恒定的键（那会把两次独立调用错误地去重）。
   */
  trace(key: string): string {
    return this.traceMap[key] ?? "";
  }

  /**
   * 凭证按类型化形状读出。返回 Partial<T> 而不是 T —— 平台下发的是「这条凭证有哪些字段」，
   * 生成的 Credential 里标必填的字段在运行时仍可能缺（凭证刚建、还没登录）。
   * 假装它一定在，只会把一次 undefined 推迟到更远的地方炸。
   */
  credentialAs<T extends object>(): Partial<T> {
    return this.credential as Partial<T>;
  }

  async fetch(f: SokelFile): Promise<Uint8Array> {
    if (f.data) return f.data;
    if (!this.files) throw new Error("文件运行时未就绪");
    return this.files.fetch(f);
  }

  /** 产出一个文件：字节交回平台登记，返回可放进出参的引用。 */
  async upload(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    if (!this.files) return { name, mime, size: data.length, data };
    return this.files.store(name, mime, data);
  }
}
