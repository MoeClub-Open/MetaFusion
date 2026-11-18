package catalog

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TimelineQuery 描述 GET /api/catalog/entities/:id/timeline 的查询过滤参数。
type TimelineQuery struct {
	From      string
	To        string
	Types     []string
	Region    string
	Platform  string
	ReleaseID string
}

func normalizeTimelineEventType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "":
		return "other"
	case "release":
		return "official_release"
	case "update":
		return "version_update"
	case "discontinued":
		return "service_end"
	case "milestone":
		return "major_event"
	default:
		return strings.TrimSpace(raw)
	}
}

func validUUIDOrEmpty(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return ""
	}
	return id.String()
}

func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	v, _ := attrs[key].(string)
	return strings.TrimSpace(v)
}

func attrBool(attrs map[string]any, key string) bool {
	if attrs == nil {
		return false
	}
	v, _ := attrs[key].(bool)
	return v
}

func attrStringSlice(attrs map[string]any, key string) []string {
	if attrs == nil {
		return nil
	}
	raw, ok := attrs[key].([]any)
	if !ok {
		if strs, ok2 := attrs[key].([]string); ok2 {
			out := make([]string, 0, len(strs))
			for _, s := range strs {
				if v := strings.TrimSpace(s); v != "" {
					out = append(out, v)
				}
			}
			return out
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			if v := strings.TrimSpace(s); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func attrNames(attrs map[string]any, key string) Names {
	if attrs == nil {
		return nil
	}
	switch m := attrs[key].(type) {
	case Names:
		out := Names{}
		for k, v := range m {
			if s := strings.TrimSpace(v); s != "" {
				out[k] = s
			}
		}
		return out
	case map[string]any:
		out := Names{}
		for k, raw := range m {
			if s, ok := raw.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out[k] = s
				}
			}
		}
		return out
	}
	return nil
}

func firstLocalizedTitle(n Names) string {
	for _, loc := range []string{"zh-CN", "ja-JP", "ja", "en-US", "zh-TW"} {
		if v := strings.TrimSpace(n[loc]); v != "" {
			return v
		}
	}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(n[k]); v != "" {
			return v
		}
	}
	return ""
}

func uniqueSortedStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueSortedUUIDs(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		if u := validUUIDOrEmpty(raw); u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

func timelineHash(parts ...string) string {
	sum := md5.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func labelsOrEmpty(n Names) Names {
	if n == nil {
		return Names{}
	}
	return n
}

// syncEntityTimelineTx 在实体创建、更新、合并或状态切换时同步其统一时间线索引行。
// 严格遵循原则：仅索引独立 event 实体与显式内嵌事件 attributes.events[]，
// 绝不把 updated_at 或数据库修订时间伪造为现实事件时间。
func syncEntityTimelineTx(ctx context.Context, tx *sql.Tx, e Entity) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog.timeline_events WHERE owner_entity_id = $1::uuid", e.ID); err != nil {
		return err
	}
	if e.State != "active" {
		return nil
	}
	recordedAt := e.UpdatedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}

	if e.Kind == "event" {
		if err := insertStandaloneEventTimelineRowTx(ctx, tx, e, recordedAt); err != nil {
			return err
		}
	}

	rawEvents, _ := e.Attributes["events"].([]any)
	if len(rawEvents) == 0 {
		if e.Kind == "release" {
			return refreshRelatedEventTimelinesTx(ctx, tx, e.ID)
		}
		return nil
	}

	var releaseWorkIDs []string
	if e.Kind == "release" {
		for _, subj := range e.Subjects {
			if wid := validUUIDOrEmpty(subj.WorkID); wid != "" {
				releaseWorkIDs = append(releaseWorkIDs, wid)
			}
		}
	}

	seenDedupe := map[string]bool{}
	linkedEventIDs := []string{}
	for idx, rawItem := range rawEvents {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if linkedID := validUUIDOrEmpty(attrString(item, "event")); linkedID != "" {
			linkedEventIDs = append(linkedEventIDs, linkedID)
			continue
		}
		evType := normalizeTimelineEventType(attrString(item, "event_kind"))
		labels := attrNames(item, "label")
		title := firstLocalizedTitle(labels)
		occurredAt := attrString(item, "date")
		endDate := attrString(item, "end_date")
		ended := attrBool(item, "ended")
		effectiveAt := attrString(item, "effective_at")
		datePrecision := attrString(item, "date_precision")
		earliestDate := attrString(item, "earliest_date")
		latestDate := attrString(item, "latest_date")
		region := attrString(item, "region")
		platform := attrString(item, "platform")
		channel := attrString(item, "channel")
		releaseID := validUUIDOrEmpty(attrString(item, "scoped_release"))
		if e.Kind == "release" && releaseID == "" {
			releaseID = e.ID
		}

		dedupe := timelineHash(
			e.ID, evType, occurredAt, strings.ToLower(title),
			strings.ToLower(region), strings.ToLower(platform), strings.ToLower(channel), releaseID,
		)
		if seenDedupe[dedupe] {
			continue
		}
		seenDedupe[dedupe] = true

		targets := []string{e.ID, releaseID, attrString(item, "store"), attrString(item, "content")}
		targets = append(targets, attrStringSlice(item, "target_ids")...)
		targets = append(targets, releaseWorkIDs...)
		targetUUIDs := uniqueSortedUUIDs(targets)
		assets := uniqueSortedStrings(attrStringSlice(item, "related_assets"))

		sources := []Source{}
		if srcURL := attrString(item, "source_url"); srcURL != "" {
			sources = append(sources, Source{Kind: "url", Citation: srcURL, URL: srcURL})
		}
		sortDate := firstNonEmpty(occurredAt, effectiveAt, earliestDate)
		rowID := fmt.Sprintf("embedded:%s:%d", e.ID, idx+1)
		var relUUID any
		if releaseID != "" {
			relUUID = releaseID
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO catalog.timeline_events ("+
				"id, origin_kind, owner_entity_id, owner_kind, event_entity_id, "+
				"event_type, title, title_names, occurred_at, begin_date, end_date, ended, "+
				"effective_at, date_precision, earliest_date, latest_date, "+
				"region, platform, channel, release_id, "+
				"target_ids, related_assets, source_refs, sort_date, dedupe_key, recorded_at"+
			") VALUES ("+
				"$1, 'embedded_event', $2::uuid, $3, NULL, "+
				"$4, $5, $6::jsonb, $7, $8, $9, $10, "+
				"$11, $12, $13, $14, "+
				"$15, $16, $17, $18::uuid, "+
				"$19::uuid[], $20::text[], $21::jsonb, $22, $23, $24"+
			") ON CONFLICT (id) DO UPDATE SET "+
				"event_type = EXCLUDED.event_type, title = EXCLUDED.title, title_names = EXCLUDED.title_names, "+
				"occurred_at = EXCLUDED.occurred_at, begin_date = EXCLUDED.begin_date, end_date = EXCLUDED.end_date, ended = EXCLUDED.ended, "+
				"effective_at = EXCLUDED.effective_at, date_precision = EXCLUDED.date_precision, earliest_date = EXCLUDED.earliest_date, latest_date = EXCLUDED.latest_date, "+
				"region = EXCLUDED.region, platform = EXCLUDED.platform, channel = EXCLUDED.channel, release_id = EXCLUDED.release_id, "+
				"target_ids = EXCLUDED.target_ids, related_assets = EXCLUDED.related_assets, source_refs = EXCLUDED.source_refs, "+
				"sort_date = EXCLUDED.sort_date, dedupe_key = EXCLUDED.dedupe_key, recorded_at = EXCLUDED.recorded_at",
			rowID, e.ID, e.Kind,
			evType, title, encode(labelsOrEmpty(labels)), occurredAt, occurredAt, endDate, ended,
			effectiveAt, datePrecision, earliestDate, latestDate,
			region, platform, channel, relUUID,
			pq.Array(targetUUIDs), pq.Array(assets), encode(sources), sortDate, dedupe, recordedAt,
		); err != nil {
			return err
		}
	}

	if len(linkedEventIDs) > 0 || e.Kind == "release" {
		checkIDs := append([]string{e.ID}, linkedEventIDs...)
		if err := refreshRelatedEventTimelinesTx(ctx, tx, checkIDs...); err != nil {
			return err
		}
	}
	return nil
}

