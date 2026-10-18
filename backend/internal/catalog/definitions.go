package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DefinitionStatus reports whether the installed seed keys are present in the
// single live configuration. An etag is an overwrite guard, not a version ID.
type DefinitionStatus struct {
	ETag               string `json:"etag"`
	CheckedAt          string `json:"checked_at"`
	Degraded           bool   `json:"degraded"`
	PendingItems       int    `json:"pending_items"`
	PendingError       string `json:"pending_error,omitempty"`
	DanglingReferences int    `json:"dangling_references"`
}

type DefinitionSeedError struct {
	ETag    string
	Pending int
	Reason  string
}

func (e *DefinitionSeedError) Error() string {
	return fmt.Sprintf("definition seed merge did not apply: etag=%s, %d seed item(s) pending: %s", e.ETag, e.Pending, e.Reason)
}

func (s *Store) setDefinitionStatus(v DefinitionStatus) {
	v.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	s.defStatusMu.Lock()
	s.defStatus = v
	s.defStatusMu.Unlock()
}

func (s *Store) DefinitionStatus() DefinitionStatus {
	s.defStatusMu.Lock()
	defer s.defStatusMu.Unlock()
	return s.defStatus
}

// DefinitionImpactFor checks an incoming document without persisting a draft.
func (s *Store) DefinitionImpactFor(ctx context.Context, d Definitions, u User) (DefinitionImpact, error) {
	if !u.Can(PermissionDefinitionsManage) {
		return DefinitionImpact{}, errForbidden
	}
	return impact(ctx, s.DB, d)
}

// SaveDefinitions atomically checks the current etag, replays existing data,
// and replaces the one live document. No previous document is retained.
func (s *Store) SaveDefinitions(ctx context.Context, d Definitions, expectedETag string, u User, note string, sources []Source) (DefinitionConfig, error) {
	var saved DefinitionConfig
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		if !u.Can(PermissionDefinitionsManage) {
			return errForbidden
		}
		if err := validateSources(sources); err != nil {
			return err
		}
		if err := lockDefinitionsExclusive(ctx, tx); err != nil {
			return err
		}
		current, err := definitions(ctx, tx)
		if err != nil {
			return err
		}
		if expectedETag == "" || expectedETag != current.ETag {
			return errVersionConflict
		}
		probe, err := impact(ctx, tx, d)
		if err != nil {
			return err
		}
		if len(probe.Issues) > 0 {
			return fmt.Errorf("definition_impact: %s", encode(probe.Issues))
		}
		logDangling("definition save", probe.Dangling)
		newETag := uuid.NewString()
		var updatedAt time.Time
		if err := tx.QueryRowContext(ctx, "UPDATE catalog.definition_config SET document=$1,etag=$2,updated_at=now() WHERE singleton=true AND etag=$3 RETURNING updated_at", encode(d), newETag, expectedETag).Scan(&updatedAt); err != nil {
			if err == sql.ErrNoRows {
				return errVersionConflict
			}
			return err
		}
		// The business audit records the action and counts, never an old or new
		// complete document that could reintroduce a rollback source.
		var auditSeq int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM catalog.revisions WHERE target_id='definitions'").Scan(&auditSeq); err != nil {
			return err
		}
		if err := audit(ctx, tx, "definitions", auditSeq, u, note, sources, definitionsSummary(d), "definitions.updated"); err != nil {
			return err
		}
		saved = DefinitionConfig{ETag: newETag, Document: d, UpdatedAt: updatedAt}
		return nil
	})
	if err == nil {
		InvalidateDefinitionsCache()
	}
	return saved, err
}

// EnsureSeedDefinitions only adds new built-in keys. It never overwrites an
// edited key, and a failed replay leaves the live configuration untouched.
func (s *Store) EnsureSeedDefinitions(ctx context.Context) error {
	current, err := s.Definitions(ctx)
	if err != nil {
		return err
	}
	merged, added := mergeSeedDefinitions(current.Document, Defaults())
	ents, err := liveEntities(ctx, s.DB)
	if err != nil {
		return err
	}
	rels, err := relations(ctx, s.DB)
	if err != nil {
		return err
	}
	dangling, _, err := merged.scanDangling(ctx, s.DB, ents, rels)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		logDangling("seed scan", dangling)
		s.setDefinitionStatus(DefinitionStatus{ETag: current.ETag, DanglingReferences: len(dangling)})
		return nil
	}
	sys := User{ID: "00000000-0000-0000-0000-000000000000", Username: "system", Permissions: []string{PermissionDefinitionsManage}}
	note := "启动时合并新增的种子定义（只增不改）：" + strings.Join(added, "、")
	sources := []Source{{Kind: "url", URL: "https://github.com/MoeclubM/MetaFusion", Citation: "种子定义合并：backend/internal/catalog/defaults.go"}}
	saved, err := s.SaveDefinitions(ctx, merged, current.ETag, sys, note, sources)
	if err != nil {
		s.setDefinitionStatus(DefinitionStatus{ETag: current.ETag, Degraded: true, PendingItems: len(added), PendingError: err.Error(), DanglingReferences: len(dangling)})
		return &DefinitionSeedError{ETag: current.ETag, Pending: len(added), Reason: err.Error()}
	}
	s.setDefinitionStatus(DefinitionStatus{ETag: saved.ETag, DanglingReferences: len(dangling)})
	return nil
}
