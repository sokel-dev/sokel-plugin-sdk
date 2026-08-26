// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Webhooks relayed by the platform: upstream system -> platform /hooks/{token} -> a __webhook__
 * frame lands here (protocol §7b).
 *
 * What the handler is responsible for: verifying the upstream signature using the secret in the
 * credential (every vendor signs differently — the platform does not know the upstream, the plugin
 * does), parsing the body, pushing typed events with ctx.trigger (which reuses the declared-event
 * check and the platform's deduplication), and deciding the response (GitLab wants a 2xx, Feishu's
 * URL verification wants the challenge echoed back).
 */

export interface WebhookFrame {
  method?: string;
  path?: string;
  query?: string;
  headers?: Record<string, string>;
  body_b64?: string;
}

/** One inbound webhook (the platform has already stripped Cookie and other platform-side headers). */
export class WebhookRequest {
  readonly method: string;
  readonly path: string;
  readonly query: string;
  readonly headers: Record<string, string>;
  /** The body travels as base64 to keep the exact bytes: HMAC-style verification has to see them
   * byte for byte, and re-encoding the JSON would break the signature. */
  readonly body: Buffer;

  constructor(frame: WebhookFrame) {
    this.method = frame.method ?? "POST";
    this.path = frame.path ?? "";
    this.query = frame.query ?? "";
    this.headers = frame.headers ?? {};
    this.body = Buffer.from(frame.body_b64 ?? "", "base64");
  }

  /** Case-insensitive header lookup (HTTP semantics: X-Gitlab-Event and x-gitlab-event both hit). */
  header(name: string): string {
    const lowered = name.toLowerCase();
    for (const [k, v] of Object.entries(this.headers)) {
      if (k.toLowerCase() === lowered) return v;
    }
    return "";
  }

  /** Parse the body as JSON. Throws on bad input: malformed JSON upstream should fail visibly. */
  json<T = unknown>(): T {
    return JSON.parse(this.body.toString("utf8")) as T;
  }
}

/** The reply sent back upstream. status=0 plus an error means the plugin failed to handle it (the
 * platform translates that into a 5xx). */
export interface WebhookResponse {
  status: number;
  headers?: Record<string, string>;
  body?: Buffer | string;
}

export function ok(): WebhookResponse {
  return { status: 200 };
}

/** For the cases that must return a body (Feishu's URL-verification challenge, say). */
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
