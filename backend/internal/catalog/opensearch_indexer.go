package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
)

const openSearchStateDocID = "metafusion-index-state-" + openSearchIndexGeneration

func (s *Store) RunOpenSearchIndexer(ctx context.Context) {
	if s.OpenSearch == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		err := s.syncOpenSearch(ctx)
		s.OpenSearch.indexingDelayed.Store(err != nil)
		if err != nil && ctx.Err() == nil {
			s.OpenSearch.logIssue("OpenSearch indexing delayed; PostgreSQL remains authoritative", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) syncOpenSearch(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", openSearchLockID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		initialized, err := s.OpenSearch.isInitialized(ctx)
		s.OpenSearch.ready.Store(err == nil && initialized)
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", openSearchLockID)
	}()

	c := s.OpenSearch
	if err := c.ensureIndex(ctx); err != nil {
		c.ready.Store(false)
		return err
	}
	initialized, err := c.isInitialized(ctx)
	if err != nil {
		c.ready.Store(false)
		return err
	}
	// A completed index remains queryable while its durable update queue is
	// delayed. A rebuild still stays unavailable until its alias is published.
	c.ready.Store(initialized)
	if !initialized {
		c.ready.Store(false)
		if err := c.clearEntityDocuments(ctx); err != nil {
			return err
		}
		if err := s.rebuildOpenSearchIndex(ctx, c); err != nil {
			return err
		}
	}
	if err := s.deliverOpenSearch(ctx, c); err != nil {
		return err
	}
	if !initialized {
		if err := c.attachAlias(ctx); err != nil {
			return err
		}
		if err := c.setInitialized(ctx); err != nil {
			return err
		}
	}
	c.ready.Store(true)
	return nil
}

func (s *Store) deliverOpenSearch(ctx context.Context, c *OpenSearchClient) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,type,entity_id,version,payload,created_at
		FROM catalog.outbox o
		WHERE o.type LIKE 'entity.%'
		  AND NOT EXISTS(SELECT 1 FROM catalog.deliveries d WHERE d.consumer=$1 AND d.event_id=o.id)
		ORDER BY created_at,id LIMIT 500`, openSearchConsumer)
	if err != nil {
		return err
	}
	var events []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Type, &event.EntityID, &event.Version, &event.Payload, &event.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(events) == 0 {
		return nil
	}
	ids := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if !strings.HasPrefix(event.Type, "entity.") {
			continue
		}
		var entity Entity
		if err := json.Unmarshal(event.Payload, &entity); err != nil {
			return fmt.Errorf("decode entity outbox event %s: %w", event.ID, err)
		}
		if entity.ID != "" && entity.ID != event.EntityID {
			return fmt.Errorf("entity outbox event %s has a mismatched entity ID", event.ID)
		}
		if entity.Kind == "" {
			return fmt.Errorf("entity outbox event %s has no kind", event.ID)
		}
		if !seen[event.EntityID] {
			ids = append(ids, event.EntityID)
			seen[event.EntityID] = true
		}
	}
	// Events are durable change notifications. Index current facts, so a delayed
	// pre-migration payload cannot overwrite canonicalized attributes/structures.
	entities, err := s.searchIndexEntities(ctx, ids)
	if err != nil {
		return err
	}
	docs := make([]searchDocument, 0, len(entities))
	deletedIDs := make([]string, 0, len(ids)-len(entities))
	for _, id := range ids {
		if e, ok := entities[id]; ok {
			docs = append(docs, makeSearchDocument(e))
		} else {
			deletedIDs = append(deletedIDs, id)
		}
	}
	// Missing current rows are deletions, regardless of historical event payloads
	// or versions. Both writes and deletes can be replayed if acknowledgement fails.
	if err := c.writeDocuments(ctx, docs, deletedIDs, true); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.deliveries(consumer,event_id)
			VALUES($1,$2) ON CONFLICT DO NOTHING`, openSearchConsumer, event.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) searchIndexEntities(ctx context.Context, ids []string) (map[string]Entity, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id::text,kind,version,title,state,created_by,updated_at,document FROM catalog.entities WHERE id=ANY($1::uuid[])", pq.Array(ids))
	if err != nil {
		return nil, err
	}
	entities := map[string]Entity{}
	for rows.Next() {
		var e Entity
		var doc []byte
		if err := rows.Scan(&e.ID, &e.Kind, &e.Version, &e.Title, &e.State, &e.CreatedBy, &e.UpdatedAt, &doc); err != nil {
			rows.Close()
			return nil, err
		}
		var content Entity
		if err := json.Unmarshal(doc, &content); err != nil {
			rows.Close()
			return nil, err
		}
		content.ID, content.Kind, content.Version, content.Title = e.ID, e.Kind, e.Version, e.Title
		content.State, content.CreatedBy, content.UpdatedAt = e.State, e.CreatedBy, e.UpdatedAt
		entities[e.ID] = content
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return fillStructural(ctx, s.DB, entities)
}

func (c *OpenSearchClient) ensureIndex(ctx context.Context) error {
	status, _, err := c.request(ctx, http.MethodHead, "/"+openSearchIndex, nil, "")
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("OpenSearch index check returned HTTP %d", status)
	}
	mapping := map[string]any{
		"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0,
			"analysis": map[string]any{
				"tokenizer": map[string]any{"substring_grams": map[string]any{"type": "ngram", "min_gram": 3, "max_gram": 3}},
				"analyzer":  map[string]any{"substring_grams": map[string]any{"type": "custom", "tokenizer": "substring_grams", "filter": []string{"lowercase"}}},
			},
		},
		"mappings": map[string]any{"dynamic": "strict", "dynamic_templates": []any{map[string]any{"localized_titles": map[string]any{"path_match": "titles.*", "mapping": map[string]any{"type": "keyword", "ignore_above": 8191}}}}, "properties": map[string]any{
			"record_type":       map[string]any{"type": "keyword"},
			"initialized":       map[string]any{"type": "boolean"},
			"entity_id":         map[string]any{"type": "keyword"},
			"entity_version":    map[string]any{"type": "long"},
			"kind":              map[string]any{"type": "keyword"},
			"state":             map[string]any{"type": "keyword"},
			"created_by":        map[string]any{"type": "keyword"},
			"tags":              map[string]any{"type": "keyword"},
			"original_language": map[string]any{"type": "keyword"},
			"work_id":           map[string]any{"type": "keyword"},
			"work_ids":          map[string]any{"type": "keyword"},
			"sort_title":        map[string]any{"type": "keyword", "ignore_above": 8191},
			"titles":            map[string]any{"type": "object", "dynamic": true},
			"fields":            map[string]any{"type": "nested", "properties": map[string]any{"path": map[string]any{"type": "keyword"}, "value": map[string]any{"type": "keyword", "ignore_above": 8191}}},
			"content_unit_id":   map[string]any{"type": "keyword"},
			"release_id":        map[string]any{"type": "keyword"},
			"medium_id":         map[string]any{"type": "keyword"},
			"parent_id":         map[string]any{"type": "keyword"},
			"has_pictures":      map[string]any{"type": "boolean"},
			"updated_at":        map[string]any{"type": "date"},
			"title_text":        map[string]any{"type": "search_as_you_type", "max_shingle_size": 3},
			"search_text":       map[string]any{"type": "search_as_you_type", "max_shingle_size": 3},
			"search_text_grams": map[string]any{"type": "text", "analyzer": "substring_grams"},
			"search_text_exact": map[string]any{"type": "keyword", "ignore_above": 8191},
		}},
	}
	body, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	status, _, err = c.request(ctx, http.MethodPut, "/"+openSearchIndex, body, "application/json")
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusCreated {
		return nil
	}
	// Creation is idempotent even if a request completed before its connection failed.
	status, _, err = c.request(ctx, http.MethodHead, "/"+openSearchIndex, nil, "")
	if err == nil && status == http.StatusOK {
		return nil
	}
	return fmt.Errorf("OpenSearch index creation returned HTTP %d", status)
}

