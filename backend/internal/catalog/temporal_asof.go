package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// TemporalAsOfQuery 描述 GET /api/catalog/entities/:id/as-of 的查询参数。
type TemporalAsOfQuery struct {
	At        string
	Region    string
	Platform  string
	ReleaseID string
}

func entityKnownStartDate(e Entity) string {
	return firstNonEmpty(
		attrString(e.Attributes, "edition_date"),
		attrString(e.Attributes, "begin_date"),
		attrString(e.Attributes, "broadcast_start"),
		attrString(e.Attributes, "formation_date"),
		attrString(e.Attributes, "birth_date"),
	)
}

func entityKnownEndDate(e Entity) string {
	return firstNonEmpty(
		attrString(e.Attributes, "end_date"),
		attrString(e.Attributes, "broadcast_end"),
		attrString(e.Attributes, "dissolution_date"),
		attrString(e.Attributes, "death_date"),
	)
}

func canonicalLangMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	pa, _, hasSubA := strings.Cut(a, "-")
	pb, _, hasSubB := strings.Cut(b, "-")
	if pa != pb {
		return false
	}
	if !hasSubA || !hasSubB {
		if pa == "zh" {
			other := a
			if !hasSubA {
				other = b
			}
			return other == "zh-cn" || other == "zh-hans"
		}
		return true
	}
	return false
}

// reconstructEntityTitleAt 按曾用名区间与实体起始日重建 at 时点的标题。
func reconstructEntityTitleAt(e Entity, at string, scope TimelineScope) (string, string, []string) {
	rawFormer, _ := e.Attributes["former_names"].([]any)
	type scoredName struct {
		name  string
		score int
	}
	var confirmed []scoredName
	var disputedNames []string
	var latestClosedEnd time.Time
	hasUnboundedFormer := false

	for _, raw := range rawFormer {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := attrString(m, "name")
		if name == "" {
			continue
		}
		entryLang := attrString(m, "language")
		if entryLang != "" && e.OriginalLanguage != "" && !canonicalLangMatch(entryLang, e.OriginalLanguage) {
			continue
		}
		fReg := attrString(m, "region")
		fPlat := attrString(m, "platform")
		fRel := validUUIDOrEmpty(attrString(m, "scoped_release"))
		okScope, spScore := matchesScalarScope(fReg, fPlat, fRel, e, scope)
		if !okScope {
			continue
		}
		b := temporalBounds{
			ValidFrom:     attrString(m, "valid_from"),
			ValidTo:       attrString(m, "valid_to"),
			BeginDate:     attrString(m, "begin_date"),
			EndDate:       attrString(m, "end_date"),
			Ended:         attrBool(m, "ended"),
			DatePrecision: attrString(m, "date_precision"),
			EarliestDate:  attrString(m, "earliest_date"),
			LatestDate:    attrString(m, "latest_date"),
		}
		if b.ValidTo != "" {
			t := pictureTimeStart(b.ValidTo)
			if t.After(latestClosedEnd) {
				latestClosedEnd = t
			}
		} else if b.EndDate != "" {
			t := pictureTimeEnd(b.EndDate).Add(time.Nanosecond)
			if t.After(latestClosedEnd) {
				latestClosedEnd = t
			}
		} else {
			hasUnboundedFormer = true
		}
		switch evalTemporalBounds(b, at) {
		case temporalConfirmed:
			confirmed = append(confirmed, scoredName{name: name, score: spScore})
		case temporalDisputed:
			disputedNames = append(disputedNames, name)
		}
	}

	disputedNames = uniqueSortedStrings(disputedNames)
	if len(confirmed) > 0 && len(disputedNames) == 0 {
		maxScore := confirmed[0].score
		for _, c := range confirmed[1:] {
			if c.score > maxScore {
				maxScore = c.score
			}
		}
		var topNames []string
		for _, c := range confirmed {
			if c.score == maxScore {
				topNames = append(topNames, c.name)
			}
		}
		topNames = uniqueSortedStrings(topNames)
		if len(topNames) == 1 {
			return topNames[0], "confirmed", nil
		}
		return "", "disputed", topNames
	}
	if len(disputedNames) > 0 {
		var cands []string
		for _, c := range confirmed {
			cands = append(cands, c.name)
		}
		cands = append(cands, disputedNames...)
		if !contains(cands, e.Title) {
			cands = append(cands, e.Title)
		}
		return "", "disputed", uniqueSortedStrings(cands)
	}

	if hasUnboundedFormer {
		return "", "unknown", nil
	}
	if !latestClosedEnd.IsZero() {
		if !pictureTimeStart(at).Before(latestClosedEnd) {
			return e.Title, "confirmed", nil
		}
		return "", "unknown", nil
	}
	startDate := entityKnownStartDate(e)
	if startDate == "" || strings.HasPrefix(startDate, "--") {
		return "", "unknown", nil
	}
	st := evalTemporalBounds(temporalBounds{
		ValidFrom:     startDate,
		DatePrecision: attrString(e.Attributes, "date_precision"),
		EarliestDate:  attrString(e.Attributes, "earliest_date"),
		LatestDate:    attrString(e.Attributes, "latest_date"),
	}, at)
	switch st {
	case temporalConfirmed:
		return e.Title, "confirmed", nil
	case temporalDisputed:
		return "", "disputed", []string{e.Title}
	default:
		return "", "unknown", nil
	}
}

