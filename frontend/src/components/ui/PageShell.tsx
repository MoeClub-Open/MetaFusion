import React from "react";

/**
 * 页面外壳：全站唯一的「最大宽度 + 水平内边距 + 垂直节奏」出处。
 *
 * 为什么必须集中：页面各自写 px-* 与 max-w-* 时，同一份内容在首页/详情页/管理台会落在
 * 不同的左边界上，切页面与切页签时视觉基线跳动；宽度也只有一处能改。
 * 各页面不得再自写这些类，由 scripts/check_page_shell.mjs 检查。
 * 导航条与管理台内栏复用 PageContainer，顶栏内容与页面内容共用同一条左基线。
 */

/** 容器宽度档位：page（主内容）/ narrow（阅读与表单的居中窄列）。 */
export type PageWidth = "page" | "narrow";

/**
 * 横向对齐容器：max-w-page + 统一水平内边距（唯一出处）。
 * width="narrow" 时在 page 档容器内部再收一列 48rem，因此窄页与宽页的内容左基线相同，
 * 不会有"详情页与专用路由左边界差一截"的问题。
 * 整屏流程页（登录/初始化）需要外层不是 <main> 时用 as 换标签。
 */
export function PageContainer({
  width = "page",
  as: Tag = "div",
  className = "",
  style,
  children,
}: {
  width?: PageWidth;
  as?: "div" | "main" | "header" | "footer" | "section";
  className?: string;
  /** 可调宽度的页面浮层可覆盖尺寸，仍复用本容器的默认宽度与水平留白。 */
  style?: React.CSSProperties;
  children: React.ReactNode;
}) {
  return (
    <Tag data-mf-pagecontainer={width} style={style} className={`mx-auto w-full max-w-page px-4 sm:px-6 lg:px-8 ${className}`}>
      {width === "narrow" ? <div className="mx-auto w-full max-w-narrow">{children}</div> : children}
    </Tag>
  );
}

const TITLE_SIZE: Record<string, string> = {
  md: "text-lg sm:text-xl",
  lg: "text-xl sm:text-2xl",
};

/**
 * 可选页头：标题 / 副标题 / 右侧操作位。
 * 页面自带更复杂页头时用 header 属性整体替换，但仍由 PageShell 决定它到正文的间距。
 */
export function PageHeader({
  icon,
  title,
  subtitle,
  actions,
  size = "md",
  bordered = false,
  titleClassName = "",
  subtitleClassName = "",
  className = "",
  children,
}: {
  icon?: React.ReactNode;
  title?: React.ReactNode;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  size?: "md" | "lg";
  bordered?: boolean;
  titleClassName?: string;
  subtitleClassName?: string;
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <header
      data-mf-pagehead=""
      className={`flex flex-col sm:flex-row sm:items-center justify-between gap-3 ${
        bordered ? "border-b border-line pb-3.5" : ""
      } ${className}`}
    >
      {title || subtitle ? (
        <div className="flex items-start gap-2.5 min-w-0">
          {icon}
          <div className="min-w-0">
            {title ? (
              <h1
                className={`font-display font-bold tracking-tight text-text-strong ${TITLE_SIZE[size]} ${titleClassName}`}
              >
                {title}
              </h1>
            ) : null}
            {subtitle ? (
              <p className={`mt-1 text-text-muted ${subtitleClassName || "text-xs"}`}>{subtitle}</p>
            ) : null}
          </div>
        </div>
      ) : null}
      {actions ? <div className="flex flex-wrap items-center gap-2 min-w-0">{actions}</div> : null}
      {children}
    </header>
  );
}

const SPACING: Record<string, string> = {
  sections: "space-y-4",
  compact: "space-y-2",
  none: "",
};

export interface PageShellProps {
  /** page：主内容；narrow：阅读与表单的居中窄列（外壳左基线与 page 一致）。 */
  width?: PageWidth;
  /** 页头简写；给了 title/subtitle/actions 时按标准页头渲染。 */
  title?: React.ReactNode;
  subtitle?: React.ReactNode;
  icon?: React.ReactNode;
  actions?: React.ReactNode;
  /** 完全自定义页头（替代 title/subtitle/actions）。 */
  header?: React.ReactNode;
  /** 外层 <main> 的附加类。 */
  className?: string;
  /** 容器（页头 + 正文共同所在的盒子）的附加类。 */
  containerClassName?: string;
  /** 正文包裹层的附加类。 */
  contentClassName?: string;
  /** 垂直居中：用于登录态缺失、加载失败这类整页空态。 */
  center?: boolean;
  /** 区块之间的纵向节奏。 */
  spacing?: "sections" | "compact" | "none";
  children: React.ReactNode;
}

/**
 * 页面外壳。页头下沿到首个内容块固定 16px（与区块间距同口径），
 * 因此每页的「页头 → 正文」距离一致，切页签时正文不会上下跳。
 */
export function PageShell({
  width = "page",
  title,
  subtitle,
  icon,
  actions,
  header,
  className = "",
  containerClassName = "",
  contentClassName = "",
  center = false,
  spacing = "sections",
  children,
}: PageShellProps) {
  const head =
    header ??
    (title || subtitle || actions ? (
      <PageHeader icon={icon} title={title} subtitle={subtitle} actions={actions} />
    ) : null);
  return (
    <main
      data-mf-shell=""
      className={`mf-enter relative z-10 w-full flex-1 ${center ? "grid place-items-center" : ""} ${className}`}
    >
      <PageContainer
        width={width}
        className={`${center ? "py-8 grid place-items-center" : "py-4 sm:py-5"} ${containerClassName}`}
      >
        {/* 页头与正文同层：两者间距 = spacing（默认 16px，与区块间距同口径）。 */}
        <div
          className={`${
            center ? "w-full flex flex-col items-center justify-center gap-4 text-center" : "space-y-4"
          }`}
        >
          {head ? <div data-mf-pagehead="">{head}</div> : null}
          <div data-mf-pagecontent="" className={`${SPACING[spacing]} ${contentClassName}`}>
            {children}
          </div>
        </div>
      </PageContainer>
    </main>
  );
}
