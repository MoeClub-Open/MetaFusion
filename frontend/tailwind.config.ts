import type { Config } from "tailwindcss";
import plugin from "tailwindcss/plugin";

const config: Config = {
  content: [
    "./src/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        background: "rgb(var(--bg-rgb) / <alpha-value>)",
        foreground: "rgb(var(--foreground-rgb) / <alpha-value>)",
        surface: "rgb(var(--surface-rgb) / <alpha-value>)",
        surfaceHover: "rgb(var(--surface-hover-rgb) / <alpha-value>)",
        surfaceBorder: "var(--surface-border-color)",
        // 卡片/次要面/静音面：这三组是同一族表面色，值与 bg-surface / bg-surfaceHover
        // 对齐（globals.css 的 --card-rgb / --muted-rgb / --secondary-rgb），
        // 用三通道变量是为了让 bg-muted/30 这类透明度变体也能生成。
        card: {
          DEFAULT: "rgb(var(--card-rgb) / <alpha-value>)",
          foreground: "rgb(var(--foreground-rgb) / <alpha-value>)",
        },
        muted: {
          DEFAULT: "rgb(var(--muted-rgb) / <alpha-value>)",
          // 跟随 --text-muted-color 的深浅取值（由主题色调块给出），承担"次要文字"角色。
          foreground: "rgb(var(--muted-foreground-rgb) / <alpha-value>)",
        },
        secondary: {
          DEFAULT: "rgb(var(--secondary-rgb) / <alpha-value>)",
          foreground: "rgb(var(--foreground-rgb) / <alpha-value>)",
        },
        // 默认描边色：值是 --line-color 在其所在表面上的不透明等价值（由 gen-theme.mjs 的
        // blendOver / globals.css 的 --border-rgb 给出），因为 <alpha-value> 要同时支撑
        // border-border 与 border-border/50 两种写法。
        border: "rgb(var(--border-rgb) / <alpha-value>)",
        theme: "rgb(var(--border-rgb) / <alpha-value>)",
        destructive: {
          DEFAULT: "rgb(var(--destructive-rgb) / <alpha-value>)",
          foreground: "rgb(var(--destructive-foreground-rgb) / <alpha-value>)",
        },
        primary: {
          // 三通道形式：裸 var() 让 Tailwind 拿不到 <alpha-value>，于是 bg-primary/10、
          // hover:bg-primary/90、border-primary/30 这类透明度变体会被整体跳过。
          DEFAULT: "rgb(var(--primary-rgb) / <alpha-value>)",
          // 主色上的文字用每套配色自己的对照色（亮主色配深字），
          // 而不是 text-white + html.light 白名单补丁。
          foreground: "rgb(var(--primary-contrast-rgb) / <alpha-value>)",
          hover: "var(--primary-hover-color)",
          light: "var(--primary-light-color)",
        },
        // 强调前景/叠加色（emphasis）：迁移前源码里写死的字面白（text-white、hover:text-white、
        // divide-white/[0.06]、bg-white/[0.04]…）的语义角色。深色模式取值与字面白逐通道相等
        // （255 255 255），所以迁移到它以后深色外观零变化；浅色模式取最强前景色，与上一轮
        // html.light 白名单补丁映射到的 --text-strong-color 取值一致。三通道形式让
        // text-emphasis/60、bg-emphasis/[0.04] 这类透明度变体也能生成。
        emphasis: "rgb(var(--emphasis-rgb) / <alpha-value>)",
        // 状态色：浅色下自动换成同色相深色（白底 ≥5:1），别再直接写 text-amber-400 这类
        // 只在深色底上够亮的调色板类。soft = 深色模式下浅一档（原 300 档）的角色色；
        // 浅色模式两档合并成同一个角色色（浅色下浅档在白底读不到，原补丁也是这么折的）。
        // 三通道形式是为了 text-warn/90 这类透明度变体（裸 var() 拿不到 <alpha-value>）。
        warn: {
          DEFAULT: "rgb(var(--state-warn-rgb) / <alpha-value>)",
          soft: "rgb(var(--state-warn-soft-rgb) / <alpha-value>)",
        },
        success: {
          DEFAULT: "rgb(var(--state-success-rgb) / <alpha-value>)",
          soft: "rgb(var(--state-success-soft-rgb) / <alpha-value>)",
        },
        danger: {
          DEFAULT: "rgb(var(--state-danger-rgb) / <alpha-value>)",
          soft: "rgb(var(--state-danger-soft-rgb) / <alpha-value>)",
        },
        info: {
          DEFAULT: "rgb(var(--state-info-rgb) / <alpha-value>)",
          soft: "rgb(var(--state-info-soft-rgb) / <alpha-value>)",
        },
        alt: {
          DEFAULT: "rgb(var(--state-alt-rgb) / <alpha-value>)",
          soft: "rgb(var(--state-alt-soft-rgb) / <alpha-value>)",
        },
        accent: {
          DEFAULT: "rgb(var(--accent-rgb) / <alpha-value>)",
          gold: "#f59e0b",
          cyan: "#06b6d4",
          emerald: "#10b981",
        },
        // 语义色：界面里只用这几个，不再逐处写 `border-black/10 dark:border-white/[0.06]`。
        // 深色/浅色由 CSS 变量切换（globals.css），组件侧与主题解耦。
        line: {
          DEFAULT: "var(--line-color)",
          subtle: "var(--line-subtle-color)",
          strong: "var(--line-strong-color)",
        },
        surfaceSubtle: "var(--surface-subtle-color)",
        text: {
          strong: "var(--text-strong-color)",
          body: "var(--text-body-color)",
          muted: "var(--text-muted-color)",
          faint: "var(--text-faint-color)",
        },
      },
      fontFamily: {
        sans: [
          // next/font 自托管字体优先（变量缺席时回落到系统字体），见 layout.tsx
          "var(--font-inter)",
          "Inter",
          "ui-sans-serif",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Noto Sans SC",
          "sans-serif",
        ],
        mono: [
          "var(--font-jetbrains-mono)",
          "JetBrains Mono",
          "Fira Code",
          "ui-monospace",
          "SFMono-Regular",
          "monospace",
        ],
        display: [
          "var(--font-instrument-serif)",
          "Instrument Serif",
          "Noto Serif SC",
          "Georgia",
          "serif",
        ],
      },
      // 圆角只来自 globals.css 的语义变量：小标记 4 / 控件与卡片 8 / 浮层 12。
      // 常用尺寸类共享同一套弧度，页面不再因选择 md/lg/xl 而出现不同曲率。
      borderRadius: {
        none: "0px",
        xs: "var(--radius-chip)",
        sm: "var(--radius-chip)",
        DEFAULT: "var(--radius-control)",
        md: "var(--radius-control)",
        lg: "var(--radius-card)",
        xl: "var(--radius-card)",
        "2xl": "var(--radius-panel)",
        "3xl": "var(--radius-panel)",
        card: "var(--radius-card)",
        panel: "var(--radius-panel)",
        control: "var(--radius-control)",
        chip: "var(--radius-chip)",
        tech: "var(--radius-control)",
        pill: "var(--radius-round)",
        full: "var(--radius-round)",
      },
      // v3 默认间距刻度没有 15 与 6.5：顶栏 sm:h-15（3.75rem，与 --mf-header-h 对齐）
      // 与 h-6.5（1.625rem）原本不生成，等于 h-15/h-6.5 写了没效果。
      spacing: { 0.2: "0.05rem", 6.5: "1.625rem", 15: "3.75rem" },
      // 容器宽度只留三档：page（主内容）/ narrow（阅读与表单）/ form（登录等窄卡）。
      maxWidth: {
        page: "80rem",
        narrow: "48rem",
      },
      // 动效统一：时长与缓动固定，组件不再各写 duration-150/200/300。
      transitionDuration: { fast: "120ms", base: "200ms" },
      // v3 的透明度刻度是 5 的倍数，bg-card/98、bg-primary/8 这类写法不会生成；补上用到的档位。
      opacity: { 8: "0.08", 98: "0.98" },
      transitionTimingFunction: { soft: "cubic-bezier(0.16, 1, 0.3, 1)" },
      boxShadow: {
        // v4 把 shadow-sm 改名 shadow-xs、v3 的 shadow-sm 又叫 shadow-2xs 的前身；
        // 源码里写的 shadow-xs/shadow-2xs 在 v3 下不生成，这里按 v3 等效值补上。
        xs: "0 1px 2px 0 rgb(0 0 0 / 0.05)",
        "2xs": "0 1px 1px 0 rgb(0 0 0 / 0.04)",
        soft: "0 8px 24px -8px rgba(0,0,0,0.4)",
        elevated: "0 16px 48px -12px rgba(0,0,0,0.55)",
        glow: "0 2px 20px rgba(59,130,246,0.12)",
        "glow-amber": "0 2px 20px rgba(245,158,11,0.14)",
      },
      animation: {
        // animate-in / animate-scale-up 来自 tailwindcss-animate（未装）：补上等效进/出场。
        in: "fadeIn 0.2s cubic-bezier(0.16,1,0.3,1) both",
        "scale-up": "scaleUp 0.18s cubic-bezier(0.16,1,0.3,1) both",
        "fade-in": "fadeIn 0.35s cubic-bezier(0.16,1,0.3,1)",
        "slide-up": "slideUp 0.4s cubic-bezier(0.16,1,0.3,1)",
        "scale-in": "scaleIn 0.18s cubic-bezier(0.16,1,0.3,1)",
        shimmer: "shimmer 1.6s ease-in-out infinite",
      },
      keyframes: {
        fadeIn: {
          from: { opacity: "0" },
          to: { opacity: "1" },
        },
        slideUp: {
          from: { opacity: "0", transform: "translateY(10px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        scaleIn: {
          from: { opacity: "0", transform: "scale(0.97)" },
          to: { opacity: "1", transform: "scale(1)" },
        },
        scaleUp: {
          from: { opacity: "0", transform: "scale(0.95)" },
          to: { opacity: "1", transform: "scale(1)" },
        },
        shimmer: {
          "0%": { backgroundPosition: "100% 0" },
          "100%": { backgroundPosition: "-100% 0" },
        },
      },
    },
  },
  // 只有在 v4 或装了插件时才存在的写法：不补就是"写了不生效"。
  // 值按 v3/v4 的等效语义给，组件侧不必逐处改名。
  plugins: [
    plugin(({ addUtilities }) => {
      addUtilities({
        ".outline-hidden": { outline: "2px solid transparent", outlineOffset: "2px" },
        ".scrollbar-none": { scrollbarWidth: "none", "-ms-overflow-style": "none" },
        ".scrollbar-none::-webkit-scrollbar": { display: "none" },
        ".scrollbar-thin": { scrollbarWidth: "thin" },
      });
    }),
  ],
};
export default config;
