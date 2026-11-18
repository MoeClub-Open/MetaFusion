package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// PicturesQuery 描述 GET /api/catalog/entities/:id/pictures 的查询参数。
type PicturesQuery struct {
	Role      string
	At        string
	ReleaseID string
	Region    string
	Platform  string
}

type temporalEvalState int

const (
	temporalExcluded temporalEvalState = iota
	temporalDisputed
	temporalConfirmed
)

type temporalBounds struct {
	ValidFrom     string
	ValidTo       string
	BeginDate     string
	EndDate       string
	Ended         bool
	DatePrecision string
	EarliestDate  string
	LatestDate    string
}

// evalTemporalBounds 按 [valid_from, valid_to) 左闭右开语义评估一条事实在 at 时点是否成立。
// 支持仅知年月（YYYY / YYYY-MM）与存在时间争议（earliest_date..latest_date / disputed）的记录。
func evalTemporalBounds(b temporalBounds, at string) temporalEvalState {
	at = strings.TrimSpace(at)
	if at == "" {
		return temporalExcluded
	}
	qMin := pictureTimeStart(at)
	qMax := pictureTimeEnd(at)

	startStr := firstNonEmpty(b.ValidFrom, b.BeginDate)
	exclusiveEnd := strings.TrimSpace(b.ValidTo)
	inclusiveEnd := strings.TrimSpace(b.EndDate)
	earliestStr := strings.TrimSpace(b.EarliestDate)
	latestStr := strings.TrimSpace(b.LatestDate)
	disputed := strings.TrimSpace(b.DatePrecision) == "disputed"

	if startStr == "" && earliestStr == "" && latestStr == "" && exclusiveEnd == "" && inclusiveEnd == "" {
		return temporalExcluded
	}

	var hasStart bool
	var sMin, sMax time.Time
	if disputed && earliestStr != "" && latestStr != "" && startStr == "" {
		hasStart = true
		sMin = pictureTimeStart(earliestStr)
		sMax = pictureTimeEnd(latestStr)
	} else if startStr != "" {
		hasStart = true
		sMin = pictureTimeStart(startStr)
		if earliestStr != "" && pictureTimeStart(earliestStr).Before(sMin) {
			sMin = pictureTimeStart(earliestStr)
		}
		if startStr == at || len(startStr) >= len(at) {
			sMax = pictureTimeStart(startStr)
		} else {
			sMax = pictureTimeEnd(startStr)
		}
		if disputed {
			if latestStr != "" && pictureTimeEnd(latestStr).After(sMax) {
				sMax = pictureTimeEnd(latestStr)
			} else if startStr != at {
				sMax = pictureTimeEnd(startStr)
			}
		}
	} else if earliestStr != "" || latestStr != "" {
		hasStart = true
		if earliestStr != "" {
			sMin = pictureTimeStart(earliestStr)
		}
		if latestStr != "" {
			sMax = pictureTimeEnd(latestStr)
		} else {
			sMax = pictureTimeEnd("9999-12-31")
		}
	}

	if hasStart && qMax.Before(sMin) {
		return temporalExcluded
	}

	var hasEnd bool
	var eMin, eMax time.Time
	if exclusiveEnd != "" {
		hasEnd = true
		if exclusiveEnd == at {
			return temporalExcluded
		}
		eMin = pictureTimeStart(exclusiveEnd)
		if len(exclusiveEnd) >= len(at) {
			eMax = pictureTimeStart(exclusiveEnd)
		} else {
			eMax = pictureTimeEnd(exclusiveEnd).Add(time.Nanosecond)
		}
	} else if inclusiveEnd != "" {
		hasEnd = true
		if len(inclusiveEnd) >= len(at) || inclusiveEnd == at {
			eMin = pictureTimeEnd(inclusiveEnd).Add(time.Nanosecond)
			eMax = eMin
		} else {
			eMin = pictureTimeStart(inclusiveEnd)
			eMax = pictureTimeEnd(inclusiveEnd).Add(time.Nanosecond)
		}
	}

	if hasEnd && !qMin.Before(eMax) {
		return temporalExcluded
	}

	if b.Ended && !hasEnd {
		return temporalDisputed
	}
	if !hasStart && hasEnd {
		return temporalDisputed
	}
	if disputed {
		return temporalDisputed
	}
	if hasStart && qMin.Before(sMax) {
		return temporalDisputed
	}
	if hasEnd && !qMax.Before(eMin) {
		return temporalDisputed
	}
	return temporalConfirmed
}

