package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	retentionLockKey    = 740208
	retentionBatchSize  = 500
	retentionMaxBatches = 20
)

// RetentionPolicy bounds disposable payloads, never immutable receipts or guards.
// RequiredConsumers must include every enabled consumer of the known entity
// events. A disabled integration is omitted by the server's explicit wiring.
type RetentionPolicy struct {
	OutboxDays        int
	CommitRequestDays int
	Interval          time.Duration
	RequiredConsumers []string
}

func RetentionPolicyFromEnv(getenv func(string) string) (RetentionPolicy, error) {
	p := RetentionPolicy{OutboxDays: 7, CommitRequestDays: 90, Interval: time.Hour,
		RequiredConsumers: []string{openSearchConsumer}}
	for _, item := range []struct {
		key string
		dst *int
	}{
		{"CATALOG_OUTBOX_RETENTION_DAYS", &p.OutboxDays},
		{"CATALOG_COMMIT_REQUEST_RETENTION_DAYS", &p.CommitRequestDays},
	} {
		if value := strings.TrimSpace(getenv(item.key)); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 || parsed > 36500 {
				return p, fmt.Errorf("invalid %s: expected 0..36500 days", item.key)
			}
			*item.dst = parsed
		}
	}
	if value := strings.TrimSpace(getenv("CATALOG_RETENTION_INTERVAL_SECONDS")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 60 || parsed > 86400 {
			return p, fmt.Errorf("invalid CATALOG_RETENTION_INTERVAL_SECONDS: expected 60..86400 seconds")
		}
		p.Interval = time.Duration(parsed) * time.Second
	}
	return p, p.validate()
}

func (p RetentionPolicy) validate() error {
	if p.OutboxDays < 0 || p.OutboxDays > 36500 || p.CommitRequestDays < 0 || p.CommitRequestDays > 36500 ||
		p.Interval < time.Minute || p.Interval > 24*time.Hour {
		return fmt.Errorf("invalid_catalog_retention_policy")
	}
	if !contains(p.RequiredConsumers, openSearchConsumer) {
		return fmt.Errorf("catalog_retention_requires_opensearch_ack")
	}
	for _, consumer := range p.RequiredConsumers {
		if strings.TrimSpace(consumer) == "" || strings.TrimSpace(consumer) != consumer {
			return fmt.Errorf("invalid_catalog_retention_consumer")
		}
	}
	return nil
}

type RetentionResult struct {
	Events         int64
	Deliveries     int64
	CommitRequests int64
}

// RunRetention waits one interval before the first bounded cleanup, so startup
// compatibility checks remain read-only. A transaction lock coordinates replicas.
func (s *Store) RunRetention(ctx context.Context, policy RetentionPolicy) {
	if err := policy.validate(); err != nil {
		log.Printf("catalog retention disabled: %v", err)
		return
	}
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			var result RetentionResult
			var err error
			for batch := 0; batch < retentionMaxBatches; batch++ {
				var removed RetentionResult
				removed, err = s.CleanupRetention(cleanupCtx, policy)
				result.Events += removed.Events
				result.Deliveries += removed.Deliveries
				result.CommitRequests += removed.CommitRequests
				if err != nil || (removed.Events < retentionBatchSize && removed.CommitRequests < retentionBatchSize) {
					break
				}
			}
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("catalog retention delayed: %v", err)
			} else if result.Events != 0 || result.CommitRequests != 0 {
				log.Printf("catalog retention: events=%d deliveries=%d commit_requests=%d",
					result.Events, result.Deliveries, result.CommitRequests)
			}
		}
	}
}

// CleanupRetention processes at most one batch per category. Days=0 retains that
// category forever. Idempotency keys/responses, commits actor/hash/result and
// revisions (including commit provenance) are deliberately never deleted.
func (s *Store) CleanupRetention(ctx context.Context, policy RetentionPolicy) (RetentionResult, error) {
	var result RetentionResult
	if err := policy.validate(); err != nil {
		return result, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var locked bool
	if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock($1::bigint)", retentionLockKey).Scan(&locked); err != nil {
		return result, err
	}
	if !locked {
		return result, nil
	}
	if policy.OutboxDays > 0 {
		result.Events, result.Deliveries, err = pruneOutbox(ctx, tx, policy)
		if err != nil {
			return RetentionResult{}, err
		}
	}
	if policy.CommitRequestDays > 0 {
		updated, err := tx.ExecContext(ctx, `WITH expired AS (
			SELECT id FROM catalog.commits
			WHERE created_at < now() - make_interval(days => $1)
			  AND request <> 'null'::jsonb AND result <> 'null'::jsonb AND review_status <> 'pending_review'
			ORDER BY created_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
		) UPDATE catalog.commits c SET request = 'null'::jsonb
		  FROM expired WHERE c.id = expired.id`, policy.CommitRequestDays, retentionBatchSize)
		if err != nil {
			return RetentionResult{}, err
		}
		result.CommitRequests, err = updated.RowsAffected()
		if err != nil {
			return RetentionResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, err
	}
	return result, nil
}

func pruneOutbox(ctx context.Context, tx *sql.Tx, policy RetentionPolicy) (events, deliveries int64, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id::text FROM catalog.outbox o
		WHERE o.created_at < now() - make_interval(days => $1)
		AND (
		  o.type IN ('relation.saved','relation.deleted','definitions.updated','rate_limits.updated')
		  OR (
		    o.type IN ('entity.saved','entity.deleted','entity.merged','entity.unpublished')
		    AND NOT EXISTS (
		      SELECT 1 FROM unnest($2::text[]) AS required(consumer)
		      WHERE NOT EXISTS (SELECT 1 FROM catalog.deliveries d
		        WHERE d.consumer = required.consumer AND d.event_id = o.id)
		    )
		    AND (o.type NOT IN ('entity.deleted','entity.merged') OR EXISTS (
		      SELECT 1 FROM catalog.lifecycle_actions a
		      WHERE a.target_id = o.entity_id AND a.version = o.version AND a.action = o.type
		    ))
		  )
		)
		ORDER BY o.created_at, o.id LIMIT $3 FOR UPDATE SKIP LOCKED`,
		policy.OutboxDays, pq.Array(policy.RequiredConsumers), retentionBatchSize)
	if err != nil {
		return 0, 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, 0, err
	}
	// Parent FOR UPDATE locks exclude concurrent FK inserts until commit; delete
	// child acknowledgements first because this FK is immediate, never bypass it.
	deleted, err := tx.ExecContext(ctx, "DELETE FROM catalog.deliveries WHERE event_id = ANY($1::uuid[])", pq.Array(ids))
	if err != nil {
		return 0, 0, err
	}
	deliveries, err = deleted.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	deleted, err = tx.ExecContext(ctx, "DELETE FROM catalog.outbox WHERE id = ANY($1::uuid[])", pq.Array(ids))
	if err != nil {
		return 0, 0, err
	}
	events, err = deleted.RowsAffected()
	return events, deliveries, err
}
