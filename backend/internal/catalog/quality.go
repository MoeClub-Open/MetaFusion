package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// 质量审计（quality audit）：按需计算、不落库。它只做自动诊断并给出提示，
// 从不阻止写入，也不修改任何数据——数据仍由 Agent 依据原始证据手工构建。
//
// 状态三档：
//   - complete           自动诊断未发现缺项。注意：这不代表经过人工核验，
//     只代表本次自动检查项全部通过；
//   - partial            存在结构性缺项（缺载体/缺曲目、曲目没有收录内容、序号未从1连续、必填字段为空）；
//   - needs_verification 存在悬挂引用等需要人工核实的数据问题。
const (
	QualityComplete          = "complete"
	QualityPartial           = "partial"
	QualityNeedsVerification = "needs_verification"

	qualitySeverityError   = "error"   // → needs_verification
	qualitySeverityWarning = "warning" // → partial

	qualityBatchMax = 100
)

// QualityChecks 列出本版本实际执行的检查码，随报告返回，便于调用方判断 complete 的覆盖面。
// 「必须具备某类关系」暂不检查：RelationshipRule 只有 MaxIncoming/MaxOutgoing 上限，没有下限/必需语义。
var QualityChecks = []string{"dangling_reference", "missing_required_field", "release_missing_media", "medium_missing_tracks", "track_empty_contents", "track_position_gap"}

type QualityIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	EntityID string `json:"entity_id"`
	Field    string `json:"field,omitempty"`
	Value    string `json:"value,omitempty"`
	Message  string `json:"message"`
}

type QualityReport struct {
	EntityID       string         `json:"entity_id"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"`
	Issues         []QualityIssue `json:"issues"`
	Checks         []string       `json:"checks"`
	DefinitionETag string         `json:"definition_etag,omitempty"`
}

type QualityBatchRequest struct {
	IDs []string `json:"ids"`
}

type QualityBatchResult struct {
	Reports []QualityReport   `json:"reports"`
	Errors  map[string]string `json:"errors"`
}

// EntityQuality 审计单个实体（可见性与 GET /entities/:id 相同）。
func (s *Store) EntityQuality(ctx context.Context, id string, u *User) (QualityReport, error) {
	if _, err := uuid.Parse(id); err != nil {
		return QualityReport{}, fmt.Errorf("invalid_id")
	}
	v, err := s.Definitions(ctx)
	if err != nil {
		return QualityReport{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return QualityReport{}, err
	}
	defer tx.Rollback()
	return auditEntity(ctx, tx, v, id, u)
}

// QualityBatch 审计多个实体；单个 id 无效/不可见不会让整批失败，记在 errors 里。
func (s *Store) QualityBatch(ctx context.Context, in QualityBatchRequest, u *User) (QualityBatchResult, error) {
	out := QualityBatchResult{Reports: []QualityReport{}, Errors: map[string]string{}}
	if len(in.IDs) == 0 {
		return out, fmt.Errorf("ids_required")
	}
	if len(in.IDs) > qualityBatchMax {
		return out, fmt.Errorf("too_many_ids")
	}
	v, err := s.Definitions(ctx)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, id := range in.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := uuid.Parse(id); err != nil {
			out.Errors[id] = "invalid_id"
			continue
		}
		r, err := auditEntity(ctx, tx, v, id, u)
		switch {
		case err == nil:
			out.Reports = append(out.Reports, r)
		case err == sql.ErrNoRows:
			out.Errors[id] = "not_found"
		default:
			return out, err
		}
	}
	return out, nil
}