// reconstructEntityTranslationsAt 按语种曾用名区间与实体起始日重建各语种标题与译名。
func reconstructEntityTranslationsAt(e Entity, at string, scope TimelineScope) (map[string]Translation, []string) {
	rawFormer, _ := e.Attributes["former_names"].([]any)
	localeSet := map[string]string{}
	for loc := range e.Translations {
		trimmed := strings.TrimSpace(loc)
		if trimmed != "" {
			localeSet[strings.ToLower(trimmed)] = trimmed
		}
	}
	for _, raw := range rawFormer {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		lang := strings.TrimSpace(attrString(m, "language"))
		if lang == "" {
			continue
		}
		matchedKey := ""
		for _, existing := range localeSet {
			if canonicalLangMatch(lang, existing) {
				matchedKey = existing
				break
			}
		}
		if matchedKey == "" {
			localeSet[strings.ToLower(lang)] = lang
		}
	}
	if len(localeSet) == 0 {
		return nil, nil
	}

	startDate := entityKnownStartDate(e)
	startBounds := temporalBounds{
		ValidFrom:     startDate,
		DatePrecision: attrString(e.Attributes, "date_precision"),
		EarliestDate:  attrString(e.Attributes, "earliest_date"),
		LatestDate:    attrString(e.Attributes, "latest_date"),
	}

	out := map[string]Translation{}
	var disputedFields []string

	locs := make([]string, 0, len(localeSet))
	for _, canonical := range localeSet {
		locs = append(locs, canonical)
	}
	sort.Strings(locs)

	type scoredTrans struct {
		title       string
		nonofficial bool
		score       int
	}
	for _, loc := range locs {
		var confirmed []scoredTrans
		var disputed []string
		var latestClosedEnd time.Time
		hasUnbounded := false

		for _, raw := range rawFormer {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := attrString(m, "name")
			lang := attrString(m, "language")
			if name == "" || lang == "" || !canonicalLangMatch(lang, loc) {
				continue
			}
			fReg := attrString(m, "region")
			fPlat := attrString(m, "platform")
			fRel := validUUIDOrEmpty(attrString(m, "scoped_release"))
			okScope, spScore := matchesScalarScope(fReg, fPlat, fRel, e, scope)
			if !okScope {
				continue
			}
			b := temporalBounds{
				ValidFrom:     attrString(m, "valid_from"),
				ValidTo:       attrString(m, "valid_to"),
				BeginDate:     attrString(m, "begin_date"),
				EndDate:       attrString(m, "end_date"),
				Ended:         attrBool(m, "ended"),
				DatePrecision: attrString(m, "date_precision"),
				EarliestDate:  attrString(m, "earliest_date"),
				LatestDate:    attrString(m, "latest_date"),
			}
			if b.ValidTo != "" {
				if t := pictureTimeStart(b.ValidTo); t.After(latestClosedEnd) {
					latestClosedEnd = t
				}
			} else if b.EndDate != "" {
				if t := pictureTimeEnd(b.EndDate).Add(time.Nanosecond); t.After(latestClosedEnd) {
					latestClosedEnd = t
				}
			} else {
				hasUnbounded = true
			}
			switch evalTemporalBounds(b, at) {
			case temporalConfirmed:
				confirmed = append(confirmed, scoredTrans{
					title:       name,
					nonofficial: attrBool(m, "nonofficial"),
					score:       spScore,
				})
			case temporalDisputed:
				disputed = append(disputed, name)
			}
		}

		if len(confirmed) > 0 && len(disputed) == 0 {
			maxScore := confirmed[0].score
			for _, c := range confirmed[1:] {
				if c.score > maxScore {
					maxScore = c.score
				}
			}
			var top []scoredTrans
			seenTitle := map[string]bool{}
			for _, c := range confirmed {
				if c.score == maxScore && !seenTitle[c.title] {
					seenTitle[c.title] = true
					top = append(top, c)
				}
			}
			if len(top) == 1 {
				out[loc] = Translation{Title: top[0].title, Nonofficial: top[0].nonofficial}
			} else {
				disputedFields = append(disputedFields, "translations."+loc)
			}
			continue
		}
		if len(disputed) > 0 {
			disputedFields = append(disputedFields, "translations."+loc)
			continue
		}

		curTr, hasCur := e.Translations[loc]
		if !hasCur || strings.TrimSpace(curTr.Title) == "" {
			continue
		}
		if hasUnbounded {
			continue
		}
		if !latestClosedEnd.IsZero() {
			if !pictureTimeStart(at).Before(latestClosedEnd) {
				out[loc] = curTr
			}
			continue
		}
		if startDate == "" || strings.HasPrefix(startDate, "--") {
			continue
		}
		switch evalTemporalBounds(startBounds, at) {
		case temporalConfirmed:
			out[loc] = curTr
		case temporalDisputed:
			disputedFields = append(disputedFields, "translations."+loc)
		}
	}

	if len(out) == 0 {
		return nil, uniqueSortedStrings(disputedFields)
	}
	return out, uniqueSortedStrings(disputedFields)
}

var intrinsicIdentityFields = map[string]bool{
	"creation_form":   true,
	"agent_type":      true,
	"collection_type": true,
	"event_type":      true,
	"medium_format":   true,
}

var temporalEndDateFields = map[string]bool{
	"end_date":         true,
	"broadcast_end":    true,
	"dissolution_date": true,
	"death_date":       true,
}

