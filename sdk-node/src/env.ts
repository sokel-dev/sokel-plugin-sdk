/**
 * 插件侧环境变量的统一读法：把 SOKEL_ 前缀收在一处（对齐 Go 侧 pluginenv）。
 *
 * 没有第二个前缀的兼容层——认第二个前缀省下的是一次重新部署，换来的是一个没人敢摘的包袱。
 */

const PREFIX = "SOKEL_";

/** 读 SOKEL_<name>。name 不带前缀，如 env("TOKEN")。 */
export function env(name: string): string {
  return (process.env[PREFIX + name] ?? "").trim();
}

export function envOr(name: string, fallback: string): string {
  return env(name) || fallback;
}
