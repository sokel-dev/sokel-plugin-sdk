/**
 * Plugin wire protocol version (see docs/plugin-wire-protocol.md in the platform repository).
 *
 * It only goes up for changes both sides must make together:
 *
 *     1 = the original protocol (replies on the global _INBOX.)
 *     2 = per-group reply prefix (inbox_prefix, v0.5.5)
 *
 * The number is reported at registration, and the platform's min_protocol (sent with the access
 * credentials) is checked before connecting, so an SDK that is too old stops with an upgrade hint
 * instead of failing to register forever.
 */

import { readFileSync } from "node:fs";

import { SokelError } from "./errors.js";

export const WIRE_PROTOCOL = 2;

/** The platform requires a newer wire protocol. Waiting does not fix it; rebuilding with a newer SDK does. */
export class SDKTooOld extends SokelError {
  readonly code = "sdk_too_old";
}

let ident: string | undefined;

/** "node/<package version>", reported at registration so the platform can tell which SDK a replica runs. */
export function sdkIdent(): string {
  if (ident === undefined) {
    try {
      // dist/src/protocol.js → the package root; the same file the release bumps.
      const pkg = JSON.parse(readFileSync(new URL("../../package.json", import.meta.url), "utf8")) as { version?: string };
      ident = `node/${pkg.version ?? "unknown"}`;
    } catch {
      ident = "node/unknown";
    }
  }
  return ident;
}

export function checkProtocol(acc: { min_protocol?: number }): void {
  const need = acc.min_protocol ?? 0;
  if (need > WIRE_PROTOCOL) {
    throw new SDKTooOld(
      `the platform needs wire protocol ${need}, this SDK (${sdkIdent()}) speaks ${WIRE_PROTOCOL}` +
        " — upgrade @sokel-dev/plugin-sdk and redeploy",
    );
  }
}
