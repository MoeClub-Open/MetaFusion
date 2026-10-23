#!/usr/bin/env node
// 语言单一来源生成器：把 ISO 639-1 全集 + 常用 BCP-47 变体生成成前端常量表。
//
//   bun scripts/gen-languages.ts           写入 src/lib/languages.data.json
//   bun scripts/gen-languages.ts --check   只校验（不一致或自检失败时非 0 退出）
//
// 来源：
//   1) ISO 639-1 代码表——本文件内嵌的 ISO_639_1 常量，取自美国国会图书馆维护的
//      ISO 639-1 Code Set（184 个双字母码）。代码表本身极稳定，内嵌是为了离线可复现：
//      不联网、不受运行时 CLDR 版本差异影响。
//   2) 语言名称——Node/Bun 自带完整 ICU 的 CLDR 数据（Intl.DisplayNames）：英文名取 locale
//      en，自称（native）取该语言自身的 locale；CLDR 没有该 locale 时自称回落英文名，
//      再用下面的 CURATED 覆盖表兜底。
//   3) 变体与别名——手工维护的 CURATED 表：只收录项目里确实会写入、或确有独立书写传统的
//      变体（zh-CN / zh-TW / ja-JP / en-US / pt-BR）；其余地区变体靠 languages.ts 的
//      「语言子标签前缀回退」（fr-CA 到 fr）覆盖，不逐个列举。
//
// 生成物随代码提交：运行时只读 src/lib/languages.data.json，不查网络、不调 Intl。
// 改了 CURATED 或 ISO 代码表就跑 bun run langs:build；改了源码没重生成时 langs:check 会红。

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// 生成物与运行时 ICU 的 CLDR 快照绑定：必须用 bun 生成。node 的 ICU 覆盖更窄，
// ba / ce / tl 这些语言在 node 下拿不到自称会回落英文名，同一份源码会生成出两份不同的表。
if (!process.versions.bun) {
  console.error("请用 bun 运行：bun scripts/gen-languages.ts（node 的 ICU 覆盖不全，会生成出不一样的自称表）");
  process.exit(1);
}

const HERE = path.dirname(fileURLToPath(import.meta.url));
const CHECK = process.argv.includes("--check");
const OUT = path.join(HERE, "../src/lib/languages.data.json");
const MODULE = path.join(HERE, "../src/lib/languages.ts");

// ISO 639-1 全集（184 码，来源见文件头）。
const ISO_639_1 = (
  "aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de dv dz " +
  "ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik io " +
  "is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr " +
  "ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si " +
  "sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh " +
  "yi yo za zh zu"
).split(/\s+/).filter(Boolean);

// 与 languages.ts 的 UI_LOCALE_CODES 必须一致：界面四语（字典、cookie、html lang 都用它）。
const UI_LOCALE_CODES = ["zh-CN", "zh-TW", "ja-JP", "en-US"];

// 编目/标题回退链的默认语种顺序（titles.ts 的 LOCALE_ORDER 来源）。
const DEFAULT_TITLE_ORDER = ["zh-CN", "zh-TW", "ja-JP", "en-US", "ko"];

