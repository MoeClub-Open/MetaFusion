import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { readerVisibleProse } from "../src/lib/readerNotes";

const root = join(import.meta.dirname, "..");

function src(rel: string) {
  return readFileSync(join(root, rel), "utf8");
}

test("NeutralCover drops unused React default import (TS6133)", () => {
  const cover = src("src/components/common/NeutralCover.tsx");
  assert.match(cover, /from "lucide-react"/);
  assert.doesNotMatch(cover, /import React from ["']react["']/);
  assert.doesNotMatch(cover, /import \* as React from ["']react["']/);
});

test("entity SEO metadata runs summary through readerVisibleProse before meta/OG/twitter", () => {
  const seo = src("src/lib/seo.ts");
  assert.match(seo, /import \{ readerVisibleProse \} from ["']\.\/readerNotes["']/);
  assert.match(seo, /const cleanedBody = readerVisibleProse\(body\)/);
  assert.match(seo, /clamp\(cleanedBody \|\| structuredDescription/);
  // build() 把同一 description 写入 description / openGraph / twitter，勿另开 SEO 例外路径。
  assert.match(seo, /openGraph:[\s\S]*description,/);
  assert.match(seo, /twitter:[\s\S]*description,/);
});

test("readerVisibleProse strips edition_type= from 進撃-style meta prose", () => {
  const raw =
    "Bangumi 图书条目 8492「進撃の巨人 (1)」的单行纸质版次：出版社 講談社、发售日 2010-03-17、ISBN 9784063842760、192 頁、￥440 円。条目未载限定版/特典/BOX 等版次字样，故 edition_type=standard、流通为实体纸本（physical）、发售地区 JP。";
  const cleaned = readerVisibleProse(raw);
  assert.doesNotMatch(cleaned, /edition_type/);
  assert.doesNotMatch(cleaned, /\bphysical\b/);
  assert.match(cleaned, /講談社/);
});
