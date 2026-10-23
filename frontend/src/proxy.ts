import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { SESSION_COOKIE_NAME, SESSION_HINT_COOKIE_NAME } from "./lib/sessionHint";

// 会话提示：HttpOnly 的 mf_session 前端读不到，这里同步一个可读的标记，
// 让未登录访客的页面不再请求 /api/auth/me（见 src/lib/sessionHint.ts）。
export function proxy(req: NextRequest) {
  const res = NextResponse.next();
  const hasSession = !!req.cookies.get(SESSION_COOKIE_NAME)?.value;
  const hinted = req.cookies.get(SESSION_HINT_COOKIE_NAME)?.value === "1";
  if (hasSession && !hinted) {
    res.cookies.set(SESSION_HINT_COOKIE_NAME, "1", { path: "/", maxAge: 60 * 60 * 24 * 30, sameSite: "lax" });
  } else if (!hasSession && req.cookies.has(SESSION_HINT_COOKIE_NAME)) {
    res.cookies.delete(SESSION_HINT_COOKIE_NAME);
  }
  return res;
}

export const config = {
  matcher: ["/((?!api/|_next|favicon.ico|.*\\..*).*)"],
};
