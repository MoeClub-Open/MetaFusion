"use client";

import { useI18n } from "@/i18n/I18nProvider";
import type { CoverOrigin, CoverBearing } from "@/lib/cover";
import { pickRecordTitle } from "@/lib/titles";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";

const ORIGIN_KEYS: Partial<Record<CoverOrigin, string>> = {
  release: "catalog.coverSourceRelease",
  medium: "catalog.coverSourceMedium",
  work: "catalog.coverSourceWork",
  subject_work: "catalog.coverSourceSubjectWork",
  mother_work: "catalog.coverSourceMotherWork",
};

/**
 * 封面借用声明：`resolveCover` 的 origin 不是 self 时，必须把"这张图来自哪一条"写明。
 *
 * 目录数据上出现过把借来的图当本实体自己的封面来标注的情况（所属发行的通用美术被写成
 * "该曲官方封面"、官网首页横幅被写成"官方主视觉"）。展示侧兜底是合理的，
 * 不写明来源才是错的，所以这里渲染的是事实陈述而不是装饰。
 */
export function CoverOriginNote({
  origin,
  name,
  from,
  className = "",
}: {
  origin: CoverOrigin;
  name?: string;
  from?: CoverBearing;
  className?: string;
}) {
  const { t, locale } = useI18n();
  const order = useTitleDisplayOrder();
  const key = ORIGIN_KEYS[origin];
  if (!key) return null;
  const source = t(key);
  const trimmed = String(name || (from ? pickRecordTitle(locale, from.translations, from.title || "", { order, originalLanguage: from.original_language }) : "")).trim();
  return (
    <p className={`font-mono text-[10px] leading-snug text-text-faint ${className}`}>
      {trimmed ? t("catalog.coverDerivedNamed", { source, name: trimmed }) : t("catalog.coverDerived", { source })}
    </p>
  );
}