// 手工覆盖：名称 / 别名 / 常用度。只写 CLDR 给不出或给得不对的部分。
// aliases = 同一语言的等价写法（同一语言的不同 BCP-47 书写），互相等价、可双向解析。
const CURATED = {
  "zh-CN": {
    native: "简体中文",
    en: "Chinese (Simplified)",
    nameZh: "简体中文",
    nameJa: "簡体字中国語",
    // zh 是不带地区的语言子标签：本项目一律按简体处理（与既有回退链一致）。
    aliases: ["zh", "zh-Hans", "zh-SG", "zh-MY", "zho", "chi"],
    ui: true,
    popular: true,
    regional: true,
  },
  "zh-TW": {
    native: "繁體中文",
    en: "Chinese (Traditional)",
    nameZh: "繁體中文",
    nameJa: "繁体字中国語",
    // 繁体由字形决定而不是地区：zh-Hant / zh-HK / zh-MO 都归这里。
    aliases: ["zh-Hant", "zh-HK", "zh-MO"],
    ui: true,
    popular: true,
    regional: true,
  },
  "ja-JP": {
    native: "日本語",
    en: "Japanese",
    nameZh: "日语",
    nameJa: "日本語",
    // 日语没有会改变题名的地区变体：ja 与 ja-JP 是同一语言的两种写法。
    aliases: ["ja", "jpn"],
    ui: true,
    popular: true,
  },
  "en-US": {
    native: "English (US)",
    en: "English",
    nameZh: "英语（美国）",
    nameJa: "英語（アメリカ）",
    // en 不带地区时本项目按 en-US 处理（字典键、definitions 名称键都是 en-US）。
    aliases: ["en", "eng"],
    ui: true,
    popular: true,
  },
  ko: { native: "한국어", en: "Korean", aliases: ["ko-KR", "kor"], popular: true, nameZh: "韩语", nameJa: "韓国語" },
  pt: { native: "Português", en: "Portuguese", aliases: ["pt-PT", "por"], popular: true, nameZh: "葡萄牙语", nameJa: "ポルトガル語" },
  "pt-BR": {
    native: "Português (Brasil)",
    en: "Portuguese (Brazil)",
    nameZh: "葡萄牙语（巴西）",
    nameJa: "ポルトガル語（ブラジル）",
    popular: true,
    regional: true,
  },
  fr: { aliases: ["fr-FR", "fr-CA", "fra", "fre"], nameZh: "法语", nameJa: "フランス語" },
  de: { aliases: ["de-DE", "de-AT", "de-CH", "deu", "ger"], nameZh: "德语", nameJa: "ドイツ語" },
  es: { aliases: ["es-ES", "es-MX", "es-419", "spa"], nameZh: "西班牙语", nameJa: "スペイン語" },
  it: { aliases: ["it-IT", "ita"], nameZh: "意大利语", nameJa: "イタリア語" },
  ru: { aliases: ["ru-RU", "rus"], nameZh: "俄语", nameJa: "ロシア語" },
  th: { aliases: ["th-TH", "tha"], nameZh: "泰语", nameJa: "タイ語" },
  vi: { aliases: ["vi-VN", "vie"], nameZh: "越南语", nameJa: "ベトナム語" },
  // 已废弃的 ISO 639 旧码仍被 BCP-47 解析器接受，存量数据里可能出现。
  he: { aliases: ["iw"] },
  id: { aliases: ["in"] },
  yi: { aliases: ["ji"] },
  // ISO 639-2/3 三字母码也收在同一张表里：original_language 与外部库导入会出现 jpn、kor、
  // deu 这类写法（旧的手写别名表只覆盖了 jpn/kor）。
  cy: { aliases: ["cym", "wel"] },
};

// zh / en / ja 三个基码被上面的变体条目吸收：它们是同一语言的等价写法，不另设条目，
// 否则「同一语言两个条目」会让选择器出现两条一模一样的候选。
const MERGED_BASE_CODES = new Set(["zh", "en", "ja"]);

const enNames = new Intl.DisplayNames(["en"], { type: "language" });
// 界面四语里的中文/日文名：让 zh-CN / ja-JP 界面的用户搜「威尔士」「ウェールズ」也能命中，
// 不必先知道代码或英文名。
const zhNames = new Intl.DisplayNames(["zh-CN"], { type: "language" });
const jaNames = new Intl.DisplayNames(["ja-JP"], { type: "language" });
const nativeName = (code) => {
  if (!Intl.DisplayNames.supportedLocalesOf([code]).length) return "";
  try {
    return new Intl.DisplayNames([code], { type: "language" }).of(code) || "";
  } catch {
    return "";
  }
};

const entries = [];
for (const code of ISO_639_1) {
  if (MERGED_BASE_CODES.has(code)) continue;
  const cur = CURATED[code] || {};
  const en = cur.en || enNames.of(code) || code;
  const nameZh = cur.nameZh || zhNames.of(code) || "";
  const nameJa = cur.nameJa || jaNames.of(code) || "";
  entries.push({
    code,
    native: cur.native || nativeName(code) || en,
    en,
    ...(nameZh && nameZh !== en ? { nameZh } : {}),
    ...(nameJa && nameJa !== en ? { nameJa } : {}),
    ...(cur.aliases && cur.aliases.length ? { aliases: [...cur.aliases] } : {}),
    ...(cur.popular ? { popular: true } : {}),
    ...(cur.regional ? { regional: true } : {}),
  });
}
for (const [code, cur] of Object.entries(CURATED)) {
  if (ISO_639_1.includes(code)) continue;
  entries.push({
    code,
    native: cur.native,
    en: cur.en,
    ...(cur.nameZh ? { nameZh: cur.nameZh } : {}),
    ...(cur.nameJa ? { nameJa: cur.nameJa } : {}),
    ...(cur.aliases && cur.aliases.length ? { aliases: [...cur.aliases] } : {}),
    ...(cur.ui ? { ui: true } : {}),
    ...(cur.popular ? { popular: true } : {}),
    ...(cur.regional ? { regional: true } : {}),
  });
}
for (const code of UI_LOCALE_CODES) {
  const e = entries.find((x) => x.code === code);
  if (e) e.ui = true;
}