// reconstructAttributesAt 统一按 attribute_history 与实体起止日期重建全种类动态属性（含 release_status）。
func reconstructAttributesAt(defs Definitions, e Entity, at string, scope TimelineScope) (map[string]any, map[string]string, []string, []string, []string) {
	out := map[string]any{}
	states := map[string]string{}
	var unknown []string
	var disputed []string
	var entityRefIDs []string

	recordStateGap := func(code, state string) {
		states[code] = state
		if e.Kind == "release" && code == "release_status" {
			return
		}
		if state == "disputed" {
			disputed = append(disputed, "attributes."+code)
			unknown = append(unknown, "attributes."+code)
		} else {
			unknown = append(unknown, "attributes."+code)
		}
	}

	collectRefs := func(code string, val any) {
		fd, ok := defs.Fields[code]
		if !ok {
			return
		}
		switch fd.Type {
		case "entity":
			if s, ok := val.(string); ok {
				if id := validUUIDOrEmpty(s); id != "" {
					entityRefIDs = append(entityRefIDs, id)
				}
			}
		case "list":
			if fd.Items != nil && fd.Items.Type == "entity" {
				if arr, ok := val.([]any); ok {
					for _, item := range arr {
						if s, ok := item.(string); ok {
							if id := validUUIDOrEmpty(s); id != "" {
								entityRefIDs = append(entityRefIDs, id)
							}
						}
					}
				}
			}
		}
	}

	start := entityKnownStartDate(e)

	type histEntry struct {
		val    any
		score  int
		bounds temporalBounds
	}
	histByField := map[string][]histEntry{}
	if rawHist, ok := e.Attributes["attribute_history"].([]any); ok {
		for _, raw := range rawHist {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			code := strings.TrimSpace(attrString(m, "field"))
			if code == "" || nonHistoryMetaFields[code] || code == "publisher" || code == "operator" {
				continue
			}
			targetField, exists := defs.Fields[code]
			if !exists {
				continue
			}
			fReg := attrString(m, "region")
			fPlat := attrString(m, "platform")
			fRel := validUUIDOrEmpty(attrString(m, "scoped_release"))
			okScope, spScore := matchesScalarScope(fReg, fPlat, fRel, e, scope)
			if !okScope {
				continue
			}
			parsedVal, err := parseAttributeHistoryValue(targetField, attrString(m, "value"), attrString(m, "entity_value"))
			if err != nil {
				continue
			}
			b := temporalBounds{
				ValidFrom:     attrString(m, "valid_from"),
				ValidTo:       attrString(m, "valid_to"),
				BeginDate:     attrString(m, "begin_date"),
				EndDate:       attrString(m, "end_date"),
				Ended:         attrBool(m, "ended"),
				DatePrecision: attrString(m, "date_precision"),
				EarliestDate:  attrString(m, "earliest_date"),
				LatestDate:    attrString(m, "latest_date"),
			}
			histByField[code] = append(histByField[code], histEntry{val: parsedVal, score: spScore, bounds: b})
		}
	}

	for code, entries := range histByField {
		type scoredVal struct {
			val   any
			score int
		}
		var confirmed []scoredVal
		hasDisputed := false
		var latestClosedEnd time.Time
		hasUnbounded := false
		for _, ent := range entries {
			if ent.bounds.ValidTo != "" {
				if t := pictureTimeStart(ent.bounds.ValidTo); t.After(latestClosedEnd) {
					latestClosedEnd = t
				}
			} else if ent.bounds.EndDate != "" {
				if t := pictureTimeEnd(ent.bounds.EndDate).Add(time.Nanosecond); t.After(latestClosedEnd) {
					latestClosedEnd = t
				}
			} else {
				hasUnbounded = true
			}
			switch evalTemporalBounds(ent.bounds, at) {
			case temporalConfirmed:
				confirmed = append(confirmed, scoredVal{val: ent.val, score: ent.score})
			case temporalDisputed:
				hasDisputed = true
			}
		}
		if len(confirmed) > 0 && !hasDisputed {
			maxScore := confirmed[0].score
			for _, c := range confirmed[1:] {
				if c.score > maxScore {
					maxScore = c.score
				}
			}
			var uniqueVals []any
			seenEnc := map[string]bool{}
			for _, c := range confirmed {
				if c.score != maxScore {
					continue
				}
				enc := encode(c.val)
				if !seenEnc[enc] {
					seenEnc[enc] = true
					uniqueVals = append(uniqueVals, c.val)
				}
			}
			if len(uniqueVals) == 1 {
				out[code] = uniqueVals[0]
				states[code] = "confirmed"
				collectRefs(code, uniqueVals[0])
			} else {
				recordStateGap(code, "disputed")
			}
			continue
		}
		if hasDisputed {
			recordStateGap(code, "disputed")
			continue
		}
		if curVal, hasCur := e.Attributes[code]; hasCur && !isEmptyValue(curVal) {
			afterKnownStart := start == "" || strings.HasPrefix(start, "--") || !pictureTimeEnd(at).Before(pictureTimeStart(start))
			canFallbackToCur := !hasUnbounded && !latestClosedEnd.IsZero() && !pictureTimeStart(at).Before(latestClosedEnd) && afterKnownStart
			if code == "release_status" {
				curStatus := attrString(e.Attributes, "release_status")
				canFallbackToCur = canFallbackToCur && (curStatus == "released" || curStatus == "operating")
			}
			if canFallbackToCur {
				out[code] = curVal
				states[code] = "confirmed"
				collectRefs(code, curVal)
				continue
			}
		}
		recordStateGap(code, "unknown")
	}

	startState := temporalExcluded
	if start != "" && !strings.HasPrefix(start, "--") {
		startState = evalTemporalBounds(temporalBounds{
			ValidFrom:     start,
			DatePrecision: attrString(e.Attributes, "date_precision"),
			EarliestDate:  attrString(e.Attributes, "earliest_date"),
			LatestDate:    attrString(e.Attributes, "latest_date"),
		}, at)
		if startState == temporalExcluded {
			return out, states, uniqueSortedStrings(unknown), uniqueSortedStrings(disputed), uniqueSortedStrings(entityRefIDs)
		}
	}

	knownEnd := entityKnownEndDate(e)
	keys := make([]string, 0, len(e.Attributes))
	for k := range e.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, handled := histByField[k]; handled {
			continue
		}
		if nonHistoryMetaFields[k] || k == "publisher" || k == "operator" {
			continue
		}
		v := e.Attributes[k]
		if isEmptyValue(v) {
			continue
		}
		if k == "release_status" {
			curStatus := attrString(e.Attributes, "release_status")
			if startState == temporalConfirmed && (curStatus == "released" || curStatus == "operating") {
				out[k] = curStatus
				states[k] = "confirmed"
			} else if startState == temporalDisputed && (curStatus == "released" || curStatus == "operating") {
				recordStateGap(k, "disputed")
			} else {
				recordStateGap(k, "unknown")
			}
			continue
		}
		if start == "" || strings.HasPrefix(start, "--") {
			if intrinsicIdentityFields[k] {
				out[k] = v
				states[k] = "confirmed"
				collectRefs(k, v)
			} else {
				recordStateGap(k, "unknown")
			}
			continue
		}
		if startState == temporalDisputed {
			if intrinsicIdentityFields[k] {
				out[k] = v
				states[k] = "confirmed"
			} else {
				recordStateGap(k, "disputed")
			}
			continue
		}
		if temporalEndDateFields[k] {
			endStr, _ := v.(string)
			endStr = strings.TrimSpace(endStr)
			if endStr == "" || strings.HasPrefix(endStr, "--") {
				continue
			}
			switch evalTemporalBounds(temporalBounds{ValidFrom: endStr}, at) {
			case temporalConfirmed:
				out[k] = v
				states[k] = "confirmed"
			case temporalDisputed:
				recordStateGap(k, "disputed")
			}
			continue
		}
		if k == "ended" {
			bVal, _ := v.(bool)
			if !bVal {
				out[k] = false
				states[k] = "confirmed"
				continue
			}
			if knownEnd != "" && !strings.HasPrefix(knownEnd, "--") {
				switch evalTemporalBounds(temporalBounds{ValidFrom: knownEnd}, at) {
				case temporalConfirmed:
					out[k] = true
					states[k] = "confirmed"
				case temporalExcluded:
					out[k] = false
					states[k] = "confirmed"
				case temporalDisputed:
					recordStateGap("ended", "disputed")
				}
			} else {
				recordStateGap("ended", "disputed")
			}
			continue
		}
		out[k] = v
		states[k] = "confirmed"
		collectRefs(k, v)
	}
	return out, states, uniqueSortedStrings(unknown), uniqueSortedStrings(disputed), uniqueSortedStrings(entityRefIDs)
}