func insertStandaloneEventTimelineRowTx(ctx context.Context, tx *sql.Tx, e Entity, recordedAt time.Time) error {
	evType := normalizeTimelineEventType(attrString(e.Attributes, "event_type"))
	titleNames := Names{}
	for loc, tr := range e.Translations {
		if t := strings.TrimSpace(tr.Title); t != "" {
			titleNames[loc] = t
		}
	}
	beginDate := attrString(e.Attributes, "begin_date")
	endDate := attrString(e.Attributes, "end_date")
	ended := attrBool(e.Attributes, "ended")
	effectiveAt := attrString(e.Attributes, "effective_at")
	occurredAt := firstNonEmpty(beginDate, effectiveAt)
	datePrecision := attrString(e.Attributes, "date_precision")
	earliestDate := attrString(e.Attributes, "earliest_date")
	latestDate := attrString(e.Attributes, "latest_date")
	region := firstNonEmpty(attrString(e.Attributes, "region"), attrString(e.Attributes, "country"))
	platform := attrString(e.Attributes, "platform")
	channel := attrString(e.Attributes, "channel")
	releaseID := validUUIDOrEmpty(attrString(e.Attributes, "scoped_release"))

	targets := []string{e.ID, releaseID}
	targets = append(targets, attrStringSlice(e.Attributes, "target_ids")...)

	relRows, err := tx.QueryContext(ctx,
		"SELECT DISTINCT u::text FROM ("+
			"SELECT CASE WHEN r.source_id = $1::uuid THEN r.target_id ELSE r.source_id END AS u "+
			"FROM catalog.relations r WHERE r.source_id = $1::uuid OR r.target_id = $1::uuid "+
			"UNION "+
			"SELECT rs.work_id FROM catalog.relations r JOIN catalog.release_subjects rs "+
			"ON rs.release_id = (CASE WHEN r.source_id = $1::uuid THEN r.target_id ELSE r.source_id END) "+
			"WHERE r.source_id = $1::uuid OR r.target_id = $1::uuid "+
			"UNION "+
			"SELECT rs2.work_id FROM catalog.release_subjects rs2 WHERE $2 <> '' AND rs2.release_id = NULLIF($2, '')::uuid "+
			"UNION "+
			"SELECT owner.id FROM catalog.entities owner WHERE owner.state = 'active' "+
			"AND owner.document->'attributes'->'events' @> jsonb_build_array(jsonb_build_object('event', $1::text)) "+
			"UNION "+
			"SELECT rs3.work_id FROM catalog.entities owner JOIN catalog.release_subjects rs3 ON rs3.release_id = owner.id "+
			"WHERE owner.state = 'active' AND owner.kind = 'release' "+
			"AND owner.document->'attributes'->'events' @> jsonb_build_array(jsonb_build_object('event', $1::text))"+
		") raw_u WHERE u IS NOT NULL", e.ID, releaseID)
	if err != nil {
		return err
	}
	for relRows.Next() {
		var uid string
		if err := relRows.Scan(&uid); err != nil {
			relRows.Close()
			return err
		}
		targets = append(targets, uid)
	}
	if err := relRows.Err(); err != nil {
		relRows.Close()
		return err
	}
	relRows.Close()

	targetUUIDs := uniqueSortedUUIDs(targets)
	assets := attrStringSlice(e.Attributes, "related_assets")
	sources := []Source{}
	seenSrc := map[string]bool{}
	for _, p := range e.Pictures {
		if aid := strings.TrimSpace(p.AssetID); aid != "" {
			assets = append(assets, aid)
		}
		if p.Source.Kind != "" && p.Source.Citation != "" {
			k := p.Source.Kind + "|" + p.Source.Citation + "|" + p.Source.URL
			if !seenSrc[k] {
				seenSrc[k] = true
				sources = append(sources, p.Source)
			}
		}
	}
	for _, l := range e.ExternalLinks {
		if strings.TrimSpace(l.URL) != "" && validURL(l.URL) {
			k := "url|" + l.URL
			if !seenSrc[k] {
				seenSrc[k] = true
				sources = append(sources, Source{Kind: "url", Citation: l.URL, URL: l.URL})
			}
		}
	}
	assets = uniqueSortedStrings(assets)
	sortDate := firstNonEmpty(beginDate, effectiveAt, earliestDate)
	var relUUID any
	if releaseID != "" {
		relUUID = releaseID
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO catalog.timeline_events ("+
			"id, origin_kind, owner_entity_id, owner_kind, event_entity_id, "+
			"event_type, title, title_names, occurred_at, begin_date, end_date, ended, "+
			"effective_at, date_precision, earliest_date, latest_date, "+
			"region, platform, channel, release_id, "+
			"target_ids, related_assets, source_refs, sort_date, dedupe_key, recorded_at"+
		") VALUES ("+
			"$1, 'event_entity', $2::uuid, 'event', $2::uuid, "+
			"$3, $4, $5::jsonb, $6, $7, $8, $9, "+
			"$10, $11, $12, $13, "+
			"$14, $15, $16, $17::uuid, "+
			"$18::uuid[], $19::text[], $20::jsonb, $21, $22, $23"+
		") ON CONFLICT (id) DO UPDATE SET "+
			"event_type = EXCLUDED.event_type, title = EXCLUDED.title, title_names = EXCLUDED.title_names, "+
			"occurred_at = EXCLUDED.occurred_at, begin_date = EXCLUDED.begin_date, end_date = EXCLUDED.end_date, ended = EXCLUDED.ended, "+
			"effective_at = EXCLUDED.effective_at, date_precision = EXCLUDED.date_precision, earliest_date = EXCLUDED.earliest_date, latest_date = EXCLUDED.latest_date, "+
			"region = EXCLUDED.region, platform = EXCLUDED.platform, channel = EXCLUDED.channel, release_id = EXCLUDED.release_id, "+
			"target_ids = EXCLUDED.target_ids, related_assets = EXCLUDED.related_assets, source_refs = EXCLUDED.source_refs, "+
			"sort_date = EXCLUDED.sort_date, dedupe_key = EXCLUDED.dedupe_key, recorded_at = EXCLUDED.recorded_at",
		"event:"+e.ID, e.ID,
		evType, e.Title, encode(titleNames), occurredAt, beginDate, endDate, ended,
		effectiveAt, datePrecision, earliestDate, latestDate,
		region, platform, channel, relUUID,
		pq.Array(targetUUIDs), pq.Array(assets), encode(sources), sortDate, "event_entity:"+e.ID, recordedAt,
	)
	return err
}

// refreshRelatedEventTimelinesTx 刷新与指定实体集相关的独立 event 实体的 target_ids。
func refreshRelatedEventTimelinesTx(ctx context.Context, tx *sql.Tx, entityIDs ...string) error {
	seedUUIDs := uniqueSortedUUIDs(entityIDs)
	if len(seedUUIDs) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT DISTINCT ev.id::text FROM catalog.entities ev "+
			"WHERE ev.kind = 'event' AND ev.state = 'active' AND ("+
			"ev.id = ANY($1::uuid[]) "+
			"OR EXISTS (SELECT 1 FROM catalog.relations r WHERE (r.source_id = ev.id AND r.target_id = ANY($1::uuid[])) OR (r.target_id = ev.id AND r.source_id = ANY($1::uuid[]))) "+
			"OR EXISTS (SELECT 1 FROM catalog.timeline_events te WHERE te.event_entity_id = ev.id AND te.target_ids && $1::uuid[])"+
			")", pq.Array(seedUUIDs))
	if err != nil {
		return err
	}
	eventIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		eventIDs = append(eventIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, evID := range eventIDs {
		ev, err := get(ctx, tx, evID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if ev.State != "active" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM catalog.timeline_events WHERE owner_entity_id = $1::uuid", ev.ID); err != nil {
				return err
			}
			continue
		}
		if err := insertStandaloneEventTimelineRowTx(ctx, tx, ev, ev.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

// EntityTimeline 查询某个可见实体的统一事件时间线（合并独立 Event 与内嵌事件并去重）。
func (s *Store) EntityTimeline(ctx context.Context, id string, q TimelineQuery, u *User) (TimelineResponse, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return TimelineResponse{}, fmt.Errorf("invalid_id")
	}
	fromDate := strings.TrimSpace(q.From)
	toDate := strings.TrimSpace(q.To)
	if fromDate != "" && !validPartialISODate(fromDate) {
		return TimelineResponse{}, fmt.Errorf("invalid_date")
	}
	if toDate != "" && !validPartialISODate(toDate) {
		return TimelineResponse{}, fmt.Errorf("invalid_date")
	}
	if fromDate != "" && toDate != "" && pictureTimeStart(fromDate).After(pictureTimeEnd(toDate)) {
		return TimelineResponse{}, fmt.Errorf("invalid_date_range")
	}
	relFilter := strings.TrimSpace(q.ReleaseID)
	if relFilter != "" {
		if _, err := uuid.Parse(relFilter); err != nil {
			return TimelineResponse{}, fmt.Errorf("invalid_id")
		}
	}
	typeSet := map[string]bool{}
	for _, raw := range q.Types {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				typeSet[p] = true
				typeSet[normalizeTimelineEventType(p)] = true
			}
		}
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return TimelineResponse{}, err
	}
	defer tx.Rollback()

	res, err := s.entityTimelineTx(ctx, tx, uid.String(), q, typeSet, u)
	if err != nil {
		return TimelineResponse{}, err
	}
	return res, tx.Commit()
}

