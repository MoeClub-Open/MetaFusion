package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// TemporalCompareQuery 描述 GET /api/catalog/entities/:id/compare 的查询参数。
type TemporalCompareQuery struct {
	From      string
	To        string
	Region    string
	Platform  string
	ReleaseID string
}

// EntityTemporalCompare 实现 GET /api/catalog/entities/:id/compare?from=&to=&region=&platform=&release_id=。
func (s *Store) EntityTemporalCompare(ctx context.Context, id string, q TemporalCompareQuery, u *User) (EntityTemporalCompareResponse, error) {
	uid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return EntityTemporalCompareResponse{}, fmt.Errorf("invalid_id")
	}
	fromDate := strings.TrimSpace(q.From)
	toDate := strings.TrimSpace(q.To)
	if fromDate == "" || toDate == "" {
		return EntityTemporalCompareResponse{}, fmt.Errorf("invalid_date")
	}
	if !validPartialISODate(fromDate) || !validPartialISODate(toDate) {
		return EntityTemporalCompareResponse{}, fmt.Errorf("invalid_date")
	}
	if pictureTimeStart(fromDate).After(pictureTimeEnd(toDate)) {
		return EntityTemporalCompareResponse{}, fmt.Errorf("invalid_date_range")
	}
	relFilter := strings.TrimSpace(q.ReleaseID)
	if relFilter != "" {
		if _, err := uuid.Parse(relFilter); err != nil {
			return EntityTemporalCompareResponse{}, fmt.Errorf("invalid_id")
		}
	}

	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return EntityTemporalCompareResponse{}, err
	}
	defer tx.Rollback()

	fromSnap, err := s.entityAsOfTx(ctx, tx, uid.String(), TemporalAsOfQuery{
		At: fromDate, Region: q.Region, Platform: q.Platform, ReleaseID: relFilter,
	}, u)
	if err != nil {
		return EntityTemporalCompareResponse{}, err
	}
	toSnap, err := s.entityAsOfTx(ctx, tx, uid.String(), TemporalAsOfQuery{
		At: toDate, Region: q.Region, Platform: q.Platform, ReleaseID: relFilter,
	}, u)
	if err != nil {
		return EntityTemporalCompareResponse{}, err
	}
	timelineBetween, err := s.entityTimelineTx(ctx, tx, uid.String(), TimelineQuery{
		From: fromDate, To: toDate, Region: q.Region, Platform: q.Platform, ReleaseID: relFilter,
	}, nil, u)
	if err != nil {
		return EntityTemporalCompareResponse{}, err
	}

	fieldDiffs := diffTemporalSnapshots(fromSnap, toSnap)
	relDiffs := diffTemporalRelations(fromSnap.ActiveRelations, toSnap.ActiveRelations)

	mergedEntities := map[string]RelationshipEntity{}
	for k, v := range fromSnap.Entities {
		mergedEntities[k] = v
	}
	for k, v := range toSnap.Entities {
		mergedEntities[k] = v
	}
	for k, v := range timelineBetween.Entities {
		mergedEntities[k] = v
	}

	return EntityTemporalCompareResponse{
		EntityID:        uid.String(),
		From:            fromDate,
		To:              toDate,
		Scope:           fromSnap.Scope,
		DefinitionETag:  fromSnap.DefinitionETag,
		FromSnapshot:    fromSnap,
		ToSnapshot:      toSnap,
		FieldChanges:    fieldDiffs,
		RelationChanges: relDiffs,
		EventsBetween:   timelineBetween.Items,
		Entities:        mergedEntities,
	}, tx.Commit()
}

func agentFactIDs(facts []TemporalAgentFact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.AgentID)
	}
	return uniqueSortedStrings(out)
}

func picSelectionURL(sel TemporalPictureSelection) string {
	if sel.Picture == nil {
		return ""
	}
	return strings.TrimSpace(sel.Picture.URL)
}

func comparableExternalLinks(links []ExternalLink) []ExternalLink {
	if len(links) == 0 {
		return nil
	}
	cp := make([]ExternalLink, len(links))
	for i, l := range links {
		l.RecordedAt = nil
		cp[i] = l
	}
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Kind != cp[j].Kind {
			return cp[i].Kind < cp[j].Kind
		}
		if cp[i].URL != cp[j].URL {
			return cp[i].URL < cp[j].URL
		}
		if cp[i].Region != cp[j].Region {
			return cp[i].Region < cp[j].Region
		}
		return cp[i].Platform < cp[j].Platform
	})
	return cp
}