func reconstructExternalLinksAt(e Entity, at string, scope TimelineScope, recTimes map[string]time.Time) ([]ExternalLink, []string, []string) {
	if len(e.ExternalLinks) == 0 {
		return []ExternalLink{}, nil, nil
	}
	ownerStart := entityKnownStartDate(e)
	ownerReg := attrString(e.Attributes, "country")
	ownerPlat := attrString(e.Attributes, "platform")
	ownerChan := attrString(e.Attributes, "channel")

	active := []ExternalLink{}
	hasUnknown := false
	hasDisputed := false

	for _, raw := range e.ExternalLinks {
		eff := raw
		if e.Kind == "release" {
			if eff.ReleaseID == "" {
				eff.ReleaseID = e.ID
			}
			if eff.Region == "" {
				eff.Region = ownerReg
			}
			if eff.Platform == "" {
				eff.Platform = ownerPlat
			}
			if eff.Channel == "" {
				eff.Channel = ownerChan
			}
		}
		if scope.ReleaseID != "" && eff.ReleaseID != "" && eff.ReleaseID != scope.ReleaseID {
			continue
		}
		if okS, _ := scoreFactScope(eff.Region, eff.Platform, eff.ReleaseID, scope); !okS {
			continue
		}
		if eff.RecordedAt == nil {
			key := e.ID + "|" + strings.TrimSpace(eff.Kind) + "|" + strings.TrimSpace(eff.URL)
			if ts, exists := recTimes[key]; exists {
				tCopy := ts
				eff.RecordedAt = &tCopy
			} else if !e.UpdatedAt.IsZero() {
				tCopy := e.UpdatedAt.UTC()
				eff.RecordedAt = &tCopy
			}
		}
		b := temporalBounds{
			ValidFrom:     strings.TrimSpace(eff.ValidFrom),
			ValidTo:       strings.TrimSpace(eff.ValidTo),
			Ended:         eff.Ended,
			DatePrecision: strings.TrimSpace(eff.DatePrecision),
			EarliestDate:  strings.TrimSpace(eff.EarliestDate),
			LatestDate:    strings.TrimSpace(eff.LatestDate),
		}
		hasExplicit := b.ValidFrom != "" || b.ValidTo != "" || b.EarliestDate != "" || b.LatestDate != "" || b.Ended
		if hasExplicit {
			if b.ValidFrom == "" && b.EarliestDate == "" && b.ValidTo != "" && ownerStart != "" && !strings.HasPrefix(ownerStart, "--") {
				b.ValidFrom = ownerStart
			}
			switch evalTemporalBounds(b, at) {
			case temporalConfirmed:
				active = append(active, eff)
			case temporalDisputed:
				hasDisputed = true
			}
			continue
		}
		if ownerStart == "" || strings.HasPrefix(ownerStart, "--") {
			hasUnknown = true
			continue
		}
		switch evalTemporalBounds(temporalBounds{
			ValidFrom:     ownerStart,
			DatePrecision: attrString(e.Attributes, "date_precision"),
			EarliestDate:  attrString(e.Attributes, "earliest_date"),
			LatestDate:    attrString(e.Attributes, "latest_date"),
		}, at) {
		case temporalConfirmed:
			active = append(active, eff)
		case temporalDisputed:
			hasDisputed = true
		}
	}

	var unknown, disputed []string
	if hasDisputed {
		disputed = append(disputed, "external_links")
		unknown = append(unknown, "external_links")
	} else if hasUnknown && len(active) == 0 {
		unknown = append(unknown, "external_links")
	}
	return active, unknown, disputed
}

// EntityAsOf 实现 GET /api/catalog/entities/:id/as-of?at=&region=&platform=&release_id=。
func (s *Store) EntityAsOf(ctx context.Context, id string, q TemporalAsOfQuery, u *User) (EntityAsOfResponse, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return EntityAsOfResponse{}, fmt.Errorf("invalid_id")
	}
	at := strings.TrimSpace(q.At)
	if at == "" {
		return EntityAsOfResponse{}, fmt.Errorf("at_required")
	}
	if !validPartialISODate(at) {
		return EntityAsOfResponse{}, fmt.Errorf("invalid_date")
	}
	relFilter := strings.TrimSpace(q.ReleaseID)
	if relFilter != "" {
		if _, err := uuid.Parse(relFilter); err != nil {
			return EntityAsOfResponse{}, fmt.Errorf("invalid_id")
		}
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	defer tx.Rollback()

	res, err := s.entityAsOfTx(ctx, tx, uid.String(), q, u)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	return res, tx.Commit()
}

