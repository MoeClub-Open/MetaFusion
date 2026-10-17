import type { Metadata, Viewport } from "next";
import { Inter, Instrument_Serif, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import { ApplicationBoundary } from "@/components/ApplicationBoundary";
import { CatalogProvider } from "@/components/catalog/CatalogProvider";
import { I18nProvider } from "@/i18n/I18nProvider";
import { ThemeProvider } from "@/lib/themeContext";
import { DEFAULT_ACCENT, DEFAULT_TONE, DEFAULT_PATTERN } from "@/lib/theme.generated";
import { SITE_NAME, SITE_ORIGIN } from "@/lib/site";

// 字体走 next/font 自托管：构建期把 woff2 落到 /_next/static/media，
// 运行时不再请求 fonts.googleapis.com（原来 globals.css 的 @import 写在 @tailwind 之后，
// PostCSS 展开后落在规则后面，按 CSS 规范整条被忽略 —— 五个 Web 字体一个都没生效）。
// 中日韩字形仍交给系统字体（Noto Sans/Serif SC 等）兜底：CJK 子集体积过大，不做自托管。
const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
  display: "swap",
  variable: "--font-inter",
});
const jetbrainsMono = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
  display: "swap",
  variable: "--font-jetbrains-mono",
});
const instrumentSerif = Instrument_Serif({
  subsets: ["latin"],
  weight: ["400"],
  style: ["normal", "italic"],
  display: "swap",
  variable: "--font-instrument-serif",
});

export const metadata: Metadata = {
  // metadataBase：详情页 OG 的 url/images 用站点绝对地址（见 lib/seo.ts），
  // 缺了它 Next 无法把相对路径变成绝对地址。
  metadataBase: new URL(SITE_ORIGIN),
  icons: { icon: "/favicon.svg", shortcut: "/favicon.svg" },
  title: SITE_NAME,
  description: SITE_NAME,
  openGraph: { siteName: SITE_NAME, type: "website", title: SITE_NAME, description: SITE_NAME },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 5,
  viewportFit: "cover",
  themeColor: "#0b0f17",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="zh-CN"
      data-scroll-behavior="smooth"
      className={`dark ${inter.variable} ${jetbrainsMono.variable} ${instrumentSerif.variable}`}
      data-theme-mode="dark"
      data-theme-accent={DEFAULT_ACCENT}
      data-theme-tone={DEFAULT_TONE}
      data-theme-pattern={DEFAULT_PATTERN}
      suppressHydrationWarning
    >
      <head>
        <meta name="theme-color" content="#0b0f17" />
        {/* 首帧前同步应用主题：见 public/theme-boot.js（静态文件，无需内联拼字符串） */}
        <script src="/theme-boot.js" />
      </head>
      <body className="font-sans min-h-screen bg-background text-text-strong flex flex-col antialiased">
        <ThemeProvider>
          <I18nProvider>
            {/* CatalogProvider（模块状态 + 实例初始化状态）在根 layout 挂**一次**：
                它原先只在 /catalog、/account、/admin、/new 四处分别挂载，于是同一类详情页
                （不同实体详情布局若没有共用 Provider）拿到的是两种上下文——
                没挂的那些路由静默落到 context 默认值 []，"capabilities 异常"只在部分路由显形。
                统一挂载后所有路由的 modules 都来自同一次探测（取值已做数组归一，见 CatalogProvider）。
                代价：每个页面多两个很小的请求（/capabilities、/setup）；目录服务连不上时，
                这四处之外的路由也会显示 Provider 自带的"暂时无法连接目录服务"提示条。 */}
            <ApplicationBoundary>
              <CatalogProvider>{children}</CatalogProvider>
            </ApplicationBoundary>
          </I18nProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
