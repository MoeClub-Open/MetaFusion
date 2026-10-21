package catalog

import (
	"context"
	"strings"

	"github.com/lib/pq"
)

// DisplayCover is a read projection; it never changes an entity's pictures.
type DisplayCover struct {
	URL    string             `json:"url"`
	Origin string             `json:"origin"`
	From   DisplayCoverSource `json:"from"`
}

type DisplayCoverSource struct {
	ID               string                 `json:"id"`
	Title            string                 `json:"title"`
	OriginalLanguage string                 `json:"original_language"`
	Translations     map[string]Translation `json:"translations"`
}

func ownCover(e Entity) string {
	if len(e.Pictures) == 0 {
		return ""
	}
	return strings.TrimSpace(e.Pictures[0].URL)
}

// DisplayCovers batches the current page's references and checks each source's
// visibility. Cover metadata stays beside items, outside authoritative documents.
func (s *Store) DisplayCovers(ctx context.Context, items []Entity, u *User) (map[string]DisplayCover, error) {
	out := map[string]DisplayCover{}
	entities := map[string]Entity{}
	seen := map[string]bool{}
	missing := []string{}
	needsInheritance := false
	for _, e := range items {
		if seen[e.ID] {
			continue
		}
		entities[e.ID], seen[e.ID] = e, true
		needsInheritance = needsInheritance || ownCover(e) == ""
		if ownCover(e) == "" && (e.Kind == "work" || e.Kind == "content_unit" || e.Kind == "expression") {
			missing = append(missing, e.ID)
		}
	}
	if !needsInheritance {
		return out, nil
	}
	occurrences := map[string]string{}
	if len(missing) > 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT root.id::text, occurrence.release_id::text
		FROM unnest($1::uuid[]) root(id)
		JOIN LATERAL (
		 SELECT m.release_id FROM catalog.expressions x
		 JOIN catalog.entities xe ON xe.id=x.id AND xe.state='active'
		 JOIN catalog.track_contents c ON c.expression_id=x.id
		 JOIN catalog.tracks t ON t.id=c.track_id
		 JOIN catalog.entities te ON te.id=t.id AND te.state='active'
		 JOIN catalog.mediums m ON m.id=t.medium_id
		 JOIN catalog.entities me ON me.id=m.id AND me.state='active'
		 JOIN catalog.entities r ON r.id=m.release_id AND r.state='active'
		 WHERE x.id=root.id OR x.work_id=root.id OR x.content_unit_id=root.id
		 ORDER BY m.release_id,c.position,t.id LIMIT 1
		) occurrence ON true`, pq.Array(missing))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, release string
			if err := rows.Scan(&id, &release); err != nil {
				rows.Close()
				return nil, err
			}
			occurrences[id] = release
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for {
		ids := []string{}
		add := func(id string) {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for _, id := range occurrences {
			add(id)
		}
		for _, e := range entities {
			add(e.WorkID)
			add(e.ReleaseID)
			add(e.MediumID)
			if len(e.Subjects) > 0 {
				add(e.Subjects[0].WorkID)
			}
		}
		if len(ids) == 0 {
			break
		}
		got, err := s.getMany(ctx, ids, u)
		if err != nil {
			return nil, err
		}
		for id, e := range got {
			if e.State == "active" {
				entities[id] = e
			}
		}
	}
	for _, e := range items {
		if ownCover(e) != "" {
			continue
		}
		candidates := []struct{ id, origin string }{{e.WorkID, "mother_work"}}
		if len(e.Subjects) > 0 {
			candidates = append(candidates, struct{ id, origin string }{e.Subjects[0].WorkID, "subject_work"})
		}
		if release, ok := entities[occurrences[e.ID]]; ok {
			if len(release.Subjects) > 0 {
				candidates = append(candidates, struct{ id, origin string }{release.Subjects[0].WorkID, "subject_work"})
			}
			candidates = append(candidates, struct{ id, origin string }{release.ID, "release"})
		}
		releaseID := e.ReleaseID
		if medium, ok := entities[e.MediumID]; ok {
			candidates = append(candidates, struct{ id, origin string }{medium.ID, "medium"})
			releaseID = medium.ReleaseID
		}
		if release, ok := entities[releaseID]; ok {
			candidates = append(candidates, struct{ id, origin string }{release.ID, "release"})
			if len(release.Subjects) > 0 {
				candidates = append(candidates, struct{ id, origin string }{release.Subjects[0].WorkID, "subject_work"})
			}
		}
		for _, candidate := range candidates {
			source, ok := entities[candidate.id]
			if !ok || source.State != "active" || ownCover(source) == "" {
				continue
			}
			cover := DisplayCover{URL: ownCover(source), Origin: candidate.origin}
			cover.From.ID, cover.From.Title = source.ID, source.Title
			cover.From.OriginalLanguage = source.OriginalLanguage
			cover.From.Translations = map[string]Translation{}
			for locale, translation := range source.Translations {
				cover.From.Translations[locale] = Translation{Title: translation.Title}
			}
			out[e.ID] = cover
			break
		}
	}
	return out, nil
}