func (s *Store) entityAsOfTx(ctx context.Context, tx *sql.Tx, entityID string, q TemporalAsOfQuery, u *User) (EntityAsOfResponse, error) {
	defs, err := definitions(ctx, tx)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	got, err := getManyFrom(ctx, tx, []string{entityID}, u)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	subject, ok := got[entityID]
	if !ok || subject.State != "active" {
		return EntityAsOfResponse{}, sql.ErrNoRows
	}

	at := strings.TrimSpace(q.At)
	scope := TimelineScope{
		Region:    strings.TrimSpace(q.Region),
		Platform:  strings.TrimSpace(q.Platform),
		ReleaseID: strings.TrimSpace(q.ReleaseID),
	}

	var releases []Entity
	if subject.Kind == "work" {
		relIDs, err := subjectReleaseIDsTx(ctx, tx, subject.ID)
		if err != nil {
			return EntityAsOfResponse{}, err
		}
		relMap, err := getManyFrom(ctx, tx, relIDs, u)
		if err != nil {
			return EntityAsOfResponse{}, err
		}
		for _, r := range relMap {
			if r.State != "active" {
				continue
			}
			if scope.ReleaseID != "" && r.ID != scope.ReleaseID {
				continue
			}
			if scope.Region != "" && !matchesTokenScope(attrString(r.Attributes, "country"), scope.Region) {
				continue
			}
			if scope.Platform != "" && !matchesTokenScope(attrString(r.Attributes, "platform"), scope.Platform) {
				continue
			}
			releases = append(releases, r)
		}
		sort.Slice(releases, func(i, j int) bool {
			di, dj := attrString(releases[i].Attributes, "edition_date"), attrString(releases[j].Attributes, "edition_date")
			if di != dj {
				return di < dj
			}
			return releases[i].ID < releases[j].ID
		})
	} else if subject.Kind == "release" {
		releases = []Entity{subject}
	}

	picOwnerIDs := []string{subject.ID}
	for _, r := range releases {
		picOwnerIDs = append(picOwnerIDs, r.ID)
	}
	picRecTimes, err := loadPictureRecordedTimesTx(ctx, tx, picOwnerIDs)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	linkRecTimes, err := loadExternalLinkRecordedTimesTx(ctx, tx, picOwnerIDs)
	if err != nil {
		return EntityAsOfResponse{}, err
	}

	relOwnerIDs := make([]string, 0, len(releases))
	for _, r := range releases {
		relOwnerIDs = append(relOwnerIDs, r.ID)
	}
	releaseRelations := map[string][]Relation{}
	allRelRowIDs := []string{}
	agentIDs := []string{}
	for _, r := range releases {
		if p := validUUIDOrEmpty(attrString(r.Attributes, "publisher")); p != "" {
			agentIDs = append(agentIDs, p)
		}
		if op := validUUIDOrEmpty(attrString(r.Attributes, "operator")); op != "" {
			agentIDs = append(agentIDs, op)
		}
	}
	if len(relOwnerIDs) > 0 {
		rRows, err := tx.QueryContext(ctx,
			"SELECT document FROM catalog.relations WHERE source_id = ANY($1::uuid[]) AND type IN ('published_by', 'operated_by') ORDER BY coalesce((document->>'position')::int, 0) ASC, id ASC",
			pq.Array(uniqueSortedUUIDs(relOwnerIDs)))
		if err != nil {
			return EntityAsOfResponse{}, err
		}
		for rRows.Next() {
			var raw []byte
			if err := rRows.Scan(&raw); err != nil {
				rRows.Close()
				return EntityAsOfResponse{}, err
			}
			var rel Relation
			if err := json.Unmarshal(raw, &rel); err != nil {
				rRows.Close()
				return EntityAsOfResponse{}, err
			}
			releaseRelations[rel.SourceID] = append(releaseRelations[rel.SourceID], rel)
			allRelRowIDs = append(allRelRowIDs, rel.ID)
			agentIDs = append(agentIDs, rel.TargetID)
		}
		if err := rRows.Err(); err != nil {
			rRows.Close()
			return EntityAsOfResponse{}, err
		}
		rRows.Close()
	}

	relRecTimes, err := loadRelationRecordedTimesTx(ctx, tx, allRelRowIDs)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	visAgents, err := relationshipEntitiesFrom(ctx, tx, uniqueSortedUUIDs(agentIDs), u)
	if err != nil {
		return EntityAsOfResponse{}, err
	}

	buildPicCandidates := func(owners ...Entity) []pictureCandidate {
		out := []pictureCandidate{}
		for _, owner := range owners {
			ownerStart := entityKnownStartDate(owner)
			ownerReg := attrString(owner.Attributes, "country")
			ownerPlat := attrString(owner.Attributes, "platform")
			for idx, p := range owner.Pictures {
				eff := p
				if owner.Kind == "release" {
					if eff.ReleaseID == "" {
						eff.ReleaseID = owner.ID
					}
					if eff.Region == "" {
						eff.Region = ownerReg
					}
					if eff.Platform == "" {
						eff.Platform = ownerPlat
					}
				}
				recAt := eff.RecordedAt
				if recAt == nil {
					if ts, exists := picRecTimes[owner.ID+"|"+strings.TrimSpace(eff.URL)]; exists {
						tCopy := ts
						recAt = &tCopy
					} else if !owner.UpdatedAt.IsZero() {
						tCopy := owner.UpdatedAt.UTC()
						recAt = &tCopy
					}
				}
				eff.RecordedAt = recAt
				out = append(out, pictureCandidate{
					ownerID:    owner.ID,
					ownerKind:  owner.Kind,
					ownerStart: ownerStart,
					index:      idx,
					picture:    eff,
					recordedAt: recAt,
				})
			}
		}
		return out
	}

	buildReleaseView := func(rel Entity) TemporalReleaseView {
		rScope := TimelineScope{Region: scope.Region, Platform: scope.Platform, ReleaseID: rel.ID}
		title, titleStatus, disputedTitles := reconstructEntityTitleAt(rel, at, rScope)
		relTrans, relTransDisputed := reconstructEntityTranslationsAt(rel, at, rScope)
		unknown := []string{}
		disputed := append([]string{}, relTransDisputed...)
		unknown = append(unknown, relTransDisputed...)
		if titleStatus == "unknown" {
			unknown = append(unknown, "title")
		} else if titleStatus == "disputed" {
			disputed = append(disputed, "title")
			unknown = append(unknown, "title")
		}

		edDate := attrString(rel.Attributes, "edition_date")
		releaseStarted := temporalExcluded
		if edDate != "" {
			releaseStarted = evalTemporalBounds(temporalBounds{ValidFrom: edDate}, at)
		}

		evalRoleAgents := func(relType, attrKey, fieldName string) ([]TemporalAgentFact, string) {
			rels := releaseRelations[rel.ID]
			hasTypedRels := false
			confirmed := []TemporalAgentFact{}
			disputedFacts := []TemporalAgentFact{}
			for _, edge := range rels {
				if edge.Type != relType {
					continue
				}
				agSummary, vis := visAgents[edge.TargetID]
				if !vis {
					continue
				}
				hasTypedRels = true
				eReg := firstNonEmpty(attrString(edge.Attributes, "region"), attrString(rel.Attributes, "country"))
				ePlat := firstNonEmpty(attrString(edge.Attributes, "platform"), attrString(rel.Attributes, "platform"))
				eChan := firstNonEmpty(attrString(edge.Attributes, "channel"), attrString(rel.Attributes, "channel"))
				if okS, _ := scoreFactScope(eReg, ePlat, rel.ID, scope); !okS {
					continue
				}
				b := temporalBounds{
					ValidFrom:     attrString(edge.Attributes, "valid_from"),
					ValidTo:       attrString(edge.Attributes, "valid_to"),
					BeginDate:     attrString(edge.Attributes, "begin_date"),
					EndDate:       attrString(edge.Attributes, "end_date"),
					Ended:         attrBool(edge.Attributes, "ended"),
					DatePrecision: attrString(edge.Attributes, "date_precision"),
					EarliestDate:  attrString(edge.Attributes, "earliest_date"),
					LatestDate:    attrString(edge.Attributes, "latest_date"),
				}
				if b.ValidFrom == "" && b.BeginDate == "" && b.EarliestDate == "" && edDate != "" && !b.Ended && b.ValidTo == "" && b.EndDate == "" {
					b.ValidFrom = edDate
				}
				var recPtr *time.Time
				if ts, exists := relRecTimes[edge.ID]; exists {
					tCopy := ts
					recPtr = &tCopy
				}
				fact := TemporalAgentFact{
					AgentID:       edge.TargetID,
					Title:         agSummary.Title,
					RelationID:    edge.ID,
					SourceOrigin:  "relation",
					Role:          attrString(edge.Attributes, "role"),
					CreditRole:    attrString(edge.Attributes, "credit_role"),
					Region:        eReg,
					Platform:      ePlat,
					Channel:       eChan,
					ValidFrom:     firstNonEmpty(b.ValidFrom, b.BeginDate),
					ValidTo:       firstNonEmpty(b.ValidTo, b.EndDate),
					DatePrecision: b.DatePrecision,
					RecordedAt:    recPtr,
				}
				switch evalTemporalBounds(b, at) {
				case temporalConfirmed:
					confirmed = append(confirmed, fact)
				case temporalDisputed:
					disputedFacts = append(disputedFacts, fact)
				}
			}
			if len(confirmed) > 0 && len(disputedFacts) == 0 {
				return confirmed, "confirmed"
			}
			if len(disputedFacts) > 0 {
				disputed = append(disputed, fieldName)
				unknown = append(unknown, fieldName)
				return append(confirmed, disputedFacts...), "disputed"
			}
			if !hasTypedRels {
				if aid := validUUIDOrEmpty(attrString(rel.Attributes, attrKey)); aid != "" {
					if agSummary, vis := visAgents[aid]; vis {
						if releaseStarted == temporalConfirmed {
							var recPtr *time.Time
							if !rel.UpdatedAt.IsZero() {
								tCopy := rel.UpdatedAt.UTC()
								recPtr = &tCopy
							}
							return []TemporalAgentFact{{
								AgentID:      aid,
								Title:        agSummary.Title,
								SourceOrigin: "attribute",
								Region:       attrString(rel.Attributes, "country"),
								Platform:     attrString(rel.Attributes, "platform"),
								Channel:      attrString(rel.Attributes, "channel"),
								ValidFrom:    edDate,
								RecordedAt:   recPtr,
							}}, "confirmed"
						}
						if releaseStarted == temporalDisputed {
							disputed = append(disputed, fieldName)
						}
					}
				}
			}
			unknown = append(unknown, fieldName)
			return []TemporalAgentFact{}, "unknown"
		}

		pubs, pubState := evalRoleAgents("published_by", "publisher", "publishers")
		ops, opState := evalRoleAgents("operated_by", "operator", "operators")

		picCands := buildPicCandidates(rel)
		if subject.Kind == "work" {
			for _, pc := range buildPicCandidates(subject) {
				if pc.picture.ReleaseID == rel.ID {
					picCands = append(picCands, pc)
				}
			}
		}
		coverSel := selectTemporalPicture(picCands, isCoverRole, at, rScope)
		iconSel := selectTemporalPicture(picCands, func(r string) bool { return strings.TrimSpace(r) == "app_icon" }, at, rScope)
		if coverSel.Status == "unknown" {
			unknown = append(unknown, "cover")
		}
		if iconSel.Status == "unknown" {
			unknown = append(unknown, "app_icon")
		}

		relAttrs, relAttrStates, relAttrUnknown, relAttrDisputed, relAttrRefs := reconstructAttributesAt(defs.Document, rel, at, rScope)
		relStatus, _ := relAttrs["release_status"].(string)
		statusState := relAttrStates["release_status"]
		if statusState == "" {
			statusState = "unknown"
		}
		if statusState == "unknown" {
			unknown = append(unknown, "release_status")
		} else if statusState == "disputed" {
			disputed = append(disputed, "release_status")
			unknown = append(unknown, "release_status")
		}
		unknown = append(unknown, relAttrUnknown...)
		disputed = append(disputed, relAttrDisputed...)
		agentIDs = append(agentIDs, relAttrRefs...)

		relLinks, relLinkUnknown, relLinkDisputed := reconstructExternalLinksAt(rel, at, rScope, linkRecTimes)
		unknown = append(unknown, relLinkUnknown...)
		disputed = append(disputed, relLinkDisputed...)

		var recPtr *time.Time
		if !rel.UpdatedAt.IsZero() {
			tCopy := rel.UpdatedAt.UTC()
			recPtr = &tCopy
		}
		return TemporalReleaseView{
			ReleaseID:       rel.ID,
			Title:           title,
			TitleStatus:     titleStatus,
			DisputedTitles:  disputedTitles,
			Translations:    relTrans,
			Attributes:      relAttrs,
			AttributeStates: relAttrStates,
			ExternalLinks:   relLinks,
			EditionDate:     edDate,
			Region:          attrString(rel.Attributes, "country"),
			Platform:        attrString(rel.Attributes, "platform"),
			Distribution:    attrString(rel.Attributes, "distribution_channel"),
			Channel:         attrString(rel.Attributes, "channel"),
			ReleaseStatus:   relStatus,
			StatusState:     statusState,
			Publishers:      pubs,
			PublisherState:  pubState,
			Operators:       ops,
			OperatorState:   opState,
			Cover:           coverSel,
			AppIcon:         iconSel,
			UnknownFields:   uniqueSortedStrings(unknown),
			DisputedFields:  uniqueSortedStrings(disputed),
			RecordedAt:      recPtr,
		}
	}

	title, titleStatus, disputedTitles := reconstructEntityTitleAt(subject, at, scope)
	subjectTrans, subjectTransDisputed := reconstructEntityTranslationsAt(subject, at, scope)
	topUnknown := append([]string{}, subjectTransDisputed...)
	topDisputed := append([]string{}, subjectTransDisputed...)
	if titleStatus == "unknown" {
		topUnknown = append(topUnknown, "title")
	} else if titleStatus == "disputed" {
		topDisputed = append(topDisputed, "title")
		topUnknown = append(topUnknown, "title")
	}

	relViews := make([]TemporalReleaseView, 0, len(releases))
	for _, r := range releases {
		relViews = append(relViews, buildReleaseView(r))
	}

	allOwners := []Entity{subject}
	allOwners = append(allOwners, releases...)
	allPicCands := buildPicCandidates(allOwners...)
	subjectPicCands := buildPicCandidates(subject)
	if scope.ReleaseID != "" {
		subjectPicCands = allPicCands
	}
	coverSel := selectTemporalPicture(subjectPicCands, isCoverRole, at, scope)
	if coverSel.Status == "unknown" && len(releases) == 1 && relViews[0].Cover.Status != "unknown" {
		coverSel = relViews[0].Cover
	}
	iconSel := selectTemporalPicture(allPicCands, func(r string) bool { return strings.TrimSpace(r) == "app_icon" }, at, scope)
	if coverSel.Status == "unknown" {
		topUnknown = append(topUnknown, "cover")
	}
	if iconSel.Status == "unknown" {
		topUnknown = append(topUnknown, "app_icon")
	}

	validPics := []Picture{}
	for _, pc := range allPicCands {
		if okS, _ := scoreFactScope(pc.picture.Region, pc.picture.Platform, pc.picture.ReleaseID, scope); !okS {
			continue
		}
		if evalTemporalBounds(pictureBounds(pc.picture), at) == temporalConfirmed {
			validPics = append(validPics, pc.picture)
		}
	}

	topPubs := []TemporalAgentFact{}
	topPubState := "unknown"
	topOps := []TemporalAgentFact{}
	topOpState := "unknown"
	topStatus := ""
	topStatusState := "unknown"
	if subject.Kind == "release" && len(relViews) == 1 {
		rv := relViews[0]
		topPubs, topPubState = rv.Publishers, rv.PublisherState
		topOps, topOpState = rv.Operators, rv.OperatorState
		topStatus, topStatusState = rv.ReleaseStatus, rv.StatusState
	} else if subject.Kind == "work" {
		seenPub, seenOp := map[string]bool{}, map[string]bool{}
		for _, rv := range relViews {
			for _, p := range rv.Publishers {
				k := p.AgentID + "|" + p.Region + "|" + p.Platform + "|" + p.ValidFrom
				if !seenPub[k] {
					seenPub[k] = true
					topPubs = append(topPubs, p)
				}
			}
			for _, op := range rv.Operators {
				k := op.AgentID + "|" + op.Region + "|" + op.Platform + "|" + op.ValidFrom
				if !seenOp[k] {
					seenOp[k] = true
					topOps = append(topOps, op)
				}
			}
		}
		if len(topPubs) > 0 {
			topPubState = "confirmed"
		} else {
			topUnknown = append(topUnknown, "publishers")
		}
		if len(topOps) > 0 {
			topOpState = "confirmed"
		} else {
			topUnknown = append(topUnknown, "operators")
		}
		if len(relViews) == 1 {
			topStatus = relViews[0].ReleaseStatus
			topStatusState = relViews[0].StatusState
			if topStatusState != "confirmed" {
				topUnknown = append(topUnknown, "release_status")
			}
		}
	}
	if subject.Kind == "release" {
		if topPubState != "confirmed" {
			topUnknown = append(topUnknown, "publishers")
		}
		if topOpState != "confirmed" {
			topUnknown = append(topUnknown, "operators")
		}
		if topStatusState != "confirmed" {
			topUnknown = append(topUnknown, "release_status")
		}
		if topStatusState == "disputed" {
			topDisputed = append(topDisputed, "release_status")
		}
	}

	allSemanticLinks := []EntityLink{}
	for offset := 0; len(allSemanticLinks) < 1000; {
		linksPage, lErr := entityLinksFrom(ctx, tx, subject.ID, 200, offset, u, defs, relationshipFilters{
			Direction:   "both",
			Classes:     []string{"semantic"},
			SubjectKind: subject.Kind,
		})
		if lErr != nil {
			return EntityAsOfResponse{}, lErr
		}
		allSemanticLinks = append(allSemanticLinks, linksPage.Items...)
		if !linksPage.HasMore || len(linksPage.Items) == 0 {
			break
		}
		offset += len(linksPage.Items)
	}

	peerEndpointIDs := []string{subject.ID}
	for _, l := range allSemanticLinks {
		peerEndpointIDs = append(peerEndpointIDs, l.SourceID, l.TargetID)
	}
	peerEntities, err := getManyFrom(ctx, tx, uniqueSortedUUIDs(peerEndpointIDs), u)
	if err != nil {
		return EntityAsOfResponse{}, err
	}
	peerEntities[subject.ID] = subject

	activeRelations := []EntityLink{}
	for _, link := range allSemanticLinks {
		lReg := attrString(link.Attributes, "region")
		lPlat := attrString(link.Attributes, "platform")
		lRel := validUUIDOrEmpty(attrString(link.Attributes, "scoped_release"))
		if scope.ReleaseID != "" && lRel != "" && lRel != scope.ReleaseID {
			continue
		}
		if okS, _ := scoreFactScope(lReg, lPlat, lRel, scope); !okS {
			continue
		}
		b := temporalBounds{
			ValidFrom:     attrString(link.Attributes, "valid_from"),
			ValidTo:       attrString(link.Attributes, "valid_to"),
			BeginDate:     attrString(link.Attributes, "begin_date"),
			EndDate:       attrString(link.Attributes, "end_date"),
			Ended:         attrBool(link.Attributes, "ended"),
			DatePrecision: attrString(link.Attributes, "date_precision"),
			EarliestDate:  attrString(link.Attributes, "earliest_date"),
			LatestDate:    attrString(link.Attributes, "latest_date"),
		}
		srcEnt := peerEntities[link.SourceID]
		tgtEnt := peerEntities[link.TargetID]
		hasExplicit := b.ValidFrom != "" || b.BeginDate != "" || b.ValidTo != "" || b.EndDate != "" || b.EarliestDate != "" || b.LatestDate != "" || b.Ended
		if hasExplicit {
			if b.ValidFrom == "" && b.BeginDate == "" && b.EarliestDate == "" && (b.ValidTo != "" || b.EndDate != "") {
				srcStart := entityKnownStartDate(srcEnt)
				tgtStart := entityKnownStartDate(tgtEnt)
				anchor := srcStart
				if tgtStart != "" && (anchor == "" || pictureTimeStart(tgtStart).After(pictureTimeStart(anchor))) {
					anchor = tgtStart
				}
				if anchor != "" && !strings.HasPrefix(anchor, "--") {
					b.ValidFrom = anchor
				}
			}
			switch evalTemporalBounds(b, at) {
			case temporalConfirmed:
				activeRelations = append(activeRelations, link)
			case temporalDisputed:
				topDisputed = append(topDisputed, "relations."+link.RuleCode)
				topUnknown = append(topUnknown, "relations."+link.RuleCode)
			}
			continue
		}
		srcStart := entityKnownStartDate(srcEnt)
		tgtStart := entityKnownStartDate(tgtEnt)
		if srcStart == "" && tgtStart == "" {
			topUnknown = append(topUnknown, "relations."+link.RuleCode)
			continue
		}
		anchorState := temporalConfirmed
		for _, stDate := range []string{srcStart, tgtStart} {
			if stDate == "" || strings.HasPrefix(stDate, "--") {
				continue
			}
			ev := evalTemporalBounds(temporalBounds{ValidFrom: stDate}, at)
			if ev == temporalExcluded {
				anchorState = temporalExcluded
				break
			}
			if ev == temporalDisputed {
				anchorState = temporalDisputed
			}
		}
		if anchorState == temporalConfirmed {
			activeRelations = append(activeRelations, link)
		} else if anchorState == temporalDisputed {
			topDisputed = append(topDisputed, "relations."+link.RuleCode)
			topUnknown = append(topUnknown, "relations."+link.RuleCode)
		}
	}

	reconstructedAttrs, attrStates, attrUnknown, attrDisputed, attrEntityRefs := reconstructAttributesAt(defs.Document, subject, at, scope)
	topUnknown = append(topUnknown, attrUnknown...)
	topDisputed = append(topDisputed, attrDisputed...)

	subjectLinks, linkUnknown, linkDisputed := reconstructExternalLinksAt(subject, at, scope, linkRecTimes)
	if scope.ReleaseID != "" && subject.Kind == "work" {
		seenLnk := map[string]bool{}
		for _, l := range subjectLinks {
			seenLnk[l.Kind+"|"+l.URL] = true
		}
		for _, rv := range relViews {
			if rv.ReleaseID != scope.ReleaseID {
				continue
			}
			for _, l := range rv.ExternalLinks {
				if k := l.Kind + "|" + l.URL; !seenLnk[k] {
					seenLnk[k] = true
					subjectLinks = append(subjectLinks, l)
				}
			}
		}
	}
	topUnknown = append(topUnknown, linkUnknown...)
	topDisputed = append(topDisputed, linkDisputed...)

	summaryIDs := []string{subject.ID}
	summaryIDs = append(summaryIDs, agentIDs...)
	summaryIDs = append(summaryIDs, attrEntityRefs...)
	for _, r := range releases {
		summaryIDs = append(summaryIDs, r.ID)
	}
	for _, p := range topPubs {
		summaryIDs = append(summaryIDs, p.AgentID)
	}
	for _, op := range topOps {
		summaryIDs = append(summaryIDs, op.AgentID)
	}
	for _, link := range activeRelations {
		summaryIDs = append(summaryIDs, link.SourceID, link.TargetID)
		for _, ref := range link.References {
			summaryIDs = append(summaryIDs, ref.EntityID)
		}
	}
	summaries, err := relationshipEntitiesFrom(ctx, tx, uniqueSortedUUIDs(summaryIDs), u)
	if err != nil {
		return EntityAsOfResponse{}, err
	}

	recAt := subject.UpdatedAt.UTC()
	if recAt.IsZero() {
		recAt = time.Now().UTC()
	}
	return EntityAsOfResponse{
		EntityID:        subject.ID,
		Kind:            subject.Kind,
		At:              at,
		Scope:           scope,
		DefinitionETag:  defs.ETag,
		Title:           title,
		TitleStatus:     titleStatus,
		DisputedTitles:  disputedTitles,
		Translations:    subjectTrans,
		Attributes:      reconstructedAttrs,
		AttributeStates: attrStates,
		ExternalLinks:   subjectLinks,
		Publishers:      topPubs,
		PublisherState:  topPubState,
		Operators:       topOps,
		OperatorState:   topOpState,
		ReleaseStatus:   topStatus,
		StatusState:     topStatusState,
		Cover:           coverSel,
		AppIcon:         iconSel,
		Pictures:        validPics,
		Releases:        relViews,
		ActiveRelations: activeRelations,
		UnknownFields:   uniqueSortedStrings(topUnknown),
		DisputedFields:  uniqueSortedStrings(topDisputed),
		RecordedAt:      recAt,
		Entities:        summaries,
	}, nil
}
