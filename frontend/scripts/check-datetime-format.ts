// 直接使用 Bun 运行当前 TypeScript 实现。
import { isoDate, isoTimestamp, localDateTime } from "../src/lib/datetime.ts";

let failed = 0;
const check = (name, cond, detail) => {
  if (cond) console.log("ok   " + name);
  else { failed += 1; console.error("FAIL " + name + (detail ? " :: " + detail : "")); }
};

const at = "2026-09-17T01:32:13Z";
check("时间戳按 ISO 8601 秒精度展示", isoTimestamp(at) === "2026-09-17T01:32:13Z", isoTimestamp(at));
check("日期按 ISO 日期展示（补零）", isoDate(at) === "2026-09-17", isoDate(at));
check("正文不再出现斜杠写法", !isoTimestamp(at).includes("/") && !isoDate(at).includes("/"));
check("本地化写法仍可读（只进 title）", typeof localDateTime(at, "zh-CN") === "string" && localDateTime(at, "zh-CN").length > 0);
check("无效值返回空串而不抛错",
  isoTimestamp("") === "" && isoTimestamp("not-a-date") === "" && isoDate(null) === "" && localDateTime(undefined, "zh-CN") === "");

if (failed > 0) { console.error("\n" + failed + " 项失败"); process.exit(1); }
console.log("\n时间戳展示口径统一为 ISO 8601：全部通过");
