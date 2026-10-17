"use client";

import { useI18n } from "@/i18n/I18nProvider";

/**
 * Suspense 兜底：兜底节点在 I18n 上下文之外，只有单独成组件才取得到字典文案。
 *
 * 各页原先各写一份，于是同一屏出现三种表现——四处走字典、landing/login/admin 写死英文
 * （词典切到中/日/繁首帧仍是英文）、/catalog/[id] 是 fallback={null} 的白屏。
 * className 保留各页原来的尺寸（全屏 / 100dvh / 窄栏），文案只留这一处。
 */
export function LoadingFallback({
  className = "min-h-screen grid place-items-center font-mono text-sm text-text-faint",
}: {
  className?: string;
}) {
  const { t } = useI18n();
  return <div className={className}>{t("common.loading")}</div>;
}
