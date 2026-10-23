import React from "react";

/**
 * 卡片：统一的边框 / 圆角 / 背景 / 内边距口径。
 * 页面里不要再手写 rounded-* + border + bg-surface + p-* 这套组合（密度口径见组件默认值）。
 * 圆角取 rounded-card，与控件和弹窗共用统一弧度。
 */
const TONE: Record<string, string> = {
  surface: "border-line bg-surface",
  subtle: "border-line-subtle bg-surfaceSubtle",
  plain: "border-line-subtle bg-surface/80 backdrop-blur-md",
};

const PADDING: Record<string, string> = {
  /** 管理台/列表卡片。 */
  card: "p-3 sm:p-4",
  /** 页面区块。 */
  section: "p-3 sm:p-4",
  none: "",
};

export function Card({
  id,
  tone = "surface",
  padding = "card",
  className = "",
  children,
}: {
  /** 分节锚点（#overview 等）：容器就是分节本身，锚点不能另挂一层包装。 */
  id?: string;
  tone?: "surface" | "subtle" | "plain";
  padding?: "card" | "section" | "none";
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div id={id} className={`rounded-card border ${TONE[tone]} ${PADDING[padding]} ${className}`}>{children}</div>
  );
}

/** 卡片内标题：与下方第一行保持 6px（管理台既有口径）。 */
export function CardTitle({
  icon,
  children,
  actions,
  className = "",
}: {
  icon?: React.ReactNode;
  children: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`flex items-center justify-between gap-2 ${className}`}>
      <h3 className="text-sm font-semibold text-text-strong flex items-center gap-1.5 mb-1.5">
        {icon}
        <span>{children}</span>
      </h3>
      {actions ? <div className="flex items-center gap-2 shrink-0">{actions}</div> : null}
    </div>
  );
}
