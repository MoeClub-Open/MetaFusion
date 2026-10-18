package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Contributions project only changes that reached the public catalog. Owning a
// private workspace, or being able to review it, never makes its activity public.
// Identity snapshots belong to catalog; this projection reads no auth tables.
type ContributionActor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ContributionEntity struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

type ContributionItem struct {
	ID            string               `json:"id"`
	CommitID      string               `json:"commit_id,omitempty"`
	Message       string               `json:"message"`
	AuthoredAt    time.Time            `json:"authored_at"`
	MergedAt      time.Time            `json:"merged_at"`
	Author        ContributionActor    `json:"author"`
	Authors       []ContributionActor  `json:"authors"`
	MergedBy      *ContributionActor   `json:"merged_by,omitempty"`
	PullRequestID string               `json:"pull_request_id,omitempty"`
	ChangesCount  int64                `json:"changes_count"`
	Entities      []ContributionEntity `json:"entities"`
	Legacy        bool                 `json:"legacy"`
}

type UserContributionStats struct {
	MergedCommits      int64 `json:"merged_commits"`
	ChangedEntities    int64 `json:"changed_entities"`
	CreatedEntities    int64 `json:"created_entities"`
	MergedPullRequests int64 `json:"merged_pull_requests"`
	LegacyRevisions    int64 `json:"legacy_revisions"`
}

