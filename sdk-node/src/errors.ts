/**
 * Typed errors.
 *
 * Errors used to travel as a bare string both ways: callers matched English text, and a plugin had no
 * way to tell the platform "this is a credential problem" or "retrying may help". Throw one of the
 * PluginError subclasses from a handler and its `code` travels next to the message (`code` /
 * `retryable` in the reply, or in a streaming `error` frame). The SDK's own failures extend SokelError.
 */

/** Base class of everything the SDK throws on purpose. */
export class SokelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** The platform answered that it offers no transport (as opposed to a network failure). */
export class NoTransport extends SokelError {}

/** A handler error with a code for the platform. Use a subclass. */
export class PluginError extends SokelError {
  readonly code: string = "";
  readonly retryable: boolean = false;
}

/** A transient failure (rate limit, upstream 5xx, timeout): trying again may work. */
export class Retryable extends PluginError {
  override readonly code = "retryable";
  override readonly retryable = true;
}

/** The credential was rejected upstream (revoked, expired, wrong key). Retrying will not help. */
export class CredentialInvalid extends PluginError {
  override readonly code = "credential_invalid";
}

/** The input is wrong for this operation. Retrying the same input will not help. */
export class InvalidInput extends PluginError {
  override readonly code = "invalid_input";
}

/** The code fields for a reply or error frame; empty for an uncoded error. */
export function errorFields(e: unknown): { code?: string; retryable?: true } {
  if (e instanceof PluginError && e.code) return e.retryable ? { code: e.code, retryable: true } : { code: e.code };
  return {};
}