func (s *Store) entityTimelineTx(ctx context.Context, tx *sql.Tx, entityID string, q TimelineQuery, typeSet map[string]bool, u *User) (TimelineResponse, error) {
	defs, err := definitions(ctx, tx)
	if err != nil {
		return TimelineResponse{}, err
	}
	subjectMap, err := getManyFrom(ctx, tx, []string{entityID}, u)
	if err != nil {
		return TimelineResponse{}, err
	}
	subject, ok := subjectMap[entityID]
	if !ok || subject.State != "active" {
		return TimelineResponse{}, sql.ErrNoRows
	}

	lookupIDs := []string{subject.ID}
	if subject.Kind == "work" {
		relIDs, err := subjectReleaseIDsTx(ctx, tx, subject.ID)
		if err != nil {
			return TimelineResponse{}, err
		}
		visRels, err := getManyFrom(ctx, tx, relIDs, u)
		if err != nil {
			return TimelineResponse{}, err
		}
		for rid, rel := range visRels {
			if rel.State != "active" {
				continue
			}
			if q.ReleaseID != "" && rid != strings.TrimSpace(q.ReleaseID) {
				continue
			}
			lookupIDs = append(lookupIDs, rid)
		}
	}
	lookupIDs = uniqueSortedUUIDs(lookupIDs)

	rows, err := tx.QueryContext(ctx,
		"SELECT id, origin_kind, owner_entity_id::text, owner_kind, coalesce(event_entity_id::text, ''), "+
			"event_type, title, title_names, occurred_at, begin_date, end_date, ended, "+
			"effective_at, date_precision, earliest_date, latest_date, "+
			"region, platform, channel, coalesce(release_id::text, ''), "+
			"target_ids::text[], related_assets, source_refs, sort_date, recorded_at "+
			"FROM catalog.timeline_events "+
			"WHERE owner_entity_id = ANY($1::uuid[]) OR target_ids && $1::uuid[] "+
			"ORDER BY (sort_date = '') ASC, sort_date ASC, id ASC", pq.Array(lookupIDs))
	if err != nil {
		return TimelineResponse{}, err
	}
	type rawRow struct {
		ev       TimelineEvent
		sortDate string
	}
	rawItems := []rawRow{}
	refEntityIDs := []string{subject.ID}
	for rows.Next() {
		var item TimelineEvent
		var titleNamesRaw, sourcesRaw []byte
		var targetIDs, relatedAssets []string
		var sortDate string
		var region, platform, channel, releaseID string
		if err := rows.Scan(
			&item.ID, &item.Origin, &item.OwnerEntityID, &item.OwnerKind, &item.EventEntityID,
			&item.EventType, &item.Title, &titleNamesRaw, &item.OccurredAt, &item.BeginDate, &item.EndDate, &item.Ended,
			&item.EffectiveAt, &item.DatePrecision, &item.EarliestDate, &item.LatestDate,
			&region, &platform, &channel, &releaseID,
			pq.Array(&targetIDs), pq.Array(&relatedAssets), &sourcesRaw, &sortDate, &item.RecordedAt,
		); err != nil {
			rows.Close()
			return TimelineResponse{}, err
		}
		_ = json.Unmarshal(titleNamesRaw, &item.TitleNames)
		_ = json.Unmarshal(sourcesRaw, &item.SourceRefs)
		if item.SourceRefs == nil {
			item.SourceRefs = []Source{}
		}
		item.Scope = TimelineScope{
			Region:    region,
			Platform:  platform,
			Channel:   channel,
			ReleaseID: releaseID,
		}
		item.TargetIDs = uniqueSortedUUIDs(targetIDs)
		item.RelatedAssets = uniqueSortedStrings(relatedAssets)
		if item.RelatedAssets == nil {
			item.RelatedAssets = []string{}
		}
		refEntityIDs = append(refEntityIDs, item.OwnerEntityID)
		if item.EventEntityID != "" {
			refEntityIDs = append(refEntityIDs, item.EventEntityID)
		}
		if item.Scope.ReleaseID != "" {
			refEntityIDs = append(refEntityIDs, item.Scope.ReleaseID)
		}
		refEntityIDs = append(refEntityIDs, item.TargetIDs...)
		rawItems = append(rawItems, rawRow{ev: item, sortDate: sortDate})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TimelineResponse{}, err
	}
	rows.Close()

	visibleSummaries, err := relationshipEntitiesFrom(ctx, tx, uniqueSortedUUIDs(refEntityIDs), u)
	if err != nil {
		return TimelineResponse{}, err
	}
	relCandidates := []string{}
	for _, r := range rawItems {
		if r.ev.Scope.ReleaseID != "" {
			relCandidates = append(relCandidates, r.ev.Scope.ReleaseID)
		}
	}
	relEntities, err := getManyFrom(ctx, tx, uniqueSortedUUIDs(relCandidates), u)
	if err != nil {
		return TimelineResponse{}, err
	}

	fromDate := strings.TrimSpace(q.From)
	toDate := strings.TrimSpace(q.To)
	regionFilter := strings.TrimSpace(q.Region)
	platformFilter := strings.TrimSpace(q.Platform)
	releaseFilter := strings.TrimSpace(q.ReleaseID)

	filtered := []TimelineEvent{}
	seenCrossEntityFact := map[string]int{}
	usedEntityIDs := map[string]bool{subject.ID: true}

	for _, row := range rawItems {
		ev := row.ev
		if _, ok := visibleSummaries[ev.OwnerEntityID]; !ok {
			continue
		}
		if ev.EventEntityID != "" {
			if _, ok := visibleSummaries[ev.EventEntityID]; !ok {
				continue
			}
		}
		if ev.Scope.ReleaseID != "" {
			if rel, ok := relEntities[ev.Scope.ReleaseID]; ok {
				if ev.Scope.Region == "" {
					ev.Scope.Region = attrString(rel.Attributes, "country")
				}
				if ev.Scope.Platform == "" {
					ev.Scope.Platform = attrString(rel.Attributes, "platform")
				}
				if ev.Scope.Channel == "" {
					ev.Scope.Channel = firstNonEmpty(attrString(rel.Attributes, "channel"), attrString(rel.Attributes, "distribution_channel"))
				}
			}
		}
		if len(typeSet) > 0 && !typeSet[ev.EventType] {
			continue
		}
		if releaseFilter != "" && ev.Scope.ReleaseID != releaseFilter && ev.OwnerEntityID != releaseFilter && !contains(ev.TargetIDs, releaseFilter) {
			continue
		}
		if regionFilter != "" && !matchesTokenScope(ev.Scope.Region, regionFilter) {
			continue
		}
		if platformFilter != "" && !matchesTokenScope(ev.Scope.Platform, platformFilter) {
			continue
		}
		if fromDate != "" || toDate != "" {
			if !timelineEventOverlapsRange(ev, fromDate, toDate) {
				continue
			}
		}
		visTargets := make([]string, 0, len(ev.TargetIDs))
		for _, tid := range ev.TargetIDs {
			if _, ok := visibleSummaries[tid]; ok {
				visTargets = append(visTargets, tid)
			}
		}
		ev.TargetIDs = visTargets

		crossKey := ""
		if ev.Origin == "event_entity" {
			crossKey = "entity:" + ev.EventEntityID
		} else if ev.OccurredAt != "" && ev.Title != "" {
			crossKey = "fact:" + ev.EventType + "|" + ev.OccurredAt + "|" + strings.ToLower(ev.Title) + "|" + strings.ToLower(ev.Scope.Region) + "|" + strings.ToLower(ev.Scope.Platform)
		}
		if crossKey != "" {
			if prevIdx, dup := seenCrossEntityFact[crossKey]; dup {
				prev := filtered[prevIdx]
				if prev.Origin != "event_entity" && (ev.Origin == "event_entity" || (prev.Scope.ReleaseID == "" && ev.Scope.ReleaseID != "")) {
					filtered[prevIdx] = ev
				}
				continue
			}
			seenCrossEntityFact[crossKey] = len(filtered)
		}
		filtered = append(filtered, ev)
	}

	for _, ev := range filtered {
		usedEntityIDs[ev.OwnerEntityID] = true
		if ev.EventEntityID != "" {
			usedEntityIDs[ev.EventEntityID] = true
		}
		if ev.Scope.ReleaseID != "" {
			usedEntityIDs[ev.Scope.ReleaseID] = true
		}
		for _, tid := range ev.TargetIDs {
			usedEntityIDs[tid] = true
		}
	}
	outEntities := map[string]RelationshipEntity{}
	for eid := range usedEntityIDs {
		if summary, ok := visibleSummaries[eid]; ok {
			outEntities[eid] = summary
		}
	}
	return TimelineResponse{
		EntityID:       subject.ID,
		DefinitionETag: defs.ETag,
		Items:          filtered,
		Entities:       outEntities,
	}, nil
}

func timelineEventOverlapsRange(ev TimelineEvent, fromDate, toDate string) bool {
	startStr := firstNonEmpty(ev.BeginDate, ev.OccurredAt, ev.EffectiveAt, ev.EarliestDate)
	endStr := firstNonEmpty(ev.EndDate, ev.LatestDate, startStr)
	if startStr == "" {
		return false
	}
	evStart := pictureTimeStart(startStr)
	evEnd := pictureTimeEnd(endStr)
	if fromDate != "" && evEnd.Before(pictureTimeStart(fromDate)) {
		return false
	}
	if toDate != "" && evStart.After(pictureTimeEnd(toDate)) {
		return false
	}
	return true
}
