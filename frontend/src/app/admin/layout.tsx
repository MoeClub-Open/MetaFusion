import type { Metadata } from "next";
import "./admin.css";

// 管理台是登录后页面，禁止收录：线上 /admin 是 Next 的静态预渲染壳，默认响应带
// s-maxage=31536000（可被中间缓存长期持有），没有 robots meta 时会被搜索引擎收录。
export const metadata: Metadata = {
  robots: { index: false, follow: false },
};

export default function AdminLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return <>{children}</>;
}
