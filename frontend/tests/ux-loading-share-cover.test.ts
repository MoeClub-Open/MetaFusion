import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");

function src(rel: string) {
  return readFileSync(join(root, rel), "utf8");
}

test("explore withholds zero totals and pagination while loading", () => {
  const page = src("src/app/explore/page.tsx");
  assert.match(page, /aria-busy="true"/);
  assert.match(page, /animate-pulse/);
  // 底部计数/分页必须等 loading 结束，避免「约 0 项 / 下一页」闪现。
  assert.match(page, /\{!loading && !noResults && \(/);
  assert.doesNotMatch(page, /\{!noResults && \(/);
});

test("entity cards adapt cover aspect within clamped bounds and use NeutralCover on failure", () => {
  const card = src("src/components/common/EntityCard.tsx");
  assert.match(card, /GRID_COVER_ASPECT/);
  assert.match(card, /inferCoverRatio/);
  assert.match(card, /naturalAspect/);
  assert.match(card, /NeutralCover/);
  assert.doesNotMatch(card, /ProceduralCover/);

  const cover = src("src/components/common/EntityCover.tsx");
  assert.match(cover, /NeutralCover/);
  assert.doesNotMatch(cover, /ProceduralCover/);

  const adaptive = src("src/components/common/AdaptiveCardCover.tsx");
  assert.match(adaptive, /clampCoverRatio/);
  assert.match(adaptive, /NeutralCover/);
});

test("work release tables hide zero counts and show skeletons while loading", () => {
  const section = src("src/components/entity/WorkReleasesSection.tsx");
  assert.match(section, /!loadingReleases && !releasesFailed/);
  assert.match(section, /aria-busy="true"/);
  assert.match(section, /animate-pulse/);

  const page = src("src/components/entity/WorkReleasesPage.tsx");
  assert.match(page, /loading \? t\("work\.detail\.loadingReleases"\)/);
  assert.match(page, /aria-busy="true"/);
});

test("entity detail share uses shareContent and surfaces status feedback", () => {
  const view = src("src/components/catalog/EntityDetailView.tsx");
  assert.match(view, /shareContent/);
  assert.match(view, /result === "cancelled"/);
  assert.match(view, /role="status"/);
  assert.match(view, /community\.shareFailed/);
  // 成功文案只在 role=status；按钮保持「分享」，避免与 live region 双重播报。
  assert.match(view, /\{copiedLink && \([\s\S]*?role="status"[\s\S]*?entity\.page\.linkCopied/);
  assert.doesNotMatch(view, /title=\{copiedLink \? t\("entity\.page\.linkCopied"\)/);
  assert.doesNotMatch(view, /aria-live="polite"/);
  assert.doesNotMatch(view, /copiedLink \? <Check/);
});