func splitScopeTokens(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '/' || r == '、' || r == ';' || r == '|'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.ToLower(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func matchesTokenScope(value, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	want := splitScopeTokens(filter)
	have := splitScopeTokens(value)
	for _, w := range want {
		m := false
		for _, h := range have {
			if h == w || strings.Contains(h, w) {
				m = true
				break
			}
		}
		if !m {
			return false
		}
	}
	return true
}

// scoreFactScope 允许无范围声明的通用记录通过过滤，并返回范围特异度分数（越高越优先）。
func scoreFactScope(factRegion, factPlatform, factReleaseID string, scope TimelineScope) (bool, int) {
	score := 0
	if qRel := strings.TrimSpace(scope.ReleaseID); qRel != "" {
		if fRel := strings.TrimSpace(factReleaseID); fRel != "" {
			if fRel != qRel {
				return false, 0
			}
			score += 4
		}
	} else if strings.TrimSpace(factReleaseID) != "" {
		score += 1
	}
	if qReg := strings.TrimSpace(scope.Region); qReg != "" {
		if strings.TrimSpace(factRegion) != "" {
			if !matchesTokenScope(factRegion, qReg) {
				return false, 0
			}
			score += 2
		}
	}
	if qPlat := strings.TrimSpace(scope.Platform); qPlat != "" {
		if strings.TrimSpace(factPlatform) != "" {
			if !matchesTokenScope(factPlatform, qPlat) {
				return false, 0
			}
			score += 2
		}
	}
	return true, score
}

// matchesScalarScope 用于单值标量（标题、语种译名、单个动态属性）的范围决议。
func matchesScalarScope(factRegion, factPlatform, factReleaseID string, owner Entity, scope TimelineScope) (bool, int) {
	effScope := scope
	ownerReg := attrString(owner.Attributes, "country")
	ownerPlat := attrString(owner.Attributes, "platform")
	if owner.Kind == "release" {
		if effScope.ReleaseID == "" {
			effScope.ReleaseID = owner.ID
		}
		if effScope.Region == "" {
			effScope.Region = ownerReg
		}
		if effScope.Platform == "" {
			effScope.Platform = ownerPlat
		}
	}
	if strings.TrimSpace(factReleaseID) != "" && strings.TrimSpace(effScope.ReleaseID) == "" {
		return false, 0
	}
	if strings.TrimSpace(factRegion) != "" && strings.TrimSpace(effScope.Region) == "" {
		if ownerReg == "" || !matchesTokenScope(ownerReg, factRegion) {
			return false, 0
		}
	}
	if strings.TrimSpace(factPlatform) != "" && strings.TrimSpace(effScope.Platform) == "" {
		if ownerPlat == "" || !matchesTokenScope(ownerPlat, factPlatform) {
			return false, 0
		}
	}
	return scoreFactScope(factRegion, factPlatform, factReleaseID, effScope)
}

func subjectReleaseIDsTx(ctx context.Context, q queryer, workID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT DISTINCT release_id::text FROM catalog.release_subjects WHERE work_id = $1::uuid ORDER BY 1", workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil {
			return nil, err
		}
		ids = append(ids, rid)
	}
	return ids, rows.Err()
}

func loadPictureRecordedTimesTx(ctx context.Context, q queryer, entityIDs []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	uniq := uniqueSortedUUIDs(entityIDs)
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		"SELECT r.target_id, coalesce(pic->>'url', ''), min(r.created_at) "+
			"FROM catalog.revisions r, "+
			"jsonb_array_elements(CASE WHEN jsonb_typeof(r.snapshot->'pictures')='array' THEN r.snapshot->'pictures' ELSE '[]'::jsonb END) AS pic "+
			"WHERE r.target_id = ANY($1::text[]) AND coalesce(pic->>'url', '') <> '' "+
			"GROUP BY r.target_id, pic->>'url'", pq.Array(uniq))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var eid, url string
		var ts time.Time
		if err := rows.Scan(&eid, &url, &ts); err != nil {
			return nil, err
		}
		out[eid+"|"+strings.TrimSpace(url)] = ts.UTC()
	}
	return out, rows.Err()
}

