"use client";

import { useMemo } from "react";
import { localeCookieName, normalizeLocale } from "@/i18n/routing";
import { getMessages, translate } from "@/i18n/getMessages";

function readCookieLocale(): string | null {
  if (typeof document === "undefined") return null;
  const m = document.cookie.match(new RegExp(`(?:^|;\\s*)${localeCookieName}=([^;]+)`));
  return m ? decodeURIComponent(m[1]!) : null;
}

export default function GlobalError({
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const locale = useMemo(() => normalizeLocale(readCookieLocale()), []);
  const messages = useMemo(() => getMessages(locale), [locale]);
  const t = (key: string, vars?: Record<string, string | number>) => translate(messages, key, vars);

  return (
    // 刻意固定深色：这一层在根布局之外渲染，主题提供者与 public/theme-boot.js 都不在，
    // 读不到（也不该去猜）用户的主题选择——固定深色比先闪错主题再纠正更稳。底部走 --bg-rgb 令牌，
    // 而不是写死的 #0a0c10。
    <html lang={locale} className="dark">
      <body className="font-sans min-h-screen bg-background text-text-strong flex flex-col items-center justify-center p-6 antialiased">
        <div className="max-w-md w-full text-center flex flex-col items-center">
          <div className="w-16 h-16 rounded-card bg-rose-500/10 border border-rose-500/20 text-danger flex items-center justify-center text-2xl font-bold mb-6">
            !
          </div>
          <h1 className="text-2xl font-bold text-emphasis mb-2">
            {t("globalError.title")}
          </h1>
          <p className="text-sm text-text-muted mb-6">
            {t("globalError.desc")}
          </p>
          <button
            onClick={() => reset()}
            className="px-5 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-emphasis text-sm font-medium transition-colors duration-fast ease-soft"
          >
            {t("globalError.reload")}
          </button>
        </div>
      </body>
    </html>
  );
}
