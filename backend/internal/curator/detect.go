package curator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

const (
	detectorLimit       = 100
	duplicateGroupLimit = 50
)

type detectorSpec struct {
	code    string
	enabled func(Settings) bool
	run     func(context.Context) ([]candidate, error)
}

func (s *Service) detectors() []detectorSpec {
	return []detectorSpec{
		{CodeDuplicateTitle, func(v Settings) bool { return v.AllowNames || v.AllowDedupe }, s.detectDuplicateTitles},
		{CodeWorkMissingForm, func(v Settings) bool { return v.AllowStructures }, s.detectWorkMissingForm},
		{CodeTVAnimeWithoutCredits, func(v Settings) bool { return v.AllowStructures }, s.detectTVAnimeWithoutAgent},
		{CodeReleaseWithoutSubject, func(v Settings) bool { return v.AllowStructures }, s.detectReleaseWithoutSubject},
		{CodeTranslationZHKana, func(v Settings) bool { return v.AllowNames }, s.detectTranslationZHKana},
		{CodeDuplicateTagValues, func(v Settings) bool { return v.AllowDedupe }, s.detectDuplicateTagValues},
		{CodeDuplicatePictureURLs, func(v Settings) bool { return v.AllowDedupe }, s.detectDuplicatePictureValues},
		{CodeMissingPicture, func(v Settings) bool { return v.AllowSources }, s.detectMissingPicture},
	}
}

func (s *Service) enabledDetectors(settings Settings) []detectorSpec {
	out := []detectorSpec{}
	for _, spec := range s.detectors() {
		if spec.enabled(settings) {
			out = append(out, spec)
		}
	}
	return out
}

func (s *Service) AuditQualityBatch(ctx context.Context, settings Settings) (int, error) {
	if !settings.AllowStructures {
		return 0, nil
	}
	ids := []string{}
	updated := []time.Time{}
	args := []any{}
	where := `e.state='active'`
	if settings.SweepCursorUpdatedAt != nil && settings.SweepCursorID != "" {
		args = append(args, *settings.SweepCursorUpdatedAt, settings.SweepCursorID)
		where += fmt.Sprintf(` AND (e.updated_at<$1 OR (e.updated_at=$1 AND e.id<$2::uuid))`)
	}
	args = append(args, 100)
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,e.updated_at FROM catalog.entities e WHERE `+where+fmt.Sprintf(` ORDER BY e.updated_at DESC,e.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		updated = append(updated, at)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(ids) == 0 {
		// 全库质量扫描完成：有自定义检测器就交给它们轮转，否则从头再来。
		next := -1
		if len(s.enabledDetectors(settings)) > 0 {
			next = 0
		}
		return 0, s.ResetSweepCursor(ctx, next)
	}
	result, err := s.Catalog.QualityBatch(ctx, catalog.QualityBatchRequest{IDs: ids}, nil)
	if err != nil {
		return 0, err
	}
	candidates := qualityCandidates(result.Reports)
	n, err := s.UpsertFindings(ctx, candidates)
	if err != nil {
		return n, err
	}
	next := 0
	if len(s.enabledDetectors(settings)) == 0 {
		next = -1
	}
	return n, s.UpdateSweepCursor(ctx, updated[len(updated)-1], ids[len(ids)-1], next)
}

func qualityCandidates(reports []catalog.QualityReport) []candidate {
	out := []candidate{}
	seen := map[string]bool{}
	for _, report := range reports {
		for _, issue := range report.Issues {
			entityID, kind := report.EntityID, report.Kind
			switch issue.Code {
			case CodeMediumMissingTracks, CodeTrackPositionGap:
				entityID, kind = issue.EntityID, "medium"
			case CodeTrackEmptyContents:
				entityID, kind = issue.EntityID, "track"
			}
			sig := signature(issue.Code, issue.Field, issue.Value, entityID, report.EntityID)
			key := entityID + "|" + issue.Code + "|" + sig
			if seen[key] {
				continue
			}
			seen[key] = true
			details := map[string]any{"message": issue.Message}
			if issue.Field != "" {
				details["field"] = issue.Field
			}
			if issue.Value != "" {
				details["value"] = issue.Value
			}
			if issue.EntityID != "" && issue.EntityID != report.EntityID {
				details["child_entity_id"] = issue.EntityID
			}
			out = append(out, candidate{EntityID: entityID, Kind: kind, Code: issue.Code, Severity: issue.Severity, Priority: qualityPriority(issue.Code), Signature: sig, Details: details})
		}
	}
	return out
}

