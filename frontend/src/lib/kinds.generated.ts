// 本文件由 frontend/scripts/generate-contracts.ts 生成，勿手改。
// 实体骨架八元组（目录库基线的 catalog.entities.kind 约束）。
// 来源：backend/migrations/000021_catalog_baseline.up.sql
// 校验：cd frontend && bun scripts/generate-contracts.ts --check

export const ENTITY_KINDS = [
  "agent",
  "collection",
  "work",
  "content_unit",
  "expression",
  "release",
  "medium",
  "track",
] as const;

export type EntityKind = (typeof ENTITY_KINDS)[number];
