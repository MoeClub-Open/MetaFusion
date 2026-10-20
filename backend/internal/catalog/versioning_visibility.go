package catalog

import (
	"context"
	"encoding/json"
)

func visibleVersionDocument(ctx context.Context, q queryer, target string, raw json.RawMessage, u User) (json.RawMessage, error) {
	if target != "entity" || rawNull(raw) {
		return raw, nil
	}
	var e Entity
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	if e.Kind != "track" {
		return raw, nil
	}
	visible, err := visibleEntityContents(ctx, q, e, &u)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encode(visible)), nil
}

func visibleVersionObject(ctx context.Context, q queryer, o *WorkspaceObject, u User) error {
	var err error
	o.Base, err = visibleVersionDocument(ctx, q, o.Target, o.Base, u)
	if err != nil {
		return err
	}
	o.Document, err = visibleVersionDocument(ctx, q, o.Target, o.Document, u)
	if err != nil {
		return err
	}
	o.Committed = o.Document
	return nil
}

func visibleVersionChanges(ctx context.Context, q queryer, c *CollaborationCommit, u User) error {
	for i := range c.Changes {
		co := &c.Changes[i]
		var err error
		co.Before, err = visibleVersionDocument(ctx, q, co.Target, co.Before, u)
		if err != nil {
			return err
		}
		co.After, err = visibleVersionDocument(ctx, q, co.Target, co.After, u)
		if err != nil {
			return err
		}
		co.Base, err = visibleVersionDocument(ctx, q, co.Target, co.Base, u)
		if err != nil {
			return err
		}
	}
	return nil
}
