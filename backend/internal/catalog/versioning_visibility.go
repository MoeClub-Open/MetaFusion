package catalog

import (
	"context"
	"encoding/json"
	"strconv"
)

type versionDocument struct {
	target string
	raw    *json.RawMessage
}

// Different versions of the same track must remain separate, while sharing
// one lookup of the current visibility of all referenced expressions.
func visibleVersionDocuments(ctx context.Context, q queryer, documents []versionDocument, u User) error {
	tracks := map[string]Entity{}
	for i, doc := range documents {
		if doc.target != "entity" || rawNull(*doc.raw) {
			continue
		}
		var e Entity
		if err := json.Unmarshal(*doc.raw, &e); err != nil {
			return err
		}
		if e.Kind == "track" {
			if len(e.Contents) == 0 {
				*doc.raw = json.RawMessage(encode(e))
			} else {
				tracks[strconv.Itoa(i)] = e
			}
		}
	}
	if err := filterVisibleTrackContents(ctx, q, tracks, &u); err != nil {
		return err
	}
	for key, e := range tracks {
		i, _ := strconv.Atoi(key)
		*documents[i].raw = json.RawMessage(encode(e))
	}
	return nil
}

func visibleVersionObjects(ctx context.Context, q queryer, objects []WorkspaceObject, u User) error {
	documents := make([]versionDocument, 0, 2*len(objects))
	for i := range objects {
		o := &objects[i]
		documents = append(documents, versionDocument{o.Target, &o.Base}, versionDocument{o.Target, &o.Document})
	}
	if err := visibleVersionDocuments(ctx, q, documents, u); err != nil {
		return err
	}
	for i := range objects {
		objects[i].Committed = objects[i].Document
	}
	return nil
}

func visibleVersionChanges(ctx context.Context, q queryer, c *CollaborationCommit, u User) error {
	documents := make([]versionDocument, 0, 3*len(c.Changes))
	for i := range c.Changes {
		co := &c.Changes[i]
		documents = append(documents, versionDocument{co.Target, &co.Before}, versionDocument{co.Target, &co.After}, versionDocument{co.Target, &co.Base})
	}
	return visibleVersionDocuments(ctx, q, documents, u)
}
