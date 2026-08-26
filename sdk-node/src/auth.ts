// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Collaborative credential authentication: some credentials cannot be typed in by hand — a QR scan,
 * a verification code, an OAuth consent page.
 *
 * the panel's "log in" button -> start returns a challenge -> (scan / type it back)
 * -> poll every 2s -> confirmed
 *
 * The shape is declared in sokel.yaml under credential.auth; the handlers hang off the reserved
 * operation ids auth.start / auth.poll / auth.submit. **Do not** register a business operation named
 * auth_start: those three names were never reserved, so any plugin with an operation of that name
 * made the panel's button appear out of nowhere.
 */

import type { Ctx } from "./runtime.js";

export const PENDING = "pending";
export const SCANNED = "scanned";
export const CONFIRMED = "confirmed";
export const EXPIRED = "expired";

/** What start hands back. The panel renders by kind: qr draws a code, input shows the prompt. */
export interface AuthChallenge {
  authId?: string;
  kind?: "qr" | "input";
  /** The QR image as a data-uri. */
  qrImage?: string;
  prompt?: string;
  expiresIn?: number;
}

/** The result of poll. Carry `session` only once confirmed — handing it over earlier makes the
 * platform rewrite the credential row again and again. */
export interface AuthState {
  status: string;
  session?: Record<string, unknown>;
}

export interface AuthHandlers {
  start?: (ctx: Ctx) => Promise<AuthChallenge> | AuthChallenge;
  poll?: (ctx: Ctx, authId: string) => Promise<AuthState> | AuthState;
  submit?: (ctx: Ctx, authId: string, input: string) => Promise<void> | void;
}