func diffTemporalSnapshots(a, b EntityAsOfResponse) []TemporalFieldDiff {
	diffs := []TemporalFieldDiff{}
	addIfChanged := func(field, relID, scopeLbl string, fv, tv any, fs, ts string) {
		if encode(fv) == encode(tv) && fs == ts {
			return
		}
		diffs = append(diffs, TemporalFieldDiff{
			Field: field, ReleaseID: relID, ScopeLabel: scopeLbl,
			FromValue: fv, ToValue: tv, FromStatus: fs, ToStatus: ts,
		})
	}

	addIfChanged("title", "", "", a.Title, b.Title, a.TitleStatus, b.TitleStatus)
	diffTransMap := func(prefix, relID, scopeLbl string, ta, tb map[string]Translation, dispA, dispB []string) {
		locKeys := []string{}
		for k := range ta {
			locKeys = append(locKeys, k)
		}
		for k := range tb {
			locKeys = append(locKeys, k)
		}
		for _, d := range append(append([]string{}, dispA...), dispB...) {
			if loc, ok := strings.CutPrefix(d, "translations."); ok && loc != "" {
				locKeys = append(locKeys, loc)
			}
		}
		for _, loc := range uniqueSortedStrings(locKeys) {
			va, okA := ta[loc]
			vb, okB := tb[loc]
			sa := "unknown"
			if okA && strings.TrimSpace(va.Title) != "" {
				sa = "confirmed"
			} else if contains(dispA, "translations."+loc) {
				sa = "disputed"
			}
			sb := "unknown"
			if okB && strings.TrimSpace(vb.Title) != "" {
				sb = "confirmed"
			} else if contains(dispB, "translations."+loc) {
				sb = "disputed"
			}
			addIfChanged(prefix+"translations."+loc, relID, scopeLbl, va.Title, vb.Title, sa, sb)
		}
	}
	diffAttrMap := func(prefix, relID, scopeLbl, kind string, aa, ab map[string]any, saMap, sbMap map[string]string) {
		keys := []string{}
		for k := range aa {
			keys = append(keys, k)
		}
		for k := range ab {
			keys = append(keys, k)
		}
		for k := range saMap {
			keys = append(keys, k)
		}
		for k := range sbMap {
			keys = append(keys, k)
		}
		for _, k := range uniqueSortedStrings(keys) {
			if kind == "release" && (k == "release_status" || k == "publisher" || k == "operator") {
				continue
			}
			va, hasA := aa[k]
			vb, hasB := ab[k]
			sa := saMap[k]
			if sa == "" {
				if hasA {
					sa = "confirmed"
				} else {
					sa = "unknown"
				}
			}
			sb := sbMap[k]
			if sb == "" {
				if hasB {
					sb = "confirmed"
				} else {
					sb = "unknown"
				}
			}
			addIfChanged(prefix+"attributes."+k, relID, scopeLbl, va, vb, sa, sb)
		}
	}
	linkState := func(links []ExternalLink, unknown, disputed []string) string {
		if contains(disputed, "external_links") {
			return "disputed"
		}
		if len(links) > 0 {
			return "confirmed"
		}
		if contains(unknown, "external_links") {
			return "unknown"
		}
		return "confirmed"
	}
	diffTransMap("", "", "", a.Translations, b.Translations, a.DisputedFields, b.DisputedFields)
	addIfChanged("cover", "", "", picSelectionURL(a.Cover), picSelectionURL(b.Cover), a.Cover.Status, b.Cover.Status)
	diffAttrMap("", "", "", a.Kind, a.Attributes, b.Attributes, a.AttributeStates, b.AttributeStates)
	if len(a.ExternalLinks) > 0 || len(b.ExternalLinks) > 0 || contains(a.DisputedFields, "external_links") || contains(b.DisputedFields, "external_links") {
		addIfChanged(
			"external_links", "", "",
			comparableExternalLinks(a.ExternalLinks),
			comparableExternalLinks(b.ExternalLinks),
			linkState(a.ExternalLinks, a.UnknownFields, a.DisputedFields),
			linkState(b.ExternalLinks, b.UnknownFields, b.DisputedFields),
		)
	}
	addIfChanged("app_icon", "", "", picSelectionURL(a.AppIcon), picSelectionURL(b.AppIcon), a.AppIcon.Status, b.AppIcon.Status)

	if a.Kind == "release" {
		addIfChanged("release_status", a.EntityID, "", a.ReleaseStatus, b.ReleaseStatus, a.StatusState, b.StatusState)
		addIfChanged("publishers", a.EntityID, "", agentFactIDs(a.Publishers), agentFactIDs(b.Publishers), a.PublisherState, b.PublisherState)
		addIfChanged("operators", a.EntityID, "", agentFactIDs(a.Operators), agentFactIDs(b.Operators), a.OperatorState, b.OperatorState)
		return diffs
	}

	relA := map[string]TemporalReleaseView{}
	for _, r := range a.Releases {
		relA[r.ReleaseID] = r
	}
	relB := map[string]TemporalReleaseView{}
	relIDs := []string{}
	for _, r := range a.Releases {
		relIDs = append(relIDs, r.ReleaseID)
	}
	for _, r := range b.Releases {
		relB[r.ReleaseID] = r
		relIDs = append(relIDs, r.ReleaseID)
	}
	for _, rid := range uniqueSortedStrings(relIDs) {
		ra, rb := relA[rid], relB[rid]
		lbl := strings.TrimSpace(strings.Join(uniqueSortedStrings([]string{firstNonEmpty(ra.Region, rb.Region), firstNonEmpty(ra.Platform, rb.Platform)}), " / "))
		addIfChanged("release.title", rid, lbl, ra.Title, rb.Title, ra.TitleStatus, rb.TitleStatus)
		addIfChanged("release.release_status", rid, lbl, ra.ReleaseStatus, rb.ReleaseStatus, ra.StatusState, rb.StatusState)
		addIfChanged("release.publishers", rid, lbl, agentFactIDs(ra.Publishers), agentFactIDs(rb.Publishers), ra.PublisherState, rb.PublisherState)
		addIfChanged("release.operators", rid, lbl, agentFactIDs(ra.Operators), agentFactIDs(rb.Operators), ra.OperatorState, rb.OperatorState)
		addIfChanged("release.cover", rid, lbl, picSelectionURL(ra.Cover), picSelectionURL(rb.Cover), ra.Cover.Status, rb.Cover.Status)
		addIfChanged("release.app_icon", rid, lbl, picSelectionURL(ra.AppIcon), picSelectionURL(rb.AppIcon), ra.AppIcon.Status, rb.AppIcon.Status)
		diffTransMap("release.", rid, lbl, ra.Translations, rb.Translations, ra.DisputedFields, rb.DisputedFields)
		diffAttrMap("release.", rid, lbl, "release", ra.Attributes, rb.Attributes, ra.AttributeStates, rb.AttributeStates)
		if len(ra.ExternalLinks) > 0 || len(rb.ExternalLinks) > 0 || contains(ra.DisputedFields, "external_links") || contains(rb.DisputedFields, "external_links") {
			addIfChanged(
				"release.external_links", rid, lbl,
				comparableExternalLinks(ra.ExternalLinks),
				comparableExternalLinks(rb.ExternalLinks),
				linkState(ra.ExternalLinks, ra.UnknownFields, ra.DisputedFields),
				linkState(rb.ExternalLinks, rb.UnknownFields, rb.DisputedFields),
			)
		}
	}
	return diffs
}

