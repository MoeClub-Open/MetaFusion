import React from "react";

/**
 * 区块标题：统一的字号 + 分隔线 + 下内边距（pb-2.5 与既有密度口径一致）。
 * 页面里不要再各写 text-sm font-semibold + border-b + pb-* 的组合。
 */
export function SectionTitle({
  id,
  icon,
  children,
  actions,
  className = "",
}: {
  id?: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`flex items-center justify-between gap-2 border-b border-line-subtle pb-2.5 ${className}`}
    >
      <h2 id={id} className="text-sm font-semibold text-text-strong flex items-center gap-1.5 min-w-0">
        {icon}
        <span className="truncate">{children}</span>
      </h2>
      {actions ? <div className="flex items-center gap-2 shrink-0">{actions}</div> : null}
    </div>
  );
}
