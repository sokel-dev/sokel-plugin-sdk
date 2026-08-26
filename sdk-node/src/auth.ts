/**
 * 协作式凭证认证：有些凭证没法让人填——扫码、验证码回填、OAuth 同意页。
 *
 * 面板点「登录」→ start 拿挑战 →（扫码 / 回填）→ 2s 轮询 poll → confirmed。
 *
 * 形态写在 sokel.yaml 的 credential.auth 里（声明式），处理器挂在保留操作 id
 * auth.start / auth.poll / auth.submit 上——**不要**自己注册叫 auth_start 的业务操作：
 * 那三个名字从来不是保留字，任何插件的同名业务操作都会让面板的按钮凭空出现。
 */

import type { Ctx } from "./runtime.js";

export const PENDING = "pending";
export const SCANNED = "scanned";
export const CONFIRMED = "confirmed";
export const EXPIRED = "expired";

/** start 交出的挑战。面板按 kind 渲染：qr 画二维码，input 显示 prompt 与输入框。 */
export interface AuthChallenge {
  authId?: string;
  kind?: "qr" | "input";
  /** data-uri 形态的二维码图片。 */
  qrImage?: string;
  prompt?: string;
  expiresIn?: number;
}

/** poll 的结果。session 只在 confirmed 时带上——中途带出去等于让平台反复覆写凭证行。 */
export interface AuthState {
  status: string;
  session?: Record<string, unknown>;
}

export interface AuthHandlers {
  start?: (ctx: Ctx) => Promise<AuthChallenge> | AuthChallenge;
  poll?: (ctx: Ctx, authId: string) => Promise<AuthState> | AuthState;
  submit?: (ctx: Ctx, authId: string, input: string) => Promise<void> | void;
}