func diffTemporalRelations(fromLinks, toLinks []EntityLink) []TemporalRelationDiff {
	fromMap := map[string]EntityLink{}
	keys := []string{}
	for _, l := range fromLinks {
		fromMap[l.Key] = l
		keys = append(keys, l.Key)
	}
	toMap := map[string]EntityLink{}
	for _, l := range toLinks {
		toMap[l.Key] = l
		keys = append(keys, l.Key)
	}
	diffs := []TemporalRelationDiff{}
	for _, k := range uniqueSortedStrings(keys) {
		a, okA := fromMap[k]
		b, okB := toMap[k]
		switch {
		case okA && !okB:
			aCopy := a
			diffs = append(diffs, TemporalRelationDiff{Change: "removed", RuleCode: a.RuleCode, SourceID: a.SourceID, TargetID: a.TargetID, FromLink: &aCopy})
		case !okA && okB:
			bCopy := b
			diffs = append(diffs, TemporalRelationDiff{Change: "added", RuleCode: b.RuleCode, SourceID: b.SourceID, TargetID: b.TargetID, ToLink: &bCopy})
		case encode(a.Attributes) != encode(b.Attributes) || a.Role != b.Role:
			aCopy, bCopy := a, b
			diffs = append(diffs, TemporalRelationDiff{Change: "changed", RuleCode: b.RuleCode, SourceID: b.SourceID, TargetID: b.TargetID, FromLink: &aCopy, ToLink: &bCopy})
		}
	}
	return diffs
}
