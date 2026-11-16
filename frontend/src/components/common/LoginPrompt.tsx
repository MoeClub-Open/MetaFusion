"use client";

import React, { useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { LogIn } from "lucide-react";
import { Card } from "@/components/ui/Card";
import { useI18n } from "@/i18n/I18nProvider";
import { getAuthLoginUrl } from "@/lib/services";

export interface LoginPromptProps {
  title: React.ReactNode;
  description?: React.ReactNode;
  actionLabel?: React.ReactNode;
  /** 显式指定登录后的回跳路径；省略时自动保留当前完整路径、查询串与锚点。 */
  returnTo?: string;
  icon?: React.ReactNode;
  /** 内联紧凑条：用于评论框、展开面板、弹窗等局部操作位。 */
  compact?: boolean;
  tone?: "surface" | "subtle";
  className?: string;
}

export function LoginPrompt({
  title,
  description,
  actionLabel,
  returnTo,
  icon,
  compact = false,
  tone,
  className = "",
}: LoginPromptProps) {
  const { t } = useI18n();
  const pathname = usePathname();
  const initialTarget = returnTo || pathname || "/home";
  const [loginHref, setLoginHref] = useState(() => getAuthLoginUrl(initialTarget));

  useEffect(() => {
    if (returnTo) {
      setLoginHref(getAuthLoginUrl(returnTo));
      return;
    }
    const target = window.location.pathname + window.location.search + window.location.hash;
    setLoginHref(getAuthLoginUrl(target || "/home"));
  }, [pathname, returnTo]);

  const resolvedTone = tone ?? (compact ? "subtle" : "surface");
  const resolvedAction = actionLabel ?? t("nav.login");

  if (compact) {
    return (
      <Card
        tone={resolvedTone}
        padding="none"
        className={"flex flex-wrap items-center justify-between gap-3 px-3.5 py-2.5 " + className}
      >
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          <span
            aria-hidden
            className="grid size-7 shrink-0 place-items-center rounded-chip border border-primary/20 bg-primary/10 text-primary"
          >
            {icon ?? <LogIn className="size-3.5" />}
          </span>
          <div className="min-w-0">
            <p className="text-xs font-medium text-text-strong leading-snug">{title}</p>
            {description ? (
              <p className="mt-0.5 text-[11px] text-text-muted leading-relaxed">{description}</p>
            ) : null}
          </div>
        </div>
        <a
          href={loginHref}
          className="inline-flex h-8 shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-control bg-primary px-3 text-xs font-semibold text-primary-foreground shadow-2xs transition-colors duration-fast ease-soft hover:bg-primary/90 mf-focus"
        >
          <LogIn className="size-3.5 shrink-0" aria-hidden />
          <span>{resolvedAction}</span>
        </a>
      </Card>
    );
  }

  return (
    <Card
      tone={resolvedTone}
      padding="none"
      className={"flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3.5 px-4 py-3.5 sm:px-5 sm:py-4 " + className}
    >
      <div className="flex min-w-0 items-start sm:items-center gap-3">
        <span
          aria-hidden
          className="grid size-9 shrink-0 place-items-center rounded-control border border-primary/20 bg-primary/10 text-primary"
        >
          {icon ?? <LogIn className="size-4" />}
        </span>
        <div className="min-w-0 space-y-0.5">
          <p className="text-sm font-medium text-text-strong leading-snug">{title}</p>
          {description ? (
            <p className="text-xs text-text-muted leading-relaxed">{description}</p>
          ) : null}
        </div>
      </div>
      <a
        href={loginHref}
        className="inline-flex h-9 shrink-0 self-start sm:self-auto items-center justify-center gap-1.5 whitespace-nowrap rounded-control bg-primary px-4 text-xs font-semibold text-primary-foreground shadow-2xs transition-colors duration-fast ease-soft hover:bg-primary/90 mf-focus"
      >
        <LogIn className="size-3.5 shrink-0" aria-hidden />
        <span>{resolvedAction}</span>
      </a>
    </Card>
  );
}