func loadExternalLinkRecordedTimesTx(ctx context.Context, q queryer, entityIDs []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	uniq := uniqueSortedUUIDs(entityIDs)
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		"SELECT r.target_id, coalesce(lnk->>'kind', ''), coalesce(lnk->>'url', ''), min(r.created_at) "+
			"FROM catalog.revisions r, "+
			"jsonb_array_elements(CASE WHEN jsonb_typeof(r.snapshot->'external_links')='array' THEN r.snapshot->'external_links' ELSE '[]'::jsonb END) AS lnk "+
			"WHERE r.target_id = ANY($1::text[]) AND coalesce(lnk->>'url', '') <> '' "+
			"GROUP BY r.target_id, coalesce(lnk->>'kind', ''), coalesce(lnk->>'url', '')", pq.Array(uniq))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var eid, kind, url string
		var ts time.Time
		if err := rows.Scan(&eid, &kind, &url, &ts); err != nil {
			return nil, err
		}
		out[eid+"|"+strings.TrimSpace(kind)+"|"+strings.TrimSpace(url)] = ts.UTC()
	}
	return out, rows.Err()
}

func loadRelationRecordedTimesTx(ctx context.Context, q queryer, relIDs []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	uniq := uniqueSortedUUIDs(relIDs)
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		"SELECT target_id, min(created_at) FROM catalog.revisions WHERE target_id = ANY($1::text[]) GROUP BY target_id",
		pq.Array(uniq))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rid string
		var ts time.Time
		if err := rows.Scan(&rid, &ts); err != nil {
			return nil, err
		}
		out[rid] = ts.UTC()
	}
	return out, rows.Err()
}

func pictureBounds(p Picture) temporalBounds {
	b := temporalBounds{
		ValidFrom:     strings.TrimSpace(p.ValidFrom),
		ValidTo:       strings.TrimSpace(p.ValidTo),
		DatePrecision: strings.TrimSpace(p.DatePrecision),
		EarliestDate:  strings.TrimSpace(p.EarliestDate),
		LatestDate:    strings.TrimSpace(p.LatestDate),
	}
	if p.UsagePeriod != nil {
		if b.BeginDate == "" {
			b.BeginDate = strings.TrimSpace(p.UsagePeriod.Begin)
		}
		if b.EndDate == "" {
			b.EndDate = strings.TrimSpace(p.UsagePeriod.End)
		}
	}
	return b
}

func isPictureHistoricalNow(p Picture, index int) bool {
	now := time.Now().UTC()
	if vt := strings.TrimSpace(p.ValidTo); vt != "" && !now.Before(pictureTimeStart(vt)) {
		return true
	}
	if p.UsagePeriod != nil {
		if end := strings.TrimSpace(p.UsagePeriod.End); end != "" && pictureTimeEnd(end).Before(now) {
			return true
		}
	}
	return false
}

func isCoverRole(role string) bool {
	r := strings.TrimSpace(role)
	return r == "" || r == "cover_art" || r == "key_visual" || r == "poster"
}

type pictureCandidate struct {
	ownerID    string
	ownerKind  string
	ownerStart string
	index      int
	picture    Picture
	recordedAt *time.Time
}

