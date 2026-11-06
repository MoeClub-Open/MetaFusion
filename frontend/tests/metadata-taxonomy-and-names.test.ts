import { test } from "node:test";
import assert from "node:assert/strict";
import { groupTitlesByLocale, pickRecordEntry, pickRecordTitle } from "../src/lib/titles";
import { entityOperation, normalizeEditorEntity } from "../src/lib/catalogCommits";
import { emptyEntity } from "../src/components/catalog/api";
import { matchesEntityQuery } from "../src/lib/entitySearch";
import { shelfExploreParam } from "../src/lib/homeSections";
import { getEntityTagLabels, type DynamicDefinitions } from "../src/lib/definitions";
import { inferCoverRatio } from "../src/lib/cover";

test("pickRecordTitle prefers official translation then base title before nonofficial translation", () => {
  const translations = {
    "zh-CN": { title: "社区译名", nonofficial: true, summary: "中文简介" },
    ja: { title: "原題" },
  };
  assert.equal(
    pickRecordTitle("zh-CN", translations, "原題", { originalLanguage: "ja" }),
    "原題",
  );
  assert.equal(
    pickRecordTitle("zh-CN", { "zh-CN": { title: "社区译名", nonofficial: true } }, "Base JP", {
      originalLanguage: "ja",
    }),
    "Base JP",
  );
  assert.equal(
    pickRecordTitle("zh-CN", { "zh-CN": { title: "社区译名", nonofficial: true } }, ""),
    "社区译名",
  );
  assert.equal(
    pickRecordTitle("zh-CN", { "zh-CN": { title: "官方中文名" }, ja: { title: "原題" } }, "原題", {
      originalLanguage: "ja",
    }),
    "官方中文名",
  );
  const entry = pickRecordEntry("zh-CN", { "zh-CN": { title: "社区译名", nonofficial: true, summary: "中文简介" } }, "原題");
  assert.deepEqual(entry, { title: "原題", body: "中文简介" });
});

test("groupTitlesByLocale preserves nonofficial flags and nonofficialAliases", () => {
  const groups = groupTitlesByLocale(
    {
      "zh-CN": {
        title: "社区主译名",
        nonofficial: true,
        aliases: ["授权曾用名"],
        nonofficial_aliases: ["民间简称", "  "],
      },
      "en-US": {
        title: "",
        nonofficial_aliases: ["Fan Alias"],
      },
    },
    "ja",
  );
  assert.equal(groups.length, 2);
  assert.deepEqual(groups[0], {
    locale: "zh-CN",
    primary: "社区主译名",
    nonofficial: true,
    aliases: ["授权曾用名"],
    nonofficialAliases: ["民间简称"],
    isOriginal: false,
  });
  assert.deepEqual(groups[1], {
    locale: "en-US",
    primary: "",
    nonofficial: false,
    aliases: [],
    nonofficialAliases: ["Fan Alias"],
    isOriginal: false,
  });
});

test("normalizeEditorEntity strips medium_formats projection and keeps nonofficial fields", () => {
  const raw = {
    ...emptyEntity("release"),
    id: "rel-1",
    title: "First Press",
    medium_formats: { cd: 2, blu_ray: 1 },
    translations: {
      "zh-CN": { title: "初回盘", nonofficial: true, aliases: null as unknown as string[], nonofficial_aliases: ["初限盘"] },
    },
  };
  const normalized = normalizeEditorEntity(raw);
  assert.equal("medium_formats" in normalized, false);
  assert.equal(normalized.translations["zh-CN"]?.nonofficial, true);
  assert.deepEqual(normalized.translations["zh-CN"]?.aliases, []);
  assert.deepEqual(normalized.translations["zh-CN"]?.nonofficial_aliases, ["初限盘"]);

  const op = entityOperation(normalized);
  assert.equal("medium_formats" in (op.document as Record<string, unknown>), false);
  assert.ok(matchesEntityQuery(normalized, "初限盘"));
});

test("shelfExploreParam encodes vocab_terms and fields into field/value or field/values", () => {
  assert.equal(
    shelfExploreParam({
      slug: "anime",
      query: { vocab_terms: { creation_form: ["tv_anime", "theater_anime", "ova"] } },
    }),
    "kind=work&field=creation_form&values=tv_anime&values=theater_anime&values=ova",
  );
  assert.equal(
    shelfExploreParam({
      slug: "film-only",
      query: { fields: { creation_form: ["film"] }, tags: ["sci-fi"] },
    }),
    "kind=work&tags=sci-fi&field=creation_form&value=film",
  );
});

test("getEntityTagLabels prepends controlled primary classification and deduplicates free tags", () => {
  const defs: DynamicDefinitions = {
    fields: {
      creation_form: { names: { "zh-CN": "创作形态" }, type: "enum", vocabulary: "creation_form", enabled: true },
      agent_type: { names: { "zh-CN": "主体类型" }, type: "enum", vocabulary: "agent_type", enabled: true },
    },
    vocabularies: {
      creation_form: {
        names: { "zh-CN": "创作形态" },
        terms: {
          original_album: { names: { "zh-CN": "原创专辑", "en-US": "Original album" }, enabled: true },
          light_novel: { names: { "zh-CN": "轻小说", "en-US": "Light novel" }, enabled: true },
        },
      },
      agent_type: {
        names: { "zh-CN": "主体类型" },
        terms: {
          group: { names: { "zh-CN": "团体 / 组合", "en-US": "Group / unit" }, enabled: true },
        },
      },
      tags: {
        names: { "zh-CN": "标签" },
        terms: {
          light_novel: { names: { "zh-CN": "轻小说", "en-US": "Light novel" }, enabled: true },
          fantasy: { names: { "zh-CN": "奇幻", "en-US": "Fantasy" }, enabled: true },
        },
      },
    },
    relations: {},
    templates: {},
    schemes: {},
    structure: {},
  };

  assert.deepEqual(
    getEntityTagLabels(defs, "work", { creation_form: "original_album" }, "zh-CN"),
    ["原创专辑"],
  );
  assert.deepEqual(
    getEntityTagLabels(defs, "work", { creation_form: "light_novel", tags: ["light_novel", "fantasy"] }, "zh-CN"),
    ["轻小说", "奇幻"],
  );
  assert.deepEqual(
    getEntityTagLabels(defs, "agent", { agent_type: "group", tags: ["rock"] }, "en-US"),
    ["Group / unit", "rock"],
  );
});

test("inferCoverRatio matches localized creation_form terms without false substring hits", () => {
  assert.equal(inferCoverRatio(["原创专辑"]), 1);
  assert.equal(inferCoverRatio(["オリジナルアルバム"]), 1);
  assert.equal(inferCoverRatio(["歌曲"]), 1);
  assert.equal(inferCoverRatio(["Song"]), 1);
  assert.equal(inferCoverRatio(["Bonus CD"]), 1);
  assert.equal(inferCoverRatio(["TV动画"]), 2 / 3);
  assert.equal(inferCoverRatio(["TVアニメ"]), 2 / 3);
  assert.equal(inferCoverRatio(["轻小说"]), 3 / 4);
  assert.equal(inferCoverRatio(["poster"]), 2 / 3);
  assert.equal(inferCoverRatio(["ghost"]), 3 / 4);
  assert.equal(inferCoverRatio(["episode"]), 3 / 4);
});