func (c *OpenSearchClient) attachAlias(ctx context.Context) error {
	body, err := json.Marshal(map[string]any{"actions": []map[string]any{{
		"remove": map[string]any{"index": "*", "alias": openSearchAlias, "must_exist": false},
	}, {
		"add": map[string]any{"index": openSearchIndex, "alias": openSearchAlias},
	}}})
	if err != nil {
		return err
	}
	status, _, err := c.request(ctx, http.MethodPost, "/_aliases", body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenSearch alias attach returned HTTP %d", status)
	}
	return nil
}

func (c *OpenSearchClient) isInitialized(ctx context.Context) (bool, error) {
	status, raw, err := c.request(ctx, http.MethodGet, "/"+openSearchIndex+"/_doc/"+openSearchStateDocID, nil, "")
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("OpenSearch state check returned HTTP %d", status)
	}
	var state struct {
		Source struct {
			Initialized bool `json:"initialized"`
		} `json:"_source"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, err
	}
	return state.Source.Initialized, nil
}

func (c *OpenSearchClient) setInitialized(ctx context.Context) error {
	body, _ := json.Marshal(map[string]any{"record_type": "state", "initialized": true})
	status, _, err := c.request(ctx, http.MethodPut, "/"+openSearchIndex+"/_doc/"+openSearchStateDocID+"?refresh=wait_for", body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenSearch state update returned HTTP %d", status)
	}
	return nil
}

func (c *OpenSearchClient) clearEntityDocuments(ctx context.Context) error {
	body, _ := json.Marshal(map[string]any{"query": map[string]any{"terms": map[string]any{"record_type": []string{"entity", "state"}}}})
	status, _, err := c.request(ctx, http.MethodPost, "/"+openSearchIndex+"/_delete_by_query?conflicts=proceed&refresh=true", body, "application/json")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenSearch index reset returned HTTP %d", status)
	}
	return nil
}

func (s *Store) rebuildOpenSearchIndex(ctx context.Context, c *OpenSearchClient) error {
	lastID := ""
	for {
		query := "SELECT id::text,kind,version,title,state,created_by,updated_at,document FROM catalog.entities ORDER BY id LIMIT 500"
		var rows *sql.Rows
		var err error
		if lastID == "" {
			rows, err = s.DB.QueryContext(ctx, query)
		} else {
			rows, err = s.DB.QueryContext(ctx, "SELECT id::text,kind,version,title,state,created_by,updated_at,document FROM catalog.entities WHERE id>$1::uuid ORDER BY id LIMIT 500", lastID)
		}
		if err != nil {
			return err
		}
		batch := make([]searchDocument, 0, 500)
		entities := map[string]Entity{}
		for rows.Next() {
			var e Entity
			var id, kind, title, status, createdBy string
			var version int64
			var updatedAt time.Time
			var doc []byte
			if err := rows.Scan(&id, &kind, &version, &title, &status, &createdBy, &updatedAt, &doc); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal(doc, &e); err != nil {
				rows.Close()
				return fmt.Errorf("decode entity %s for OpenSearch: %w", id, err)
			}
			// These values live in relational columns as well as in the API DTO.
			e.ID, e.Kind, e.Version, e.Title = id, kind, version, title
			e.State, e.CreatedBy, e.UpdatedAt = status, createdBy, updatedAt
			entities[e.ID] = e
			lastID = e.ID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(entities) == 0 {
			// Publish the completed rebuild only after every batch is searchable.
			return c.refreshIndex(ctx)
		}
		entities, err = fillStructural(ctx, s.DB, entities)
		if err != nil {
			return err
		}
		for _, e := range entities {
			batch = append(batch, makeSearchDocument(e))
		}
		if err := c.writeDocuments(ctx, batch, nil, false); err != nil {
			return err
		}
	}
}

func (c *OpenSearchClient) refreshIndex(ctx context.Context) error {
	status, raw, err := c.request(ctx, http.MethodPost, "/"+openSearchIndex+"/_refresh", nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenSearch rebuild refresh returned HTTP %d", status)
	}
	var result struct {
		Shards *struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode OpenSearch rebuild refresh response: %w", err)
	}
	if result.Shards == nil || result.Shards.Failed != 0 {
		return fmt.Errorf("OpenSearch rebuild refresh did not acknowledge all shards")
	}
	return nil
}

func (c *OpenSearchClient) writeDocuments(ctx context.Context, docs []searchDocument, deletedIDs []string, waitForRefresh bool) error {
	if len(docs)+len(deletedIDs) == 0 {
		return nil
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, doc := range docs {
		version := doc.EntityVersion
		if version < 1 {
			version = 1
		}
		if err := enc.Encode(map[string]any{"index": map[string]any{
			"_index": openSearchIndex, "_id": doc.EntityID,
			"version": version, "version_type": "external_gte",
		}}); err != nil {
			return err
		}
		if err := enc.Encode(doc); err != nil {
			return err
		}
	}
	for _, id := range deletedIDs {
		// The row no longer exists. Its old event version must not prevent removal
		// of a newer stale projection already present in the index.
		if err := enc.Encode(map[string]any{"delete": map[string]any{"_index": openSearchIndex, "_id": id}}); err != nil {
			return err
		}
	}
	refresh := "false"
	if waitForRefresh {
		refresh = "wait_for"
	}
	status, raw, err := c.request(ctx, http.MethodPost, "/_bulk?refresh="+refresh, body.Bytes(), "application/x-ndjson")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("OpenSearch bulk request returned HTTP %d", status)
	}
	var result struct {
		Items []map[string]struct {
			Status int             `json:"status"`
			Result string          `json:"result"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode OpenSearch bulk response: %w", err)
	}
	if len(result.Items) != len(docs)+len(deletedIDs) {
		return fmt.Errorf("OpenSearch returned incomplete bulk acknowledgements")
	}
	for i, item := range result.Items {
		action := "index"
		if i >= len(docs) {
			action = "delete"
		}
		response, ok := item[action]
		hasError := len(response.Error) != 0 && string(response.Error) != "null"
		accepted := !hasError && response.Status >= 200 && response.Status < 300
		if action == "delete" && response.Status == http.StatusNotFound && response.Result == "not_found" && !hasError {
			accepted = true
		}
		if action == "index" && response.Status == http.StatusConflict {
			var conflict struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(response.Error, &conflict); err == nil && conflict.Type == "version_conflict_engine_exception" {
				accepted = true
			}
		}
		if !ok || len(item) != 1 || !accepted {
			var detail struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(response.Error, &detail)
			return fmt.Errorf("OpenSearch rejected a bulk item with HTTP %d (%s): %s", response.Status, detail.Type, detail.Reason)
		}
	}
	return nil
}