// selectTemporalPicture 按有效区间优先、其次手工置顶、无法确认返回 unknown 的规则选择历史主图。
func selectTemporalPicture(candidates []pictureCandidate, roleMatch func(string) bool, at string, scope TimelineScope) TemporalPictureSelection {
	type scored struct {
		c     pictureCandidate
		score int
	}
	var confirmed []scored
	var pinned []scored

	for _, cand := range candidates {
		p := cand.picture
		if !roleMatch(p.Role) {
			continue
		}
		effReleaseID := strings.TrimSpace(p.ReleaseID)
		if cand.ownerKind == "release" && effReleaseID == "" {
			effReleaseID = cand.ownerID
		}
		if scope.ReleaseID != "" && effReleaseID != "" && effReleaseID != scope.ReleaseID {
			continue
		}
		okScope, spScore := scoreFactScope(p.Region, p.Platform, effReleaseID, scope)
		if !okScope {
			continue
		}
		if cand.ownerKind == "release" && scope.ReleaseID == cand.ownerID {
			spScore += 4
		}
		b := pictureBounds(p)
		st := evalTemporalBounds(b, at)
		if st == temporalConfirmed {
			confirmed = append(confirmed, scored{c: cand, score: spScore})
			continue
		}
		if p.Pinned {
			hasExplicitBounds := b.ValidFrom != "" || b.BeginDate != "" || b.ValidTo != "" || b.EndDate != "" || b.EarliestDate != "" || b.LatestDate != ""
			if hasExplicitBounds && st == temporalExcluded {
				continue
			}
			if !hasExplicitBounds && cand.ownerStart != "" && pictureTimeEnd(at).Before(pictureTimeStart(cand.ownerStart)) {
				continue
			}
			pinned = append(pinned, scored{c: cand, score: spScore})
		}
	}

	sortScored := func(list []scored) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].score != list[j].score {
				return list[i].score > list[j].score
			}
			return list[i].c.index < list[j].c.index
		})
	}

	if len(confirmed) > 0 {
		sortScored(confirmed)
		win := confirmed[0].c
		picCopy := win.picture
		return TemporalPictureSelection{Status: "confirmed", Picture: &picCopy, OwnerID: win.ownerID, RecordedAt: win.recordedAt}
	}
	if len(pinned) > 0 {
		sortScored(pinned)
		win := pinned[0].c
		picCopy := win.picture
		return TemporalPictureSelection{Status: "pinned", Picture: &picCopy, OwnerID: win.ownerID, RecordedAt: win.recordedAt}
	}
	return TemporalPictureSelection{Status: "unknown"}
}