func qualityPriority(code string) int {
	switch code {
	case CodeTrackPositionGap:
		return 1
	case CodeDanglingReference:
		return 1
	default:
		return 2
	}
}

func (s *Service) SweepDetector(ctx context.Context, settings Settings, index int) (int, int, error) {
	specs := s.enabledDetectors(settings)
	index = normalizeDetector(index, len(specs))
	if index < 0 {
		return -1, 0, nil
	}
	spec := specs[index]
	next := index + 1
	if next >= len(specs) {
		next = -1
	}
	candidates, err := spec.run(ctx)
	if err != nil {
		return next, 0, err
	}
	n, err := s.UpsertFindings(ctx, candidates)
	if err != nil {
		return next, n, err
	}
	return next, n, nil
}

// normalizeDetector 把持久化的检测器游标夹到当前启用的集合：-1 表示质量扫描，
// 质量扫描关闭时从第一个自定义检测器开始。
func normalizeDetector(index, count int) int {
	if count <= 0 {
		return -1
	}
	if index < 0 {
		return 0
	}
	return index % count
}

// notDecided 过滤已有结论的实体：同一 code 已有 open/running/failed/done/ignored
// 的候选时不再重复入队，检测器才能越过前 100 条继续向后扫描；只有 resolved
// （已合入或问题已消失）会在问题重新出现时再次入队。
func notDecided(code string) string {
	return fmt.Sprintf(` AND NOT EXISTS (SELECT 1 FROM curator.findings f WHERE f.entity_id=e.id AND f.code='%s' AND f.status<>'resolved')`, code)
}

func (s *Service) detectDuplicateTitles(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.kind,btrim(e.title),array_agg(e.id::text ORDER BY e.id)
		FROM catalog.entities e
		WHERE e.state='active' AND e.kind IN ('agent','work','release','collection')
		  AND btrim(e.title)<>''`+notDecided(CodeDuplicateTitle)+`
		GROUP BY 1,2 HAVING count(*)>1
		ORDER BY count(*) DESC,1,2 LIMIT $1`, duplicateGroupLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var kind, title string
		var ids []string
		if err := rows.Scan(&kind, &title, pq.Array(&ids)); err != nil {
			return nil, err
		}
		if len(ids) < 2 {
			continue
		}
		peers := ids
		if len(peers) > 10 {
			peers = peers[:10]
		}
		sig := signature("title", normalizeTitle(title))
		for i, id := range ids {
			if len(out) >= 500 || i >= 25 {
				break
			}
			out = append(out, candidate{EntityID: id, Kind: kind, Code: CodeDuplicateTitle, Severity: severityWarning, Priority: 2,
				Signature: sig, Details: map[string]any{"title": title, "duplicate_count": len(ids), "peer_ids": peers}})
		}
		if len(out) >= 500 {
			break
		}
	}
	return out, rows.Err()
}

func (s *Service) detectWorkMissingForm(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,btrim(e.title)
		FROM catalog.entities e
		WHERE e.state='active' AND e.kind='work'
		  AND COALESCE(btrim(e.document->'attributes'->>'creation_form'),'')=''`+notDecided(CodeWorkMissingForm)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out = append(out, candidate{EntityID: id, Kind: "work", Code: CodeWorkMissingForm, Severity: severityWarning, Priority: 2,
			Signature: "creation_form", Details: map[string]any{"title": title}})
	}
	return out, rows.Err()
}

func (s *Service) detectTVAnimeWithoutAgent(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,btrim(e.title)
		FROM catalog.entities e
		WHERE e.state='active' AND e.kind='work' AND e.document->'attributes'->>'creation_form'='tv_anime'
		  AND NOT EXISTS (
			SELECT 1 FROM catalog.relations r
			JOIN catalog.entities peer ON peer.id=CASE WHEN r.source_id=e.id THEN r.target_id ELSE r.source_id END
			WHERE (r.source_id=e.id OR r.target_id=e.id) AND peer.kind='agent' AND peer.state='active'
		  )`+notDecided(CodeTVAnimeWithoutCredits)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out = append(out, candidate{EntityID: id, Kind: "work", Code: CodeTVAnimeWithoutCredits, Severity: severityWarning, Priority: 2,
			Signature: "no_agent", Details: map[string]any{"title": title}})
	}
	return out, rows.Err()
}

func (s *Service) detectReleaseWithoutSubject(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,btrim(e.title)
		FROM catalog.entities e
		WHERE e.state='active' AND e.kind='release'
		  AND NOT EXISTS (SELECT 1 FROM catalog.release_subjects s WHERE s.release_id=e.id)`+notDecided(CodeReleaseWithoutSubject)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out = append(out, candidate{EntityID: id, Kind: "release", Code: CodeReleaseWithoutSubject, Severity: severityWarning, Priority: 2,
			Signature: "no_subject", Details: map[string]any{"title": title}})
	}
	return out, rows.Err()
}

