// 账号服务把稳定错误码放在响应体的 error 字段（respond() 统一只给单一 error）。
// 这里把码映射成四语文案：前端只认 auth.error.* 字典，未知码回退到通用提示，
// 绝不把原始 code 抛给用户看。
//
// 同一句人话对应多个码时走别名表，避免为服务端的近义码各写一条文案。

const AUTH_ERROR_ALIASES: Record<string, string> = {
  // 注册撞名：服务端用 username_or_email_taken，历史上还有下面几种写法
  username_taken: "user_already_exists",
  username_or_email_taken: "user_already_exists",
  email_taken: "user_already_exists",
  // 邀请码无效：服务端 consumeInvite 给 invalid_invite_code
  invite_invalid: "invalid_invite_code",
  // 注册关闭：服务端 Register 给 registration_closed
  registration_disabled: "registration_closed",
  // 注册字段不合规：线上实测到的 invalid_username（前端此前只显示「请求失败」），
  // 以及服务端在别处用的 invalid_password / weak_password 写法。用户名格式与口令长度
  // 是两个不同的问题，分别指向两条文案，不合并成一句泛泛的"格式不对"。
  invalid_password: "invalid_password_length",
  weak_password: "invalid_password_length",
  // 账号停用：服务端 fail() 给的 banned（403），历史上还有 account_disabled 写法。
  banned: "account_disabled",
};

/** 读取响应错误对象上的状态码：只按字段探测，不 import api.ts 的错误类，避免跨模块耦合。 */
export function httpStatusOf(err: unknown): number | undefined {
  const status = (err as { status?: unknown } | null | undefined)?.status;
  return typeof status === "number" ? status : undefined;
}

/**
 * 把账号服务错误码翻译成当前语言的人话；未知码回退到通用请求失败提示。
 * status 只用来兜住"路由不存在/上游不可用"这类没有业务码的情况
 * （例如账号服务尚未部署这些端点时返回的 404 纯文本）。
 *
 * 限流要分辨两层（见 metafusion-auth/internal/handler/login_guard.go 的注释）：
 *   · 请求速率层只回 rate_limited；
 *   · 来源 IP 失败预算耗尽时回 login_blocked，服务端带 Retry-After，但这条链路上的 ApiError
 *     并不携带它，因此文案不承诺具体分钟数（也不显示未替换的 {minutes} 占位符）。
 */
export function authErrorText(
  code: string | null | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
  status?: number,
  fallbackKey = "auth.requestFailed"
): string {
  if (status === 404 || (status !== undefined && status >= 500)) {
    return t("auth.error.service_unavailable");
  }
  const raw = (code || "").trim();
  if (status === 429 || raw === "login_blocked") {
    return t(raw === "login_blocked" ? "auth.error.login_blocked" : "auth.error.rate_limit_exceeded");
  }
  if (!raw) return t(fallbackKey);
  for (const candidate of [raw, AUTH_ERROR_ALIASES[raw]]) {
    if (!candidate) continue;
    const key = `auth.error.${candidate}`;
    const translated = t(key);
    if (translated && translated !== key) return translated;
  }
  return t(fallbackKey);
}
