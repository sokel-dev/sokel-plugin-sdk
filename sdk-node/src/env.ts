// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * One place to read the plugin's environment variables (mirrors pluginenv on the Go side).
 *
 * There is no compatibility layer for a second prefix: accepting one saves a single redeploy and
 * buys a piece of history nobody dares remove.
 */

const PREFIX = "SOKEL_";

/** Read SOKEL_<name>. `name` carries no prefix, e.g. env("TOKEN"). */
export function env(name: string): string {
  return (process.env[PREFIX + name] ?? "").trim();
}

export function envOr(name: string, fallback: string): string {
  return env(name) || fallback;
}