func (s *Service) detectTranslationZHKana(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,e.kind,btrim(e.title),e.document->'translations'->'zh-CN'->>'title'
		FROM catalog.entities e
		WHERE e.state='active' AND COALESCE(e.document->'translations'->'zh-CN'->>'title','') ~ '[ぁ-んァ-ヶ]'`+notDecided(CodeTranslationZHKana)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, kind, title, value string
		if err := rows.Scan(&id, &kind, &title, &value); err != nil {
			return nil, err
		}
		out = append(out, candidate{EntityID: id, Kind: kind, Code: CodeTranslationZHKana, Severity: severityWarning, Priority: 2,
			Signature: signature("zh-CN.title", value), Details: map[string]any{"title": title, "zh-CN_title": value}})
	}
	return out, rows.Err()
}

func (s *Service) detectDuplicateTagValues(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,e.kind,btrim(e.title),e.document->'attributes'->'tags'
		FROM catalog.entities e
		WHERE e.state='active' AND jsonb_typeof(e.document->'attributes'->'tags')='array'
		  AND CASE WHEN jsonb_typeof(e.document->'attributes'->'tags')='array' THEN
		      jsonb_array_length(e.document->'attributes'->'tags') <>
		      (SELECT count(DISTINCT t.value) FROM jsonb_array_elements_text(e.document->'attributes'->'tags') t(value)) ELSE false END`+notDecided(CodeDuplicateTagValues)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, kind, title string
		var raw []byte
		if err := rows.Scan(&id, &kind, &title, &raw); err != nil {
			return nil, err
		}
		var tags []string
		if json.Unmarshal(raw, &tags) != nil {
			continue
		}
		duplicates := duplicateStrings(tags)
		if len(duplicates) == 0 {
			continue
		}
		out = append(out, candidate{EntityID: id, Kind: kind, Code: CodeDuplicateTagValues, Severity: severityWarning, Priority: 1,
			Signature: signature("tags", strings.Join(duplicates, ",")), Details: map[string]any{"title": title, "duplicate_values": duplicates}})
	}
	return out, rows.Err()
}

