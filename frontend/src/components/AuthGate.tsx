"use client";

import React, { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/authContext";
import { fetchSetupStatus } from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";
import { isEditEntry } from "@/lib/entityRoutes";

const PROTECTED_PREFIXES = [
  // /account 已不自带登录表单：未登录访问统一跳 /login（见 app/account/page.tsx 的说明）。
  "/account",
  "/admin",
  "/developer",
  "/settings",
  "/invites",
  "/contribute",
  "/new",
];

function isProtectedPath(pathname: string | null): boolean {
  if (!pathname || pathname === "/") return false;
  return PROTECTED_PREFIXES.some((p) => pathname === p || pathname.startsWith(p + "/"));
}

let cachedSetupStatus: { is_initialized: boolean } | null = null;

export const AuthGate: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { user, loading } = useAuth();
  const { t } = useI18n();
  const pathname = usePathname();
  const router = useRouter();

  const [setupChecked, setSetupChecked] = useState(cachedSetupStatus !== null);
  // `/catalog/[id]?edit=1` 不在受保护前缀里，但它就是编辑器入口，门槛必须与 /new 一致。
  // 查询串只能在客户端读（见下面重定向里的说明），故先按 false 渲染一帧，挂载后并入同一判定；
  // EntityEditor 的未登录空态带登录链接，这一帧不再是死胡同。
  const [editEntry, setEditEntry] = useState(false);
  const isProtected = isProtectedPath(pathname) || editEntry;

  useEffect(() => {
    setEditEntry(isEditEntry(typeof window !== "undefined" ? window.location.search : ""));
  }, [pathname]);

  useEffect(() => {
    // 首次检测系统是否完成 OOBE 初始化
    if (cachedSetupStatus && cachedSetupStatus.is_initialized) {
      if (pathname === "/setup") {
        router.replace("/home");
      }
      return;
    }

    fetchSetupStatus()
      .then((status) => {
        cachedSetupStatus = status;
        if (!status.is_initialized) {
          // 系统未初始化且当前不在 /setup，则强制跳转 /setup
          if (pathname !== "/setup") {
            router.replace("/setup");
          }
        } else {
          // 系统已完成初始化且当前访问 /setup，则重定向回首页
          if (pathname === "/setup") {
            router.replace("/home");
          }
        }
      })
      .catch(() => {
        // 网络或后端异常时默认放行
      })
      .finally(() => {
        setSetupChecked(true);
      });
  }, [pathname, router]);

  useEffect(() => {
    if (!loading && !user && isProtected && pathname !== "/setup") {
      // 回跳目标必须带上查询串：`/new?kind=release`、`/settings?tab=password`、`?edit=1`，
      // 以及账号服务 302 过来的 `/account?return_to=<授权请求>`——只带 pathname 会让用户
      // 登录后停在丢了参数的页面上（第三方 OAuth 续授权就是这么断链的）。
      // 读 window.location.search 而不是 useSearchParams：AuthGate 挂在根布局上，
      // useSearchParams 会让所有静态页在构建期额外要求 Suspense 边界。
      const search = typeof window !== "undefined" ? window.location.search : "";
      router.replace(`/login?redirect=${encodeURIComponent(`${pathname}${search}`)}`);
    }
  }, [loading, user, isProtected, pathname, router]);

  // 仅在受保护路由且正在重定向未登录用户时显示阻断全屏加载，普通公开页面（如首页）直接平滑渲染
  if (isProtected && (loading || !setupChecked)) {
    return (
      <div className="min-h-screen grid place-items-center">
        <div className="flex flex-col items-center gap-3">
          <div className="w-8 h-8 rounded-full border-2 border-primary/30 border-t-primary animate-spin" />
          <span className="font-mono text-xs text-text-faint tracking-wider">{t("auth.initializing")}</span>
        </div>
      </div>
    );
  }

  // Block protected routes while redirecting
  if (!user && isProtected && pathname !== "/setup") {
    return null;
  }

  return <>{children}</>;
};

