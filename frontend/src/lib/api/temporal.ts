import { fetchApi } from "./client";
import type { Entity, ExternalLink, Names, Source } from "@/components/catalog/api";

export type CatalogPicture = Entity["pictures"][number];

export interface TimelineScope {
  region?: string;
  platform?: string;
  channel?: string;
  release_id?: string;
}

export interface RelationshipEntitySummary {
  id: string;
  kind: string;
  title: string;
  original_language?: string;
  translations?: Record<
    string,
    { title?: string; nonofficial?: boolean; aliases?: string[]; nonofficial_aliases?: string[] }
  >;
  attributes?: Record<string, any>;
}

export interface TimelineEvent {
  id: string;
  origin: "event_entity" | "embedded_event" | string;
  owner_entity_id: string;
  owner_kind: string;
  event_entity_id?: string;
  event_type: string;
  title: string;
  title_names?: Names;
  occurred_at?: string;
  begin_date?: string;
  end_date?: string;
  ended?: boolean;
  effective_at?: string;
  date_precision?: string;
  earliest_date?: string;
  latest_date?: string;
  scope: TimelineScope;
  target_ids: string[];
  source_refs: Source[];
  related_assets: string[];
  recorded_at: string;
}

export interface TimelineResponse {
  entity_id: string;
  definition_etag: string;
  items: TimelineEvent[];
  entities: Record<string, RelationshipEntitySummary>;
}

export interface TemporalAgentFact {
  agent_id: string;
  title: string;
  relation_id?: string;
  source_origin: string;
  role?: string;
  credit_role?: string;
  region?: string;
  platform?: string;
  channel?: string;
  valid_from?: string;
  valid_to?: string;
  date_precision?: string;
  recorded_at?: string;
}

export interface TemporalPictureSelection {
  status: "confirmed" | "pinned" | "disputed" | "unknown" | string;
  picture?: CatalogPicture;
  owner_id?: string;
  recorded_at?: string;
}

export interface TemporalReleaseView {
  release_id: string;
  title?: string;
  title_status: string;
  disputed_titles?: string[];
  translations?: Entity["translations"];
  attributes?: Record<string, any>;
  attribute_states?: Record<string, string>;
  external_links?: ExternalLink[];
  edition_date?: string;
  region?: string;
  platform?: string;
  distribution_channel?: string;
  channel?: string;
  release_status?: string;
  status_state: string;
  publishers: TemporalAgentFact[];
  publisher_state: string;
  operators: TemporalAgentFact[];
  operator_state: string;
  cover: TemporalPictureSelection;
  app_icon: TemporalPictureSelection;
  unknown_fields: string[];
  disputed_fields?: string[];
  recorded_at?: string;
}

export interface TemporalEntityLink {
  key: string;
  class: string;
  rule_code: string;
  direction: string;
  source_id: string;
  target_id: string;
  relation_id?: string;
  role?: string;
  attributes?: Record<string, any>;
}

export interface EntityAsOfResponse {
  entity_id: string;
  kind: string;
  at: string;
  scope: TimelineScope;
  definition_etag: string;
  title?: string;
  title_status: string;
  disputed_titles?: string[];
  translations?: Entity["translations"];
  attributes: Record<string, any>;
  attribute_states?: Record<string, string>;
  external_links: ExternalLink[];
  publishers: TemporalAgentFact[];
  publisher_state: string;
  operators: TemporalAgentFact[];
  operator_state: string;
  release_status?: string;
  status_state: string;
  cover: TemporalPictureSelection;
  app_icon: TemporalPictureSelection;
  pictures: CatalogPicture[];
  releases?: TemporalReleaseView[];
  active_relations: TemporalEntityLink[];
  unknown_fields: string[];
  disputed_fields?: string[];
  recorded_at: string;
  entities: Record<string, RelationshipEntitySummary>;
}

export interface TemporalFieldDiff {
  field: string;
  release_id?: string;
  scope_label?: string;
  from_value?: any;
  to_value?: any;
  from_status: string;
  to_status: string;
}

export interface TemporalRelationDiff {
  change: "added" | "removed" | "changed" | string;
  rule_code: string;
  source_id: string;
  target_id: string;
  from_link?: TemporalEntityLink;
  to_link?: TemporalEntityLink;
}

export interface EntityTemporalCompareResponse {
  entity_id: string;
  from: string;
  to: string;
  scope: TimelineScope;
  definition_etag: string;
  from_snapshot: EntityAsOfResponse;
  to_snapshot: EntityAsOfResponse;
  field_changes: TemporalFieldDiff[];
  relation_changes: TemporalRelationDiff[];
  events_between: TimelineEvent[];
  entities: Record<string, RelationshipEntitySummary>;
}

export interface ScopedPictureItem {
  owner_id: string;
  owner_kind: string;
  owner_title: string;
  index: number;
  is_current: boolean;
  is_historical: boolean;
  picture: CatalogPicture;
  recorded_at?: string;
}

export interface EntityPicturesResponse {
  entity_id: string;
  definition_etag: string;
  at?: string;
  scope: TimelineScope;
  selected_cover?: TemporalPictureSelection;
  selected_app_icon?: TemporalPictureSelection;
  items: ScopedPictureItem[];
}

function buildQuery(params: Record<string, string | string[] | undefined>): string {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (Array.isArray(v)) {
      for (const item of v) {
        const trimmed = item.trim();
        if (trimmed) qs.append(k, trimmed);
      }
    } else if (typeof v === "string" && v.trim()) {
      qs.set(k, v.trim());
    }
  }
  const str = qs.toString();
  return str ? "?" + str : "";
}

export function fetchEntityTimeline(
  entityId: string,
  params: {
    from?: string;
    to?: string;
    type?: string | string[];
    region?: string;
    platform?: string;
    release_id?: string;
  } = {},
  signal?: AbortSignal,
): Promise<TimelineResponse> {
  return fetchApi<TimelineResponse>(
    "/catalog/entities/" + encodeURIComponent(entityId) + "/timeline" + buildQuery(params),
    { signal },
  );
}

export function fetchEntityAsOf(
  entityId: string,
  params: {
    at: string;
    region?: string;
    platform?: string;
    release_id?: string;
  },
  signal?: AbortSignal,
): Promise<EntityAsOfResponse> {
  return fetchApi<EntityAsOfResponse>(
    "/catalog/entities/" + encodeURIComponent(entityId) + "/as-of" + buildQuery(params),
    { signal },
  );
}

export function fetchEntityTemporalCompare(
  entityId: string,
  params: {
    from: string;
    to: string;
    region?: string;
    platform?: string;
    release_id?: string;
  },
  signal?: AbortSignal,
): Promise<EntityTemporalCompareResponse> {
  return fetchApi<EntityTemporalCompareResponse>(
    "/catalog/entities/" + encodeURIComponent(entityId) + "/compare" + buildQuery(params),
    { signal },
  );
}

export function fetchEntityPictures(
  entityId: string,
  params: {
    role?: string | string[];
    at?: string;
    release_id?: string;
    region?: string;
    platform?: string;
  } = {},
  signal?: AbortSignal,
): Promise<EntityPicturesResponse> {
  return fetchApi<EntityPicturesResponse>(
    "/catalog/entities/" + encodeURIComponent(entityId) + "/pictures" + buildQuery(params),
    { signal },
  );
}
