import type { MetadataRoute } from "next";
import { SITE_ORIGIN } from "@/lib/site";

// 只列公开且稳定 200 的静态路径，三类不列：
//   * 受登录保护的 /admin、/account、/settings、/developer、/new、/contribute、/invites
//     （AuthGate.PROTECTED_PREFIXES）；
//   * /catalog/[id] 详情页（内容全靠客户端取数）；
//   * /login、/setup 与只做客户端外跳的 /developers，没有可收录内容。
const PUBLIC_PATHS = ["/", "/home", "/explore", "/community", "/downloads"];

// 构建期求值一次、只产出静态 /sitemap.xml：不查目录服务，也不发任何网络请求。
export default function sitemap(): MetadataRoute.Sitemap {
  return PUBLIC_PATHS.map((path) => ({ url: `${SITE_ORIGIN}${path}` }));
}