// EntityPicturesQuery 实现 GET /api/catalog/entities/:id/pictures?role=&at=&release_id=&region=&platform=。
func (s *Store) EntityPicturesQuery(ctx context.Context, id string, q PicturesQuery, u *User) (EntityPicturesResponse, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return EntityPicturesResponse{}, fmt.Errorf("invalid_id")
	}
	at := strings.TrimSpace(q.At)
	if at != "" && !validPartialISODate(at) {
		return EntityPicturesResponse{}, fmt.Errorf("invalid_date")
	}
	relFilter := strings.TrimSpace(q.ReleaseID)
	if relFilter != "" {
		if _, err := uuid.Parse(relFilter); err != nil {
			return EntityPicturesResponse{}, fmt.Errorf("invalid_id")
		}
	}
	roleSet := map[string]bool{}
	for _, part := range strings.Split(q.Role, ",") {
		if r := strings.TrimSpace(part); r != "" {
			roleSet[r] = true
		}
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return EntityPicturesResponse{}, err
	}
	defer tx.Rollback()

	defs, err := definitions(ctx, tx)
	if err != nil {
		return EntityPicturesResponse{}, err
	}
	got, err := getManyFrom(ctx, tx, []string{uid.String()}, u)
	if err != nil {
		return EntityPicturesResponse{}, err
	}
	subject, ok := got[uid.String()]
	if !ok || subject.State != "active" {
		return EntityPicturesResponse{}, sql.ErrNoRows
	}

	owners := []Entity{subject}
	if subject.Kind == "work" {
		relIDs, err := subjectReleaseIDsTx(ctx, tx, subject.ID)
		if err != nil {
			return EntityPicturesResponse{}, err
		}
		rels, err := getManyFrom(ctx, tx, relIDs, u)
		if err != nil {
			return EntityPicturesResponse{}, err
		}
		sortedRels := make([]Entity, 0, len(rels))
		for _, r := range rels {
			if r.State == "active" {
				sortedRels = append(sortedRels, r)
			}
		}
		sort.Slice(sortedRels, func(i, j int) bool {
			di, dj := attrString(sortedRels[i].Attributes, "edition_date"), attrString(sortedRels[j].Attributes, "edition_date")
			if di != dj {
				return di < dj
			}
			return sortedRels[i].ID < sortedRels[j].ID
		})
		owners = append(owners, sortedRels...)
	}

	ownerIDs := make([]string, 0, len(owners))
	for _, o := range owners {
		ownerIDs = append(ownerIDs, o.ID)
	}
	recTimes, err := loadPictureRecordedTimesTx(ctx, tx, ownerIDs)
	if err != nil {
		return EntityPicturesResponse{}, err
	}

	scope := TimelineScope{
		Region:    strings.TrimSpace(q.Region),
		Platform:  strings.TrimSpace(q.Platform),
		ReleaseID: relFilter,
	}

	items := []ScopedPictureItem{}
	candidates := []pictureCandidate{}
	for _, owner := range owners {
		ownerReg := attrString(owner.Attributes, "country")
		ownerPlat := attrString(owner.Attributes, "platform")
		ownerStart := entityKnownStartDate(owner)
		for idx, p := range owner.Pictures {
			effPic := p
			if owner.Kind == "release" {
				if effPic.ReleaseID == "" {
					effPic.ReleaseID = owner.ID
				}
				if effPic.Region == "" {
					effPic.Region = ownerReg
				}
				if effPic.Platform == "" {
					effPic.Platform = ownerPlat
				}
			}
			recAt := effPic.RecordedAt
			if recAt == nil {
				if ts, exists := recTimes[owner.ID+"|"+strings.TrimSpace(effPic.URL)]; exists {
					tCopy := ts
					recAt = &tCopy
				} else if !owner.UpdatedAt.IsZero() {
					tCopy := owner.UpdatedAt.UTC()
					recAt = &tCopy
				}
			}
			effPic.RecordedAt = recAt
			candidates = append(candidates, pictureCandidate{
				ownerID:    owner.ID,
				ownerKind:  owner.Kind,
				ownerStart: ownerStart,
				index:      idx,
				picture:    effPic,
				recordedAt: recAt,
			})

			if len(roleSet) > 0 && !roleSet[strings.TrimSpace(effPic.Role)] {
				continue
			}
			if scope.ReleaseID != "" && effPic.ReleaseID != scope.ReleaseID {
				continue
			}
			if scope.Region != "" && !matchesTokenScope(effPic.Region, scope.Region) {
				continue
			}
			if scope.Platform != "" && !matchesTokenScope(effPic.Platform, scope.Platform) {
				continue
			}
			if at != "" {
				b := pictureBounds(effPic)
				st := evalTemporalBounds(b, at)
				if st == temporalExcluded && !(effPic.Pinned && b.ValidFrom == "" && b.BeginDate == "" && b.ValidTo == "" && b.EndDate == "" && (ownerStart == "" || !pictureTimeEnd(at).Before(pictureTimeStart(ownerStart)))) {
					continue
				}
			}
			hist := isPictureHistoricalNow(effPic, idx)
			items = append(items, ScopedPictureItem{
				OwnerID:      owner.ID,
				OwnerKind:    owner.Kind,
				OwnerTitle:   owner.Title,
				Index:        idx,
				IsCurrent:    !hist,
				IsHistorical: hist,
				Picture:      effPic,
				RecordedAt:   recAt,
			})
		}
	}

	res := EntityPicturesResponse{
		EntityID:       subject.ID,
		DefinitionETag: defs.ETag,
		At:             at,
		Scope:          scope,
		Items:          items,
	}
	if at != "" {
		coverSel := selectTemporalPicture(candidates, isCoverRole, at, scope)
		iconSel := selectTemporalPicture(candidates, func(r string) bool { return strings.TrimSpace(r) == "app_icon" }, at, scope)
		res.SelectedCover = &coverSel
		res.SelectedAppIcon = &iconSel
	}
	return res, tx.Commit()
}
