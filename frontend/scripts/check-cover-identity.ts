// 直接使用 Bun 运行当前 TypeScript 实现。
import { coverIdentity, coverHash, coverRefCode } from "../src/lib/coverIdentity.ts";

let failed = 0;
const check = (name, cond, detail) => {
  if (cond) {
    console.log("ok   " + name);
  } else {
    failed += 1;
    console.error("FAIL " + name + (detail ? " :: " + detail : ""));
  }
};

const refOf = (input) => coverRefCode(coverHash(coverIdentity(input)));
const id = "01a0aafa-a044-74d5-9e69-e9d5b3262f0b";

const zh = refOf({ id, title: "美国恐怖故事：天启" });
const en = refOf({ id, title: "American Horror Story: Apocalypse" });
const ja = refOf({ id, title: "アメリカン・ホラー・ストーリー: アポカリプス" });
check("同一实体三种界面语言得到同一个 REF 码", zh === en && en === ja, [zh, en, ja].join(" / "));
check("REF 码形如 MF-XXXXXX", /^MF-[0-9A-F]{6}$/.test(zh), zh);
check("身份串在有 id 时不包含展示题名", !coverIdentity({ id, title: "美国恐怖故事：天启" }).includes("美国恐怖故事"));
check("不同实体的 REF 码不同",
  refOf({ id: "01a0aafa-a044-74d5-9e69-e9d5b3262f0b" }) !== refOf({ id: "01a0a816-7592-7157-9839-b741b6269329" }));
// 无 id 的临时封面：退到原语言题名，同样不随界面语言变化。
const noIdZh = refOf({ originalTitle: "Original Title", title: "中文题名" });
const noIdJa = refOf({ originalTitle: "Original Title", title: "日本語題名" });
check("无 id 时用原语言题名作身份（不随界面语言变化）", noIdZh === noIdJa, noIdZh + " / " + noIdJa);
check("既无 id 也无原语言题名时才用展示题名（画面仍可渲染）",
  coverIdentity({ title: "只有展示题名" }) === "title:只有展示题名");

if (failed > 0) {
  console.error("\n" + failed + " 项失败");
  process.exit(1);
}
console.log("\n程序封面身份/引用码与界面语言无关：全部通过");
