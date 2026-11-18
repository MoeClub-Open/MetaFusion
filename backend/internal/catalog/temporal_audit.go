package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lib/pq"
)

// WorkReleaseOwnershipIssue 识别挂在 Work 上但带有特定地区/渠道/发行/运营语义的关系边，供编目核对后迁移至对应独立 Release。
type WorkReleaseOwnershipIssue struct {
	WorkID            string   `json:"work_id"`
	WorkTitle         string   `json:"work_title"`
	RelationID        string   `json:"relation_id"`
	RelationType      string   `json:"relation_type"`
	AgentID           string   `json:"agent_id"`
	AgentTitle        string   `json:"agent_title"`
	CreditRole        string   `json:"credit_role,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	Region            string   `json:"region,omitempty"`
	SuggestedRelation string   `json:"suggested_relation"`
	CandidateReleases []string `json:"candidate_releases"`
}

// AuditWorkReleaseOwnership 扫描指定（或全部）Work 上疑似地区发行/运营归属的关系边。
func (s *Store) AuditWorkReleaseOwnership(ctx context.Context, workIDs []string) ([]WorkReleaseOwnershipIssue, error) {
	query := "SELECT r.id::text, r.type, w.id::text, w.title, a.id::text, a.title, coalesce(r.document->'attributes', '{}'::jsonb) " +
		"FROM catalog.relations r " +
		"JOIN catalog.entities w ON w.id = r.source_id AND w.kind = 'work' AND w.state = 'active' " +
		"JOIN catalog.entities a ON a.id = r.target_id AND a.kind = 'agent' AND a.state = 'active' " +
		"WHERE r.type IN ('produced_by', 'credit_for')"
	args := []any{}
	if len(workIDs) > 0 {
		query += " AND w.id = ANY($1::uuid[])"
		args = append(args, pq.Array(uniqueSortedUUIDs(workIDs)))
	}
	query += " ORDER BY w.id, r.id"

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WorkReleaseOwnershipIssue{}
	for rows.Next() {
		var relID, relType, wID, wTitle, aID, aTitle string
		var rawAttrs []byte
		if err := rows.Scan(&relID, &relType, &wID, &wTitle, &aID, &aTitle, &rawAttrs); err != nil {
			return nil, err
		}
		attrs := map[string]any{}
		_ = json.Unmarshal(rawAttrs, &attrs)
		creditRole := attrString(attrs, "credit_role")
		role := attrString(attrs, "role")
		scopeTxt := attrString(attrs, "scope")
		regionTxt := attrString(attrs, "region")
		combined := strings.ToLower(creditRole + " " + role + " " + scopeTxt + " " + regionTxt)
		suggested := ""
		if strings.Contains(combined, "运营") || strings.Contains(combined, "營運") || strings.Contains(combined, "運営") || strings.Contains(combined, "operator") || strings.Contains(combined, "代理") {
			suggested = "operated_by"
		} else if strings.Contains(combined, "发行") || strings.Contains(combined, "發行") || strings.Contains(combined, "発売") || strings.Contains(combined, "配信") || strings.Contains(combined, "publish") || regionTxt != "" || scopeTxt != "" {
			suggested = "published_by"
		}
		if suggested == "" {
			continue
		}
		cands, err := subjectReleaseIDsTx(ctx, s.DB, wID)
		if err != nil {
			return nil, err
		}
		out = append(out, WorkReleaseOwnershipIssue{
			WorkID:            wID,
			WorkTitle:         wTitle,
			RelationID:        relID,
			RelationType:      relType,
			AgentID:           aID,
			AgentTitle:        aTitle,
			CreditRole:        creditRole,
			Scope:             scopeTxt,
			Region:            regionTxt,
			SuggestedRelation: suggested,
			CandidateReleases: cands,
		})
	}
	return out, rows.Err()
}

// HistoricalRevisionPictureCandidate 记录存量历史修订中曾出现但当前未保留在 pictures[] 内的旧图片。
type HistoricalRevisionPictureCandidate struct {
	EntityID           string    `json:"entity_id"`
	EntityKind         string    `json:"entity_kind"`
	EntityTitle        string    `json:"entity_title"`
	RevisionVersion    int       `json:"revision_version"`
	RevisionRecordedAt time.Time `json:"revision_recorded_at"`
	SuggestedPicture   Picture   `json:"suggested_picture"`
}

// TemporalMigrationAuditReport 汇总需人工核对后迁移的 Work/Release 归属与历史覆盖图片候选。
type TemporalMigrationAuditReport struct {
	WorkReleaseOwnershipIssues []WorkReleaseOwnershipIssue          `json:"work_release_ownership_issues"`
	UnarchivedRevisionPictures []HistoricalRevisionPictureCandidate `json:"unarchived_revision_pictures"`
}

// AuditHistoricalRevisionPictures 扫描存量修订中曾存在但当前实体 pictures[] 已缺失的图片资产/URL。
func (s *Store) AuditHistoricalRevisionPictures(ctx context.Context, entityIDs []string) ([]HistoricalRevisionPictureCandidate, error) {
	query := "SELECT e.id::text, e.kind, e.title, e.document, r.version, r.created_at, r.snapshot->'pictures' " +
		"FROM catalog.entities e JOIN catalog.revisions r ON r.target_id = e.id::text " +
		"WHERE e.state = 'active' AND jsonb_typeof(r.snapshot->'pictures') = 'array' AND jsonb_array_length(r.snapshot->'pictures') > 0"
	args := []any{}
	if len(entityIDs) > 0 {
		query += " AND e.id = ANY($1::uuid[])"
		args = append(args, pq.Array(uniqueSortedUUIDs(entityIDs)))
	}
	query += " ORDER BY e.id, r.version DESC"

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	currentSeen := map[string]map[string]bool{}
	emitted := map[string]bool{}
	out := []HistoricalRevisionPictureCandidate{}

	picIdentity := func(p Picture) string {
		if aid := strings.TrimSpace(p.AssetID); aid != "" {
			return "asset:" + aid
		}
		if u := strings.TrimSpace(p.URL); u != "" {
			return "url:" + u
		}
		return ""
	}

	for rows.Next() {
		var eID, eKind, eTitle string
		var curDoc, revPicsRaw []byte
		var revVer int
		var revAt time.Time
		if err := rows.Scan(&eID, &eKind, &eTitle, &curDoc, &revVer, &revAt, &revPicsRaw); err != nil {
			return nil, err
		}
		if _, ok := currentSeen[eID]; !ok {
			seen := map[string]bool{}
			var curEntity Entity
			if json.Unmarshal(curDoc, &curEntity) == nil {
				for _, cp := range curEntity.Pictures {
					if k := picIdentity(cp); k != "" {
						seen[k] = true
					}
					if u := strings.TrimSpace(cp.URL); u != "" {
						seen["url:"+u] = true
					}
				}
			}
			currentSeen[eID] = seen
		}
		var revPics []Picture
		if json.Unmarshal(revPicsRaw, &revPics) != nil {
			continue
		}
		for _, rp := range revPics {
			key := picIdentity(rp)
			if key == "" || currentSeen[eID][key] || currentSeen[eID]["url:"+strings.TrimSpace(rp.URL)] {
				continue
			}
			dedupe := eID + "|" + key
			if emitted[dedupe] {
				continue
			}
			emitted[dedupe] = true
			recAt := revAt.UTC()
			rp.RecordedAt = &recAt
			if rp.UsagePeriod != nil {
				if rp.ValidFrom == "" {
					rp.ValidFrom = strings.TrimSpace(rp.UsagePeriod.Begin)
				}
				if rp.ValidTo == "" {
					rp.ValidTo = strings.TrimSpace(rp.UsagePeriod.End)
				}
			}
			out = append(out, HistoricalRevisionPictureCandidate{
				EntityID:           eID,
				EntityKind:         eKind,
				EntityTitle:        eTitle,
				RevisionVersion:    revVer,
				RevisionRecordedAt: recAt,
				SuggestedPicture:   rp,
			})
		}
	}
	return out, rows.Err()
}
