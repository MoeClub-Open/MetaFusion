import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // 默认会带 X-Powered-By: Next.js（线上实测主站与三个管理台都有），是给攻击者免费用指纹的
  // 无谓泄露；关掉它不改变任何客户端行为，网关也不读这个头。
  poweredByHeader: false,
  images: {
    remotePatterns: [
      {
        protocol: "https",
        hostname: "**.metafusion.io",
      },
      {
        protocol: "https",
        hostname: "**.r2.cloudflarestorage.com",
      },
      {
        // 官方来源图片（bushiroad / BanG Dream! 官网）：乐队键视觉、成员图与唱片封面都直接引用
        // 官网原始地址，不在白名单里会导致 <Image> 全部渲染失败（整页只显示 alt 文本）。
        protocol: "https",
        hostname: "**.bang-dream.com",
      },
      {
        protocol: "https",
        hostname: "bang-dream.com",
      },
      {
        protocol: "https",
        hostname: "lain.bgm.tv",
      },
      {
        protocol: "https",
        hostname: "image.tmdb.org",
      },
      {
        protocol: "https",
        hostname: "coverartarchive.org",
      },
      {
        protocol: "https",
        hostname: "media.vgm.io",
      },
      // 这里曾有 protocol: "http" / hostname: "localhost"。它把 /_next/image 的取图范围
      // 放到了回环地址上：对本机自托管实例，任何能传 url= 的人都能让优化器去打内网（SSRF），
      // 或至少拿到"这个端口上有东西"的结论。线上从不需要它（图片都来自上面的公开源），
      // 只服务于早期本地调试，故整体移除——本机调试请把图片放到 /public 走相对路径。
      // 相关上游公告：GHSA-9g9p-9gw9-jx7f（remotePatterns 配置不当导致 DoS）、
      // GHSA-2xp9-vwfh-vxw4（图片优化 API 的 RCE，15.5.24 起修）。
    ],
  },
  output: "standalone",
  async rewrites() {
    const origin = (process.env.BACKEND_ORIGIN || "http://127.0.0.1:8080").replace(/\/+$/, "");
    const routes = [{ source: "/api/:path*", destination: `${origin}/api/:path*` }];
    // 宿主机完整联调时 BACKEND_ORIGIN 指向网关；登录 HTML 与账号静态资源须一起转发。
    // 只启动目录后端的默认开发环境不提供账号应用，不能把它伪装成本前端页面。
    if (process.env.NODE_ENV === "development" && process.env.BACKEND_ORIGIN) {
      routes.push(
        { source: "/login", destination: `${origin}/login` },
        { source: "/setup", destination: `${origin}/setup` },
        { source: "/auth-user-assets/:path*", destination: `${origin}/auth-user-assets/:path*` },
      );
    }
    return routes;
  },
};

export default nextConfig;