func (s *Service) detectDuplicatePictureValues(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,e.kind,btrim(e.title),e.document->'pictures'
		FROM catalog.entities e
		WHERE e.state='active' AND jsonb_typeof(e.document->'pictures')='array'
		  AND CASE WHEN jsonb_typeof(e.document->'pictures')='array' THEN
		      jsonb_array_length(e.document->'pictures') <>
		      (SELECT count(DISTINCT p.value::text) FROM jsonb_array_elements(e.document->'pictures') p(value)) ELSE false END`+notDecided(CodeDuplicatePictureURLs)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, kind, title string
		var raw []byte
		if err := rows.Scan(&id, &kind, &title, &raw); err != nil {
			return nil, err
		}
		var pictures []json.RawMessage
		if json.Unmarshal(raw, &pictures) != nil {
			continue
		}
		seen := map[string]bool{}
		duplicates := []string{}
		for _, p := range pictures {
			key := string(p)
			if seen[key] {
				var v struct {
					URL string `json:"url"`
				}
				_ = json.Unmarshal(p, &v)
				value := v.URL
				if value == "" {
					value = truncate(key, 200)
				}
				duplicates = appendUnique(duplicates, value)
				continue
			}
			seen[key] = true
		}
		if len(duplicates) == 0 {
			continue
		}
		sort.Strings(duplicates)
		out = append(out, candidate{EntityID: id, Kind: kind, Code: CodeDuplicatePictureURLs, Severity: severityWarning, Priority: 1,
			Signature: signature("pictures", strings.Join(duplicates, ",")), Details: map[string]any{"title": title, "duplicate_urls": duplicates}})
	}
	return out, rows.Err()
}

func (s *Service) detectMissingPicture(ctx context.Context) ([]candidate, error) {
	rows, err := s.Catalog.DB.QueryContext(ctx, `SELECT e.id::text,e.kind,btrim(e.title)
		FROM catalog.entities e
		WHERE e.state='active' AND e.kind IN ('agent','work','release','collection')
		  AND CASE WHEN jsonb_typeof(e.document->'pictures')='array' THEN jsonb_array_length(e.document->'pictures')=0 ELSE true END
		  AND CASE WHEN jsonb_typeof(e.document->'external_links')='array' THEN jsonb_array_length(e.document->'external_links')>0 ELSE false END`+notDecided(CodeMissingPicture)+`
		ORDER BY e.updated_at DESC,e.id LIMIT $1`, detectorLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var id, kind, title string
		if err := rows.Scan(&id, &kind, &title); err != nil {
			return nil, err
		}
		out = append(out, candidate{EntityID: id, Kind: kind, Code: CodeMissingPicture, Severity: severityInfo, Priority: 3,
			Signature: "no_picture", Details: map[string]any{"title": title}})
	}
	return out, rows.Err()
}

// EntityStillHas reports whether the original finding is still present.
func (s *Service) EntityStillHas(ctx context.Context, finding Finding) (bool, error) {
	if isQualityCode(finding.Code) {
		report, err := s.Catalog.EntityQuality(ctx, finding.EntityID, nil)
		if err != nil {
			if err == sql.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		for _, issue := range report.Issues {
			if issue.Code == finding.Code {
				return true, nil
			}
		}
		return false, nil
	}
	entity, err := s.Catalog.Get(ctx, finding.EntityID, nil)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	switch finding.Code {
	case CodeDuplicateTitle:
		var n int
		err = s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.entities WHERE state='active' AND kind=$1 AND btrim(title)=btrim($2)`, entity.Kind, entity.Title).Scan(&n)
		return n > 1, err
	case CodeWorkMissingForm:
		return strings.TrimSpace(stringValue(entity.Attributes["creation_form"])) == "", nil
	case CodeTVAnimeWithoutCredits:
		var n int
		err = s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.relations r
			JOIN catalog.entities peer ON peer.id=CASE WHEN r.source_id=$1 THEN r.target_id ELSE r.source_id END
			WHERE (r.source_id=$1 OR r.target_id=$1) AND peer.kind='agent' AND peer.state='active'`, entity.ID).Scan(&n)
		return n == 0, err
	case CodeReleaseWithoutSubject:
		var n int
		err = s.Catalog.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.release_subjects WHERE release_id=$1`, entity.ID).Scan(&n)
		return n == 0, err
	case CodeTranslationZHKana:
		return hasKana(translationTitle(entity, "zh-CN")), nil
	case CodeDuplicateTagValues:
		return len(duplicateStrings(stringSlice(entity.Attributes["tags"]))) > 0, nil
	case CodeDuplicatePictureURLs:
		return hasDuplicatePictures(entity), nil
	case CodeMissingPicture:
		return len(entity.Pictures) == 0 && len(entity.ExternalLinks) > 0, nil
	}
	return true, nil
}

func isQualityCode(code string) bool {
	switch code {
	case CodeDanglingReference, CodeMissingRequiredField, CodeReleaseMissingMedia, CodeMediumMissingTracks, CodeTrackEmptyContents, CodeTrackPositionGap:
		return true
	}
	return false
}

func signature(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, truncate(p, 160))
		}
	}
	return truncate(strings.Join(out, ":"), 500)
}

func normalizeTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

func duplicateStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if seen[v] {
			out = appendUnique(out, v)
			continue
		}
		seen[v] = true
	}
	sort.Strings(out)
	return out
}

func appendUnique(values []string, v string) []string {
	for _, x := range values {
		if x == v {
			return values
		}
	}
	return append(values, v)
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringSlice(v any) []string {
	out := []string{}
	for _, item := range anySlice(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func anySlice(v any) []any {
	switch value := v.(type) {
	case []any:
		return value
	case []string:
		out := make([]any, len(value))
		for i := range value {
			out[i] = value[i]
		}
		return out
	}
	return nil
}

func translationTitle(e catalog.Entity, locale string) string {
	if t, ok := e.Translations[locale]; ok {
		return t.Title
	}
	return ""
}

func hasKana(value string) bool {
	for _, r := range value {
		if (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) {
			return true
		}
	}
	return false
}

func hasDuplicatePictures(e catalog.Entity) bool {
	seen := map[string]bool{}
	for _, p := range e.Pictures {
		raw, _ := json.Marshal(p)
		if seen[string(raw)] {
			return true
		}
		seen[string(raw)] = true
	}
	return false
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
