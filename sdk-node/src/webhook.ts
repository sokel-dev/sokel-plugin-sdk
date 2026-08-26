/**
 * 平台代插件收 webhook：外部系统 → 平台 /hooks/{token} → __webhook__ 帧到这里（协议 §7b）。
 *
 * handler 的职责：用凭证里的 secret 验上游签名（各家算法不同，平台不懂上游、插件懂）→
 * 解析 body → ctx.trigger 推 typed 事件（走既有声明校验与平台去重）→ 返回响应
 * （GitLab 要 2xx、飞书 URL 校验要回 challenge，由 handler 决定）。
 */

export interface WebhookFrame {
  method?: string;
  path?: string;
  query?: string;
  headers?: Record<string, string>;
  body_b64?: string;
}

/** 一次入站 webhook（平台已剥掉 Cookie 等平台侧头）。 */
export class WebhookRequest {
  readonly method: string;
  readonly path: string;
  readonly query: string;
  readonly headers: Record<string, string>;
  /** body 走 base64 保原始字节：HMAC 类验签必须逐字节一致，JSON 重编码会破坏签名。 */
  readonly body: Buffer;

  constructor(frame: WebhookFrame) {
    this.method = frame.method ?? "POST";
    this.path = frame.path ?? "";
    this.query = frame.query ?? "";
    this.headers = frame.headers ?? {};
    this.body = Buffer.from(frame.body_b64 ?? "", "base64");
  }

  /** 大小写不敏感取头（HTTP 语义；X-Gitlab-Event 与 x-gitlab-event 都认）。 */
  header(name: string): string {
    const lowered = name.toLowerCase();
    for (const [k, v] of Object.entries(this.headers)) {
      if (k.toLowerCase() === lowered) return v;
    }
    return "";
  }

  /** body 按 JSON 解。解不开时抛错——上游发了坏 JSON 该是一次可见的失败。 */
  json<T = unknown>(): T {
    return JSON.parse(this.body.toString("utf8")) as T;
  }
}

/** 回给上游的应答。status=0 + error 表示处理失败（平台翻译成 5xx）。 */
export interface WebhookResponse {
  status: number;
  headers?: Record<string, string>;
  body?: Buffer | string;
}

export function ok(): WebhookResponse {
  return { status: 200 };
}

/** 要回 body 的场景（飞书 URL 校验的 challenge 这类）。 */
export function text(status: number, body: string): WebhookResponse {
  return { status, body };
}

export function responseFrame(resp: WebhookResponse, events: number): Record<string, unknown> {
  const body = resp.body === undefined ? Buffer.alloc(0) : Buffer.from(resp.body as never);
  return {
    status: resp.status || 200,
    headers: resp.headers ?? null,
    body_b64: body.toString("base64"),
    events,
  };
}
