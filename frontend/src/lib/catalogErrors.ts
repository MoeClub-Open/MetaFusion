// 目录写入错误码 → 四语文案键。
//
// 后端返回的是稳定错误码（translation_required / parent_required …）。直接把码丢给用户等于没解释，
// 所以这里统一映射成人话；未知码才回退原文，避免把没覆盖的码伪装成已解释。
const CODE_KEYS: Record<string, string> = {
  relation_scope_mismatch: "catalog.error.relationScope",
  relation_reference_scope_mismatch: "catalog.error.relationReferenceScope",
  duplicate_relation_position: "catalog.error.relationPosition",
  invalid_relation_scope: "catalog.error.invalidRelationRules",
  invalid_reference_scope: "catalog.error.invalidRelationRules",
  invalid_cycle_group: "catalog.error.invalidRelationRules",
  invalid_relation_usage: "catalog.error.invalidRelationRules",
  edition_group_conflict: "catalog.error.editionGroupConflict",
  invalid_template_block: "catalog.error.invalidTemplateRules",
  invalid_template_match: "catalog.error.invalidTemplateRules",
  translation_required: "catalog.error.translationRequired",
  evidence_required: "catalog.error.evidenceRequired",
  parent_required: "catalog.error.parentRequired",
  version_conflict: "catalog.error.versionConflict",
  immutable_scope: "catalog.error.immutableScope",
  invalid_payload: "catalog.error.invalidPayload",
  unknown_field: "catalog.error.unknownField",
  types_required: "catalog.error.typesRequired",
  invalid_term: "catalog.error.invalidTerm",
  invalid_reference: "catalog.error.invalidReference",
  duplicate_relation: "catalog.error.duplicateRelation",
  duplicate_content: "catalog.error.duplicateContent",
  duplicate_position: "catalog.error.duplicatePosition",
  // 多图写入的拒绝码（见 backend validation.go 的 entity 校验）：URL 撞车、张数封顶、
  // 地址与自身时间格式、以及 asset_id 不是 UUID。编辑器已有行内提示，这里是最后一道。
  duplicate_picture: "catalog.error.duplicatePicture",
  too_many_pictures: "catalog.error.tooManyPictures",
  invalid_picture: "catalog.error.invalidPicture",
  invalid_picture_time: "catalog.error.invalidPictureTime",
  invalid_picture_period: "catalog.error.invalidPicturePeriod",
  invalid_picture_asset: "catalog.error.invalidPictureAsset",
  undeclared_release_subject: "catalog.error.undeclaredSubject",
  invalid_structural_field: "catalog.error.invalidStructuralField",
  use_lifecycle_endpoint: "catalog.error.useLifecycle",
  id_must_be_empty: "catalog.error.idMustBeEmpty",
  // 合并/生命周期两条路径的拒绝码：目标不同归属或非同层级（invalid_merge_target）、
  // 源已删除或已合并、或状态值不在允许集合内（invalid_status）。
  invalid_merge_target: "catalog.error.invalidMergeTarget",
  invalid_status: "catalog.error.invalidStatus",
  // 证据/来源校验：下架与合并都要求 edit_note + 至少一条合法 sources。
  invalid_source: "catalog.error.invalidSource",
  source_not_eligible: "catalog.error.sourceNotEligible",
  rate_limit_unavailable: "catalog.error.rateLimitUnavailable",
  identity_candidates_changed: "catalog.error.identityCandidatesChanged",
  identity_candidates_incomplete: "catalog.error.identityCandidatesIncomplete",
  identity_candidate_unresolved: "catalog.error.identityCandidateUnresolved",
  invalid_identity_review: "catalog.error.invalidIdentityReview",
  invalid_isbn: "catalog.error.invalidIsbn",
  invalid_barcode: "catalog.error.invalidBarcode",
  forbidden: "catalog.error.forbidden",
  authentication_required: "catalog.error.authenticationRequired",
  // 定义/货架/外部库的名称四语齐备是硬约束：缺语种的写入一律被拒。
  four_locale_names_required: "catalog.error.fourLocaleNamesRequired",
  // 用户首页分区偏好的写入校验：slug / 标题 / 排序 / 数量（后台货架编辑器共用同一套码）。
  invalid_slug: "catalog.error.invalidSlug",
  invalid_name: "catalog.error.invalidName",
  invalid_sort: "catalog.error.invalidSort",
  too_many_sections: "catalog.error.tooManySections",
  invalid_types: "catalog.error.invalidTypes",
  invalid_tags: "catalog.error.invalidTags",
  invalid_fields: "catalog.error.invalidFields",
  invalid_vocab_terms: "catalog.error.invalidVocabTerms",
  invalid_relations: "catalog.error.invalidRelations",
  invalid_locale: "catalog.error.invalidLocale",
  // 服务端故障类码：目录服务对未登记的库层/网络错误只回这两个通用码，前端给通用提示。
  database_error: "catalog.error.unknown",
  internal_error: "catalog.error.unknown",
};

/**
 * 按冒号分段取**第一个已知码**的文案键；没有已知码时返回 null（调用方回退原文）。
 *
 * 后端消息有两层：错误码自身可带补充（unknown_field: duration），字段级与场景级校验
 * 还会把条目码包在外层（duration: four_locale_names_required: zh-TW,ja-JP）。所以不能只看
 * 第一段——否则被包裹的码拿不到文案，用户看到的是裸码。从左到右扫描，第一段已知时
 * 结果与只看第一段完全一致。各域的错误码表共用这一份扫描口径。
 */
export function firstKnownCodeKey(
  message: string | null | undefined,
  lookup: (code: string) => string | null | undefined
): string | null {
  if (!message) return null;
  for (const segment of message.trim().split(":")) {
    const key = lookup(segment.trim());
    if (key) return key;
  }
  return null;
}

/** 取错误码对应的文案键；未知码返回 null（调用方回退原文）。 */
export function catalogErrorKey(message: string): string | null {
  return firstKnownCodeKey(message, (code) => CODE_KEYS[code] ?? null);
}

/**
 * 读取类错误码 not_found：GET 实体拿不到时的稳定码。
 * 它不属于写入错误的映射表，各页用自己的"未找到"文案兜底即可，
 * 但绝不能把裸码 `not_found` 直接渲染给用户。
 */
export function isNotFoundError(message?: string | null): boolean {
  if (!message) return false;
  return message.trim().split(":")[0].trim() === "not_found";
}

/**
 * 把后端错误消息本地化：命中码就用人话，**未命中的一律回通用提示**。
 *
 * 未知码不再原样渲染：目录服务的库层/网络/反序列化故障过去会把驱动原文当错误码回给
 * 客户端（报告 #16），原样显示等于把 "dial tcp 10.0.0.5:5432: connect: connection refused"
 * 或反序列化报错摆给用户看。服务端已改为只回稳定码，这一侧同样不把原文当文案。
 */
export function localizeCatalogError(message: string, t: (k: string) => string): string {
  const key = catalogErrorKey(message);
  if (key) {
    const text = t(key);
    if (text && text !== key) return text;
  }
  const fallback = t("catalog.error.unknown");
  // 字典缺键（不该发生）时退回原文：宁可显示得难看，也不编造一句与事实不符的提示。
  return fallback && fallback !== "catalog.error.unknown" ? fallback : message;
}