type ContributionDay struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type UserContributions struct {
	Year     int                   `json:"year"`
	Timezone string                `json:"timezone"`
	Summary  UserContributionStats `json:"summary"`
	Calendar []ContributionDay     `json:"calendar"`
	Items    []ContributionItem    `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func contributionPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

func contributionOffset(page, pageSize int) int64 {
	const maxOffset = int64(1<<63 - 1)
	pages := int64(page - 1)
	if pages > maxOffset/int64(pageSize) {
		return maxOffset
	}
	return pages * int64(pageSize)
}

func contributionYear(year int) (int, error) {
	if year == 0 {
		return time.Now().UTC().Year(), nil
	}
	if year < 1970 || year > 9999 {
		return 0, fmt.Errorf("invalid_year")
	}
	return year, nil
}

func contributionCalendar(year int, counts map[string]int64) []ContributionDay {
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	days := make([]ContributionDay, 0, 366)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		days = append(days, ContributionDay{Date: date, Count: counts[date]})
	}
	return days
}

// The page, totals and heat map share one database snapshot. There is no viewer
// argument: a profile's public contribution counts never depend on permissions.
func (s *Store) UserContributions(ctx context.Context, userID string, year, page, pageSize int) (UserContributions, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return UserContributions{}, sql.ErrNoRows
	}
	userID = id.String()
	year, err = contributionYear(year)
	if err != nil {
		return UserContributions{}, err
	}
	page, pageSize = contributionPage(page, pageSize)
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	var summary, days, items []byte
	out := UserContributions{Year: year, Timezone: "UTC", Page: page, PageSize: pageSize}
	err = s.DB.QueryRowContext(ctx, userContributionsQuery, userID, start, start.AddDate(1, 0, 0), pageSize, contributionOffset(page, pageSize)).Scan(&summary, &days, &items, &out.Total)
	if err != nil {
		return UserContributions{}, err
	}
	if err = json.Unmarshal(summary, &out.Summary); err != nil {
		return UserContributions{}, err
	}
	counts := map[string]int64{}
	if err = json.Unmarshal(days, &counts); err != nil {
		return UserContributions{}, err
	}
	if err = json.Unmarshal(items, &out.Items); err != nil {
		return UserContributions{}, err
	}
	out.Calendar = contributionCalendar(year, counts)
	return out, nil
}

// Object counts describe changes; calendar counts describe distinct commits.
// Relation edits include both visible endpoints. An associated authoritative
// revision is required, so local no-ops cannot manufacture catalog contributions.
// Partly hidden commits withhold their message and detail links, which may identify a
// hidden object. Historical direct revisions stay revisions, not invented commits.
// Legacy candidates deliberately retain no snapshots: this CTE is reused, and a
// prolific editor's original documents can otherwise spill to disk. Canonical
// UUID guards preserve the old exact text matches without casting invalid IDs.
const userContributionsQuery = `
WITH candidate_commits AS (
    SELECT c.id,c.actor_id,c.author_snapshot,c.message,c.created_at,c.merged_at,c.merged_by,c.pull_request_id
    FROM catalog.commits c
    WHERE EXISTS (SELECT 1 FROM catalog.commit_authors a WHERE a.commit_id=c.id AND a.user_id=$1)
      AND c.merged_at >= $2 AND c.merged_at < $3
), all_objects AS (
    SELECT o.*, COALESCE(o.after_document,o.before_document) AS document
    FROM catalog.commit_objects o JOIN candidate_commits c ON c.id=o.commit_id
), object_visibility AS (
    SELECT o.*,CASE WHEN o.target='entity' THEN
        EXISTS (SELECT 1 FROM catalog.entities e WHERE e.id=o.target_id AND e.state='active')
        ELSE EXISTS (SELECT 1 FROM catalog.entities s JOIN catalog.entities t
                     ON t.id::text=o.document->>'target_id' AND t.state='active'
                     WHERE s.id::text=o.document->>'source_id' AND s.state='active')
        END AS public
    FROM all_objects o
), changed_objects AS (
    SELECT o.* FROM object_visibility o
    WHERE o.before_document IS DISTINCT FROM o.after_document
      AND EXISTS (SELECT 1 FROM catalog.revision_commits rc
                   JOIN catalog.revisions r ON r.id=rc.revision_id
                   WHERE rc.commit_id=o.commit_id AND r.target_id=o.target_id::text)
), visible_commit_objects AS (
    SELECT o.commit_id,o.target,o.target_id,o.base_version,
           o.before_document,o.after_document,
           jsonb_build_array(jsonb_build_object('id',e.id,'kind',e.kind,'title',e.title)) AS entities
    FROM changed_objects o JOIN catalog.entities e ON e.id=o.target_id AND e.state='active'
    WHERE o.target='entity'
    UNION ALL
    SELECT o.commit_id,o.target,o.target_id,o.base_version,
           o.before_document,o.after_document,
           jsonb_build_array(jsonb_build_object('id',s.id,'kind',s.kind,'title',s.title),
                             jsonb_build_object('id',t.id,'kind',t.kind,'title',t.title))
    FROM changed_objects o
    JOIN catalog.entities s ON s.id::text=o.document->>'source_id' AND s.state='active'
    JOIN catalog.entities t ON t.id::text=o.document->>'target_id' AND t.state='active'
    WHERE o.target='relation'
), commit_counts AS (
    SELECT c.id,count(v.target_id) AS visible_count,
           (SELECT bool_and(o.public) FROM object_visibility o WHERE o.commit_id=c.id) AS all_public
    FROM candidate_commits c JOIN visible_commit_objects v ON v.commit_id=c.id
    GROUP BY c.id
), legacy_candidates AS (
    SELECT r.id,r.actor_id,r.actor_name,r.edit_note,r.created_at,r.version=1 AS entity_created,
           e.id AS entity_id,e.kind AS entity_kind,e.title AS entity_title,e.state AS entity_state,
           CASE WHEN e.id IS NULL AND r.snapshot->>'source_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (r.snapshot->>'source_id')::uuid END AS source_id,
           CASE WHEN e.id IS NULL AND r.snapshot->>'target_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN (r.snapshot->>'target_id')::uuid END AS relation_target_id
    FROM catalog.revisions r
    LEFT JOIN catalog.entities e ON e.id=CASE
        WHEN r.target_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN r.target_id::uuid END
    WHERE r.actor_id=$1::uuid AND r.created_at >= $2 AND r.created_at < $3
      AND r.commit_id IS NULL
      AND NOT EXISTS (SELECT 1 FROM catalog.revision_commits rc WHERE rc.revision_id=r.id)
), visible_legacy AS (
    SELECT r.id,r.actor_id,r.actor_name,r.edit_note,r.created_at,
           jsonb_build_array(jsonb_build_object('id',r.entity_id,'kind',r.entity_kind,'title',r.entity_title)) AS entities,
           r.entity_created
    FROM legacy_candidates r WHERE r.entity_state='active'
    UNION ALL
    SELECT r.id,r.actor_id,r.actor_name,r.edit_note,r.created_at,
           jsonb_build_array(jsonb_build_object('id',s.id,'kind',s.kind,'title',s.title),
                                 jsonb_build_object('id',t.id,'kind',t.kind,'title',t.title)),false
    FROM legacy_candidates r
    JOIN catalog.entities s ON s.id=r.source_id AND s.state='active'
    JOIN catalog.entities t ON t.id=r.relation_target_id AND t.state='active'
    WHERE r.entity_id IS NULL
), public_pull_requests AS (
    SELECT p.id FROM catalog.pull_requests p
    WHERE p.status='merged' AND p.id IN (SELECT c.pull_request_id FROM candidate_commits c)
      AND NOT EXISTS (
          SELECT 1 FROM catalog.commits pc JOIN catalog.commit_objects o ON o.commit_id=pc.id
          WHERE pc.pull_request_id=p.id AND NOT (CASE WHEN o.target='entity' THEN
              EXISTS (SELECT 1 FROM catalog.entities e WHERE e.id=o.target_id AND e.state='active')
              ELSE EXISTS (
                  SELECT 1 FROM catalog.entities s JOIN catalog.entities t
                    ON t.id::text=COALESCE(o.after_document,o.before_document)->>'target_id' AND t.state='active'
                  WHERE s.id::text=COALESCE(o.after_document,o.before_document)->>'source_id' AND s.state='active')
              END))
), activity AS (
    SELECT c.id::text AS id,CASE WHEN n.all_public THEN c.id::text END AS commit_id,
           CASE WHEN n.all_public THEN c.message ELSE '' END AS message,
           c.created_at AS authored_at,c.merged_at,
           c.actor_id AS author_id,COALESCE(NULLIF(c.author_snapshot->>'username',''),c.actor_id) AS author_name,
           (SELECT jsonb_agg(jsonb_build_object('id',a.user_id,'name',COALESCE(NULLIF(a.name,''),a.user_id)) ORDER BY a.user_id)
            FROM catalog.commit_authors a WHERE a.commit_id=c.id) AS authors,
           NULLIF(c.merged_by,'') AS merged_by,
           CASE WHEN n.all_public THEN p.id::text END AS pull_request_id,
           n.visible_count AS changes_count,false AS legacy
    FROM candidate_commits c JOIN commit_counts n ON n.id=c.id
    LEFT JOIN public_pull_requests p ON p.id=c.pull_request_id
    UNION ALL
    SELECT 'revision:'||r.id::text,NULL,r.edit_note,r.created_at,r.created_at,
           r.actor_id::text,COALESCE(NULLIF(r.actor_name,''),r.actor_id::text,'system'),
           jsonb_build_array(jsonb_build_object('id',r.actor_id,'name',COALESCE(NULLIF(r.actor_name,''),r.actor_id::text,'system'))),
           NULL,NULL,1,true
    FROM visible_legacy r
), activity_entities AS (
    SELECT DISTINCT o.commit_id::text AS activity_id,x->>'id' AS id,x->>'kind' AS kind,x->>'title' AS title,
           o.target='entity' AND o.base_version=0 AND o.before_document IS NULL AS created
    FROM visible_commit_objects o CROSS JOIN LATERAL jsonb_array_elements(o.entities) x
    UNION ALL
    SELECT 'revision:'||r.id::text,x->>'id',x->>'kind',x->>'title',r.entity_created
    FROM visible_legacy r CROSS JOIN LATERAL jsonb_array_elements(r.entities) x
), daily AS (
    SELECT to_char(merged_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS day,count(*) AS count
    FROM activity WHERE NOT legacy GROUP BY day
), paged AS (
    SELECT * FROM activity ORDER BY merged_at DESC,id DESC LIMIT $4 OFFSET $5
)
SELECT
    jsonb_build_object(
        'merged_commits',(SELECT count(*) FROM activity WHERE NOT legacy),
        'changed_entities',(SELECT count(DISTINCT e.id) FROM activity_entities e JOIN activity a ON a.id=e.activity_id WHERE NOT a.legacy),
        'created_entities',(SELECT count(DISTINCT e.id) FROM activity_entities e JOIN activity a ON a.id=e.activity_id WHERE NOT a.legacy AND e.created),
        'merged_pull_requests',(SELECT count(DISTINCT pull_request_id) FROM activity),
        'legacy_revisions',(SELECT count(*) FROM activity WHERE legacy)),
    COALESCE((SELECT jsonb_object_agg(day,count) FROM daily),'{}'::jsonb),
    COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
        'id',a.id,'commit_id',a.commit_id,'message',a.message,
        'authored_at',a.authored_at,'merged_at',a.merged_at,
        'author',jsonb_build_object('id',a.author_id,'name',a.author_name),
        'authors',a.authors,
        'merged_by',CASE WHEN a.merged_by IS NOT NULL THEN jsonb_build_object('id',a.merged_by,'name',a.merged_by) END,
        'pull_request_id',a.pull_request_id,'changes_count',a.changes_count,'legacy',a.legacy,
        'entities',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id,'kind',e.kind,'title',e.title) ORDER BY e.kind,e.title,e.id)
                             FROM (SELECT DISTINCT id,kind,title FROM activity_entities WHERE activity_id=a.id) e),'[]'::jsonb)
    )) ORDER BY a.merged_at DESC,a.id DESC) FROM paged a),'[]'::jsonb),
    (SELECT count(*) FROM activity)
`