func auditEntity(ctx context.Context, q queryer, v DefinitionConfig, id string, u *User) (QualityReport, error) {
	e, err := get(ctx, q, id)
	if err != nil {
		return QualityReport{}, err
	}
	if !visible(e, u) {
		return QualityReport{}, sql.ErrNoRows
	}
	d := v.Document
	rep := QualityReport{EntityID: e.ID, Kind: e.Kind, Issues: []QualityIssue{}, Checks: QualityChecks, DefinitionETag: v.ETag}

	// 1. 悬挂引用：复用 scanDangling（与 mf-migrate check-refs 同口径），范围限定为本实体及其端点边。
	rels, err := relationsWithEndpoint(ctx, q, e.ID)
	if err != nil {
		return rep, err
	}
	dangling, _, err := d.scanDangling(ctx, q, []Entity{e}, rels)
	if err != nil {
		return rep, err
	}
	for _, r := range dangling {
		field := r.Field
		if r.Scope == "relation" {
			field = "relation:" + r.ID + "." + r.Field
		}
		rep.Issues = append(rep.Issues, QualityIssue{Code: "dangling_reference", Severity: qualitySeverityError, EntityID: e.ID, Field: field, Value: r.Value,
			Message: fmt.Sprintf("reference %s (%s)", r.Value, r.Reason)})
	}

	// 2. 必填字段为空。
	keys := d.attributeKeys(e.Kind)
	sort.Strings(keys)
	for _, k := range keys {
		if f := d.Fields[k]; f.Required && f.Enabled && isEmptyValue(e.Attributes[k]) {
			rep.Issues = append(rep.Issues, QualityIssue{Code: "missing_required_field", Severity: qualitySeverityWarning, EntityID: e.ID, Field: k, Message: "required field is empty"})
		}
	}

	// 3. 结构：发行→载体→曲目→收录内容。
	switch e.Kind {
	case "release":
		mediumIDs, err := childIDs(ctx, q, "SELECT id::text FROM catalog.mediums WHERE release_id=$1 ORDER BY id", e.ID)
		if err != nil {
			return rep, err
		}
		if len(mediumIDs) == 0 {
			rep.Issues = append(rep.Issues, QualityIssue{Code: "release_missing_media", Severity: qualitySeverityWarning, EntityID: e.ID, Message: "release has no medium"})
		}
		for _, mid := range mediumIDs {
			if err := auditMediumTracks(ctx, q, mid, u, &rep); err != nil {
				return rep, err
			}
		}
	case "medium":
		if err := auditMediumTracks(ctx, q, e.ID, u, &rep); err != nil {
			return rep, err
		}
	case "track":
		if len(e.Contents) == 0 {
			rep.Issues = append(rep.Issues, QualityIssue{Code: "track_empty_contents", Severity: qualitySeverityWarning, EntityID: e.ID, Field: "contents", Message: "track has no included expression"})
		}
	}
	rep.Status = qualityStatus(rep.Issues)
	return rep, nil
}

func auditMediumTracks(ctx context.Context, q queryer, mediumID string, u *User, rep *QualityReport) error {
	ids, err := childIDs(ctx, q, "SELECT id::text FROM catalog.tracks WHERE medium_id=$1 ORDER BY id", mediumID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		rep.Issues = append(rep.Issues, QualityIssue{Code: "medium_missing_tracks", Severity: qualitySeverityWarning, EntityID: mediumID, Message: "medium has no track"})
		return nil
	}
	tracks, err := getManyFrom(ctx, q, ids, u)
	if err != nil {
		return err
	}
	positions := []int{}
	for _, id := range ids {
		t, ok := tracks[id]
		if !ok {
			continue
		}
		if t.ParentID == "" {
			positions = append(positions, t.Position)
		}
		if len(t.Contents) == 0 {
			rep.Issues = append(rep.Issues, QualityIssue{Code: "track_empty_contents", Severity: qualitySeverityWarning, EntityID: id, Field: "contents", Message: "track has no included expression"})
		}
	}
	if gap := positionGap(positions); gap != "" {
		rep.Issues = append(rep.Issues, QualityIssue{Code: "track_position_gap", Severity: qualitySeverityWarning, EntityID: mediumID, Value: gap,
			Message: "top-level track positions are not contiguous; track count may not match the source"})
	}
	return nil
}

// positionGap 检查同一 medium 内顶层曲目序号是否从 1 起连续不重复（1..n）。
// 不接受 0-based，也不接受从 2 起的「自洽连续」——缺第 1 轨也是 gap（如《アルストロメリア》）。
func positionGap(ps []int) string {
	if len(ps) == 0 {
		return ""
	}
	sort.Ints(ps)
	if ps[0] != 1 {
		return fmt.Sprintf("count=%d positions=%d..%d", len(ps), ps[0], ps[len(ps)-1])
	}
	for i := 1; i < len(ps); i++ {
		if ps[i] != ps[i-1]+1 {
			return fmt.Sprintf("count=%d positions=%d..%d", len(ps), ps[0], ps[len(ps)-1])
		}
	}
	return ""
}

func childIDs(ctx context.Context, q queryer, query, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func qualityStatus(issues []QualityIssue) string {
	status := QualityComplete
	for _, i := range issues {
		if i.Severity == qualitySeverityError {
			return QualityNeedsVerification
		}
		status = QualityPartial
	}
	return status
}
