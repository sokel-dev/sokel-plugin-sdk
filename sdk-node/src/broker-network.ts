// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

import { env } from "./env.js";

export type BrokerNetwork = "public" | "internal";

export function brokerNetwork(): BrokerNetwork {
  const network = env("BROKER_NETWORK");
  if (network === "" || network === "public") return "public";
  if (network === "internal") return "internal";
  throw new Error('invalid SOKEL_BROKER_NETWORK: expected "public" or "internal"');
}

/** Internal mode is explicit: missing or invalid discovery data must never select the public URL. */
export function internalBrokerURL(target: unknown): string {
  const message = "internal broker requires nats.internal_url with one valid nats:// or tls:// host (no credentials, path, query, fragment, or server list)";
  if (typeof target !== "string" || !/^(nats|tls):\/\//.test(target) || /[\s\u0000-\u001f\u007f,@?#\\]/.test(target)) {
    throw new Error(message);
  }
  try {
    const url = new URL(target);
    if (!url.hostname || url.username || url.password || url.pathname || target.endsWith(":")) throw new Error(message);
    if (url.port && (Number(url.port) < 1 || Number(url.port) > 65535)) throw new Error(message);
  } catch {
    throw new Error(message);
  }
  return target;
}
