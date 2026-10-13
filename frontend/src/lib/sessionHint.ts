// 会话 Cookie（mf_session，账号服务下发）是 HttpOnly，浏览器脚本读不到。
// middleware 在每次页面请求时看到它就种一个可读的提示 Cookie，前端据此判断
// 「可能已登录」：没有提示时直接当未登录，不再请求 /api/auth/me 白拿一个 401。
// 提示只用来省请求，不代表身份；真正的身份仍以 /api/auth/me 为准。
export const SESSION_COOKIE_NAME = "mf_session";
export const SESSION_HINT_COOKIE_NAME = "mf_has_session";

export function hasSessionHint(): boolean {
  if (typeof document === "undefined") return false;
  return new RegExp(`(?:^|;\\s*)${SESSION_HINT_COOKIE_NAME}=1(?:;|$)`).test(document.cookie);
}

export function clearSessionHint(): void {
  if (typeof document === "undefined") return;
  document.cookie = `${SESSION_HINT_COOKIE_NAME}=; path=/; max-age=0; samesite=lax`;
}