// 排序：能当快捷 chip 的（popular）按默认语言秩在前，其余按代码字典序。稳定输出，便于 diff。
const rank = (code) => {
  const i = DEFAULT_TITLE_ORDER.indexOf(code);
  return i < 0 ? DEFAULT_TITLE_ORDER.length : i;
};
entries.sort((a, b) => {
  const pa = a.popular ? 0 : 1;
  const pb = b.popular ? 0 : 1;
  if (pa !== pb) return pa - pb;
  if (pa === 0 && rank(a.code) !== rank(b.code)) return rank(a.code) - rank(b.code);
  return a.code.localeCompare(b.code);
});

// 自检：UI 四语在表内、默认回退链语种在表内、别名不与任何条目代码撞车（同一语言的两种
// 写法只能有一个条目，否则选择器会出现重复候选）。
const byCode = new Map(entries.map((e) => [e.code, e]));
const problems = [];
for (const code of UI_LOCALE_CODES) {
  const e = byCode.get(code);
  if (!e) problems.push("UI 语种 " + code + " 不在语言表内");
  else if (!e.ui) problems.push("UI 语种 " + code + " 缺 ui 标记");
}
for (const code of DEFAULT_TITLE_ORDER) {
  if (!byCode.has(code)) problems.push("默认标题回退链语种 " + code + " 不在语言表内");
}
const aliasOwner = new Map();
for (const e of entries) {
  for (const a of e.aliases || []) {
    if (byCode.has(a)) problems.push("别名 " + a + " 与条目 " + e.code + " 的代码重复");
    if (aliasOwner.has(a)) problems.push("别名 " + a + " 同时属于 " + aliasOwner.get(a) + " 与 " + e.code);
    aliasOwner.set(a, e.code);
  }
  if ((e.aliases || []).includes(e.code)) problems.push("条目 " + e.code + " 把自己列成别名");
}
if (entries.length < 150) problems.push("语言表只有 " + entries.length + " 条，ISO 639-1 全集应有 180+ 条");
// languages.ts 的 UI_LOCALE_CODES 常量必须与这里一致：改一处不改另一处会被这条拦住。
const tsSource = fs.existsSync(MODULE) ? fs.readFileSync(MODULE, "utf8") : "";
const tsUi = /UI_LOCALE_CODES = \[([^\]]*)\]/.exec(tsSource);
if (!tsUi) {
  problems.push("languages.ts 里找不到 UI_LOCALE_CODES 常量");
} else {
  const listed = tsUi[1].split(",").map((s) => s.trim().replace(/^"|"$/g, "")).filter(Boolean);
  if (listed.join("|") !== UI_LOCALE_CODES.join("|")) {
    problems.push("languages.ts 的 UI_LOCALE_CODES=" + listed.join(",") + " 与生成器的 " + UI_LOCALE_CODES.join(",") + " 不一致");
  }
}
if (problems.length) {
  console.error("语言表自检失败：");
  for (const p of problems) console.error("  - " + p);
  process.exit(1);
}

const doc = {
  $comment:
    "语言单一来源（生成物，勿手改）。由 frontend/scripts/gen-languages.ts 生成：" +
    "ISO 639-1 全集 + 常用 BCP-47 变体；名称取 Node/Bun 自带 CLDR（Intl.DisplayNames），" +
    "变体与别名取生成器里的 CURATED 表。重新生成：bun run langs:build。",
  source: "ISO 639-1 code set (Library of Congress) + CLDR language names via Intl.DisplayNames",
  uiLocales: UI_LOCALE_CODES,
  languages: entries,
};
const next = JSON.stringify(doc, null, 2) + "\n";
const prev = fs.existsSync(OUT) ? fs.readFileSync(OUT, "utf8") : "";
if (CHECK) {
  if (prev !== next) {
    console.error("languages.data.json 与生成器输出不一致：请跑 bun run langs:build");
    process.exit(1);
  }
  console.log("语言表一致（" + entries.length + " 条语言，" + aliasOwner.size + " 个别名）");
} else {
  fs.writeFileSync(OUT, next);
  console.log("已写入 " + path.relative(process.cwd(), OUT) + "（" + entries.length + " 条语言，" + aliasOwner.size + " 个别名）");
}
