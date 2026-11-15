// 本文件由 frontend/scripts/generate-contracts.ts 生成，勿手改。
// 实体骨架（目录库迁移的 catalog.entities.kind 约束与 Go Kinds）。
// 来源：backend/migrations/000046_temporal_events_and_org_relations.up.sql、backend/internal/catalog/types.go
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
  "event",
] as const;

export type EntityKind = (typeof ENTITY_KINDS)[number];
