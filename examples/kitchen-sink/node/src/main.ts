/**
 * 全能示例插件（Node / TypeScript 实现）。
 *
 * 契约在 ../sokel.yaml 里声明，sokel.gen.ts 由它生成 —— 本文件只写实现，全程 typed。
 * Python 版实现的是**同一份声明**（../python/main.py），两边上报的契约必须逐字节相同。
 *
 * 运行：
 *
 *     pnpm install && pnpm build
 *     SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx node dist/main.js
 */

import { createHash } from "node:crypto";
import { ok, text } from "@sokel-dev/plugin-sdk";
import type { AuthChallenge, AuthState, Ctx, Emitter, SourceCtx, WebhookRequest } from "@sokel-dev/plugin-sdk";

import {
  newPlugin,
  onChatStream,
  onEchoAll,
  onFileDigest,
  onHealthCheck,
  triggerHeartbeat,
  triggerMessage,
} from "./sokel.gen.js";
import type {
  ChatStreamIn, ChatStreamOut, Credential, EchoAllIn, EchoAllOut,
  FileDigestIn, FileDigestOut, HealthCheckOut,
} from "./sokel.gen.js";

const p = newPlugin({ name: "kitchen-sink", version: "1.0.0" });

// —— 操作：每种字段形态原样回显 ——

onEchoAll(p, (ctx: Ctx, in_: EchoAllIn): EchoAllOut => {
  const cred = ctx.credentialAs<Credential>(); // 类型化读凭证，避免裸键拼错
  // 结构联合：运行值就是分支本身的形状（不带 discriminator），按形状判别即可
  const doc = in_.doc;
  const docDesc = Array.isArray(doc)
    ? `块数组（${doc.length} 块）`
    : doc
      ? `文档对象《${doc.title}》`
      : "未给文档";
  const count = in_.count ?? 1;
  return {
    text: in_.text.repeat(Math.max(count, 1)),
    count,
    mode: in_.mode ?? "fast",
    tags: in_.tags ?? [],
    profile: in_.profile,
    points: in_.points ?? [],
    labels: in_.labels ?? {},
    summary:
      `mode=${in_.mode ?? "fast"} ratio=${in_.ratio ?? 0} flag=${in_.flag ?? false} ` +
      `tags=${(in_.tags ?? []).length} points=${(in_.points ?? []).length} ` +
      `extra_keys=${Object.keys(in_.extra ?? {}).sort().join(",")} ${docDesc} region=${cred.region ?? ""}`,
  };
});

// —— 操作：文件入参惰性取字节，出参把字节交回平台登记 ——

onFileDigest(p, async (ctx: Ctx, in_: FileDigestIn): Promise<FileDigestOut> => {
  const data = await ctx.fetch(in_.file);
  const sha256 = createHash("sha256").update(data).digest("hex");
  const report = await ctx.upload(
    "digest.json",
    "application/json",
    new TextEncoder().encode(JSON.stringify({ name: in_.file.name, sha256, size: data.length })),
  );
  return {
    name: in_.file.name ?? "",
    sha256,
    size: data.length,
    extra_count: (in_.extras ?? []).length,
    report,
  };
});

// —— 操作：流式 ——

onChatStream(p, async (_ctx: Ctx, in_: ChatStreamIn, out: Emitter<ChatStreamOut>) => {
  const frames = Math.max(in_.chunks ?? 3, 1);
  let reply = "";
  for (let i = 0; i < frames; i++) {
    const piece = `${in_.prompt}#${i + 1} `;
    reply += piece;
    out.text(piece); // 人类可读增量：节点执行时实时可见
    await sleep(50);
  }
  // 末尾产出类型化变量：进下游节点、按 Outputs 契约校验
  out.vars({ reply: reply.trim(), frames });
});

// —— 凭证体检：平台约定的保留 id，凭证页「测试」调的就是它 ——

onHealthCheck(p, (ctx: Ctx): HealthCheckOut => {
  const cred = ctx.credentialAs<Credential>();
  // 不可用要**返回 ok=false 而不是抛错**：抛错的话平台只能说「调用失败」，
  // 说不出是密钥没配还是上游拒绝，而这两件事的处理办法完全不同。
  if (!cred.api_key) return { ok: false, message: "凭证里没有 API Key" };
  if (!cred.api_key.startsWith("sk-")) {
    return { ok: false, message: `API Key 形如 sk-…，当前是「${cred.api_key.slice(0, 6)}…」` };
  }
  // 真插件在这里发一个最廉价的上游请求（如 GET /me），把上游原文带回 message
  return { ok: true, message: `${cred.base_url ?? ""} / ${cred.region ?? ""} 可用` };
});

// —— webhook：验签 → 推 typed 事件 ——

p.registerWebhook(async (ctx: SourceCtx, req: WebhookRequest) => {
  const cred = ctx.credentialAs<Credential>();
  // 验上游签名是**插件的职责**：各家算法不同，平台不懂上游、插件懂
  if (req.header("X-Sokel-Token") !== cred.api_key) return text(401, "bad token");
  const body = req.json<{ id?: string; chat_id?: string; text?: string }>();
  await triggerMessage(ctx, String(body.id ?? ""), {
    chat_id: String(body.chat_id ?? ""),
    text: String(body.text ?? ""),
  });
  return ok();
});

// —— 常驻事件源：每 30 秒推一条心跳 ——

p.registerSource("heartbeat", "心跳", async (ctx: SourceCtx) => {
  const cred = ctx.credentialAs<Credential>();
  if (!cred.api_key) {
    // 没凭证就自报「待登录」，面板上这一行会亮起来，而不是静悄悄地什么都不做
    ctx.reportStatus("auth_required", "凭证还没登录");
    return;
  }
  while (!ctx.stopped) {
    await triggerHeartbeat(ctx, `hb-${Math.floor(Date.now() / 1000)}`, {
      chat_id: "system",
      at: new Date().toISOString(),
    });
    await sleep(30_000);
  }
});

// —— 协作式认证（kind=input：start → poll → submit）——

const pending = new Map<string, string>(); // authId → 已提交的验证码；生产里换成带过期的存储

p.registerAuth({
  start: (): AuthChallenge => {
    const authId = `demo-${Math.floor(Date.now() / 1000)}`;
    pending.set(authId, "");
    return { authId, prompt: "请输入任意 6 位数字作为验证码", expiresIn: 300 };
  },
  submit: (_ctx, authId, input) => {
    if (!/^\d{6}$/.test(input)) throw new Error("验证码应为 6 位数字");
    pending.set(authId, input);
  },
  poll: (_ctx, authId): AuthState => {
    const code = pending.get(authId);
    // 只有 confirmed 才带 session：中途带出去等于让平台反复覆写凭证行
    return code ? { status: "confirmed", session: { api_key: `sk-demo-${code}` } } : { status: "pending" };
  },
});

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

await p.run();
