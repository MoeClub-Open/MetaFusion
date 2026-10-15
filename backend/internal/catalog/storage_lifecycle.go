package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// StorageLifecycleConsumer must also be required by outbox retention whenever
// this consumer is enabled. Delivery acknowledgements belong to this consumer,
// independently of OpenSearch acknowledgements.
const StorageLifecycleConsumer = "storage-lifecycle-v1"

const storageLifecycleLockID = int64(740221)

// Only storage reference facts cross the service boundary. Inclusions refer to
// Expressions, not assets; pending submissions never change applied asset references.
type storageLifecycleSnapshot struct {
	EntityID      string           `json:"entity_id"`
	Kind          string           `json:"kind"`
	State         string           `json:"state"`
	Version       int64            `json:"version"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Aliases       []string         `json:"aliases"`
	AliasVersions map[string]int64 `json:"alias_versions"`
	CoverAssetIDs []string         `json:"cover_asset_ids"`
}

type storageLifecycleClient struct {
	endpoint string
	secret   string
	http     *http.Client
}

func newStorageLifecycleClient(base, secret string) (*storageLifecycleClient, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid storage lifecycle URL")
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, errors.New("storage lifecycle secret is required")
	}
	u.Path = "/internal/catalog/lifecycle"
	return &storageLifecycleClient{endpoint: u.String(), secret: secret, http: &http.Client{
		Timeout: 15 * time.Second,
		// A redirect must never forward the service credential to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// ValidateStorageLifecycleConfig lets startup fail before enabling retention's
// mandatory consumer ACK when a configured delivery endpoint is unusable.
func ValidateStorageLifecycleConfig(baseURL, secret string) error {
	_, err := newStorageLifecycleClient(baseURL, secret)
	return err
}

func (c *storageLifecycleClient) apply(ctx context.Context, state storageLifecycleSnapshot) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	res, err := c.http.Do(req)
	if err != nil {
		// Do not log the request, headers, secret or upstream response body.
		return fmt.Errorf("storage lifecycle request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("storage lifecycle returned HTTP %d", res.StatusCode)
	}
	var receipt struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&receipt); err != nil || !receipt.OK {
		return errors.New("storage lifecycle returned an invalid receipt")
	}
	return nil
}

type storageBindingTargets struct {
	EntityIDs  []string `json:"entity_ids"`
	NextCursor string   `json:"next_cursor"`
}

func (c *storageLifecycleClient) bindingTargets(ctx context.Context, after string) (storageBindingTargets, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return storageBindingTargets{}, err
	}
	u.Path = "/internal/catalog/binding-targets"
	q := u.Query()
	q.Set("limit", "100")
	if after != "" {
		q.Set("after", after)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return storageBindingTargets{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	res, err := c.http.Do(req)
	if err != nil {
		return storageBindingTargets{}, fmt.Errorf("storage binding inventory request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return storageBindingTargets{}, fmt.Errorf("storage binding inventory returned HTTP %d", res.StatusCode)
	}
	var page storageBindingTargets
	if err := json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&page); err != nil || page.EntityIDs == nil || len(page.EntityIDs) > 100 {
		return storageBindingTargets{}, errors.New("invalid storage binding inventory")
	}
	previous := after
	for _, id := range page.EntityIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || id <= previous {
			return storageBindingTargets{}, errors.New("invalid storage binding inventory cursor")
		}
		previous = id
	}
	if page.NextCursor != "" && (len(page.EntityIDs) == 0 || page.NextCursor != previous) {
		return storageBindingTargets{}, errors.New("invalid storage binding inventory next cursor")
	}
	return page, nil
}

// Bootstrap visits only identities that actually have storage references. It
// does not scan the whole metadata catalog and does not depend on old outbox
// events having survived a period when this optional consumer was disabled.
// Successful items advance the cursor before an error; restart replays safely.
func (s *Store) bootstrapStorageLifecycle(ctx context.Context, c *storageLifecycleClient, after string) (string, bool, error) {
	page, err := c.bindingTargets(ctx, after)
	if err != nil {
		return after, false, err
	}
	for _, id := range page.EntityIDs {
		state, err := s.currentStorageLifecycle(ctx, id)
		if err != nil {
			return after, false, err
		}
		if err := c.apply(ctx, state); err != nil {
			return after, false, err
		}
		after = id
	}
	return after, page.NextCursor == "", nil
}

// RunStorageLifecycle retries durable outbox facts without making catalog writes
// depend on storage availability. The caller enables it only with both settings
// configured and registers StorageLifecycleConsumer with its retention policy.
func (s *Store) RunStorageLifecycle(ctx context.Context, baseURL, secret string) {
	c, err := newStorageLifecycleClient(baseURL, secret)
	if err != nil {
		log.Printf("storage lifecycle consumer disabled: %v", err)
		return
	}
	backoff := time.Second
	bootstrapped := false
	cursor := ""
	for ctx.Err() == nil {
		var err error
		if !bootstrapped {
			cursor, bootstrapped, err = s.bootstrapStorageLifecycle(ctx, c, cursor)
		} else {
			err = s.syncStorageLifecycle(ctx, c)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("storage lifecycle delivery delayed; catalog writes remain available: %v", err)
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		} else {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Store) syncStorageLifecycle(ctx context.Context, c *storageLifecycleClient) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", storageLifecycleLockID).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", storageLifecycleLockID)
	}()
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,entity_id::text FROM catalog.outbox o
		WHERE o.type IN ('entity.saved','entity.deleted','entity.merged')
		AND NOT EXISTS(SELECT 1 FROM catalog.deliveries d WHERE d.consumer=$1 AND d.event_id=o.id)
		ORDER BY o.created_at,o.id LIMIT 100`, StorageLifecycleConsumer)
	if err != nil {
		return err
	}
	byEntity := map[string][]string{}
	var ids []string
	for rows.Next() {
		var eventID, entityID string
		if err := rows.Scan(&eventID, &entityID); err != nil {
			rows.Close()
			return err
		}
		if _, found := byEntity[entityID]; !found {
			ids = append(ids, entityID)
		}
		byEntity[entityID] = append(byEntity[entityID], eventID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		state, err := s.currentStorageLifecycle(ctx, id)
		if err != nil {
			return err
		}
		if err := c.apply(ctx, state); err != nil {
			return err
		}
		// The storage operation is idempotent. A failure before this durable ACK
		// replays it after restart; no in-memory queue can lose the reference fact.
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO catalog.deliveries(consumer,event_id)
			SELECT $1,unnest($2::uuid[]) ON CONFLICT DO NOTHING`, StorageLifecycleConsumer, pq.Array(byEntity[id])); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) currentStorageLifecycle(ctx context.Context, id string) (storageLifecycleSnapshot, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return storageLifecycleSnapshot{}, err
	}
	defer tx.Rollback()
	cur := id
	seen := map[string]bool{}
	var e Entity
	for {
		if seen[cur] {
			return storageLifecycleSnapshot{}, errRedirectCycle
		}
		seen[cur] = true
		e, err = get(ctx, tx, cur)
		if errors.Is(err, sql.ErrNoRows) {
			// A physically removed row has no current references to retain. Do not
			// invent aliases or infer deletion from a visibility-filtered HTTP 404.
			state := storageLifecycleSnapshot{EntityID: cur, State: "deleted", Version: math.MaxInt64,
				UpdatedAt: time.Now().UTC(), Aliases: []string{}, AliasVersions: map[string]int64{}, CoverAssetIDs: []string{}}
			for alias := range seen {
				if alias != cur {
					state.Aliases = append(state.Aliases, alias)
					state.AliasVersions[alias] = math.MaxInt64
				}
			}
			return state, nil
		}
		if err != nil {
			return storageLifecycleSnapshot{}, err
		}
		if e.State != "merged" {
			break
		}
		cur = e.RedirectID
	}
	state := storageLifecycleSnapshot{EntityID: e.ID, Kind: e.Kind, State: e.State, Version: e.Version,
		UpdatedAt: e.UpdatedAt, Aliases: []string{}, AliasVersions: map[string]int64{}, CoverAssetIDs: []string{}}
	// A merge preserves source pictures in its historical identity. Keep their
	// assets while canonicalizing all file roles; a normal target Save must not
	// erase an old source cover. This read uses one database snapshot throughout.
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE identities(id) AS (
		SELECT $1::uuid UNION SELECT e.id FROM catalog.entities e JOIN identities i
		ON e.state='merged' AND e.document->>'redirect_id'=i.id::text)
		SELECT e.id::text,e.version,e.document FROM catalog.entities e JOIN identities i ON e.id=i.id
		ORDER BY e.id`, e.ID)
	if err != nil {
		return storageLifecycleSnapshot{}, err
	}
	assets := map[string]bool{}
	for rows.Next() {
		var entityID string
		var version int64
		var document []byte
		if err := rows.Scan(&entityID, &version, &document); err != nil {
			rows.Close()
			return storageLifecycleSnapshot{}, err
		}
		if entityID != e.ID {
			state.Aliases = append(state.Aliases, entityID)
			state.AliasVersions[entityID] = version
		}
		var entity Entity
		if err := json.Unmarshal(document, &entity); err != nil {
			rows.Close()
			return storageLifecycleSnapshot{}, err
		}
		for _, picture := range entity.Pictures {
			if picture.AssetID != "" {
				assets[picture.AssetID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storageLifecycleSnapshot{}, err
	}
	rows.Close()
	for assetID := range assets {
		state.CoverAssetIDs = append(state.CoverAssetIDs, assetID)
	}
	sort.Strings(state.CoverAssetIDs)
	return state, tx.Commit()
}
