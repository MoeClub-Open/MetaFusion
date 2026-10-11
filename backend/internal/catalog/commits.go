package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// A commit is an immutable, owner-scoped push receipt. Entity histories remain
// independent: there is no global catalog head which unrelated editors must move.
type CatalogCommit struct {
	ID              string            `json:"id"`
	DefinitionsETag string            `json:"definitions_etag"`
	EditNote        string            `json:"edit_note"`
	Sources         []Source          `json:"sources"`
	Operations      []CommitOperation `json:"operations"`
}
type CommitOperation struct {
	Target               string          `json:"target"`
	Action               string          `json:"action"`
	Ref                  string          `json:"ref,omitempty"`
	ID                   string          `json:"id,omitempty"`
	BaseVersion          int64           `json:"base_version,omitempty"`
	Document             json.RawMessage `json:"document,omitempty"`
	Patch                []CommitPatch   `json:"patch,omitempty"`
	ReviewedCandidateIDs []string        `json:"reviewed_candidate_ids"`
}

// Paths use JSON Pointer escaping. Arrays are atomic values; indexes cannot be
// edited independently because their identity changes when another editor sorts.
type CommitPatch struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value,omitempty"`
	Remove bool            `json:"remove,omitempty"`
}
type CommitItem struct {
	Target  string `json:"target"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Merged  bool   `json:"merged"`
	Changed bool   `json:"changed"`
}
type CommitReceipt struct {
	ID      string            `json:"id"`
	Applied bool              `json:"applied"`
	Refs    map[string]string `json:"refs"`
	Items   []CommitItem      `json:"items"`
}
type CommitConflict struct {
	Operation      int      `json:"operation"`
	ID             string   `json:"id"`
	BaseVersion    int64    `json:"base_version"`
	CurrentVersion int64    `json:"current_version"`
	Paths          []string `json:"paths"`
}
type commitConflictError struct{ Conflict CommitConflict }

func (e *commitConflictError) Error() string { return "commit_conflict" }

type commitIdentityError struct {
	Operation    int      `json:"operation"`
	CandidateIDs []string `json:"candidate_ids"`
}

func (e *commitIdentityError) Error() string { return "identity_candidates_changed" }

type commitOperationError struct {
	Index int
	Cause error
}

func (e *commitOperationError) Error() string { return e.Cause.Error() }
func (e *commitOperationError) Unwrap() error { return e.Cause }

var errPreviewRollback = errors.New("preview_rollback")
var errDefinitionsConflict = errors.New("definitions_conflict")
var commitRef = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

var entityCommitFields = strings.Fields("title original_language translations attributes external_ids pictures status content_unit_id parent_id position number contents subjects")
var relationCommitFields = strings.Fields("position attributes")

func pointerParts(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("invalid_patch_path")
	}
	parts := strings.Split(path[1:], "/")
	if len(parts) > 16 {
		return nil, fmt.Errorf("invalid_patch_path")
	}
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				if j+1 == len(p) || (p[j+1] != '0' && p[j+1] != '1') {
					return nil, fmt.Errorf("invalid_patch_path")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		if parts[i] == "" {
			return nil, fmt.Errorf("invalid_patch_path")
		}
	}
	return parts, nil
}
func validateCommit(in CatalogCommit) error {
	id, err := uuid.Parse(in.ID)
	if err != nil || id.String() != in.ID {
		return fmt.Errorf("invalid_commit_id")
	}
	if in.DefinitionsETag == "" {
		return fmt.Errorf("definitions_etag_required")
	}
	if err := validateSources(in.EditNote, in.Sources); err != nil {
		return err
	}
	if len(in.Operations) < 1 || len(in.Operations) > 100 {
		return fmt.Errorf("invalid_commit_size")
	}
	seen := map[string]bool{}
	for i, op := range in.Operations {
		fail := func(err error) error { return &commitOperationError{i, err} }
		if op.Target != "entity" && op.Target != "relation" {
			return fail(fmt.Errorf("invalid_commit_target"))
		}
		key := op.Target + ":" + op.ID
		switch op.Action {
		case "create":
			key = "ref:" + op.Ref
			if !commitRef.MatchString(op.Ref) || op.ID != "" || op.BaseVersion != 0 || len(op.Document) == 0 || len(op.Patch) != 0 {
				return fail(fmt.Errorf("invalid_create_operation"))
			}
			if op.Target == "entity" && op.ReviewedCandidateIDs == nil {
				return fail(fmt.Errorf("identity_review_required"))
			}
			if len(op.ReviewedCandidateIDs) > 1000 || (op.Target == "relation" && op.ReviewedCandidateIDs != nil) {
				return fail(fmt.Errorf("invalid_identity_review"))
			}
		case "update":
			id, err := uuid.Parse(op.ID)
			if err != nil || id.String() != op.ID || op.Ref != "" || op.BaseVersion < 1 || len(op.Document) != 0 || len(op.Patch) < 1 || len(op.Patch) > 200 {
				return fail(fmt.Errorf("invalid_update_operation"))
			}
			if len(op.ReviewedCandidateIDs) > 1000 || (!commitNeedsIdentityReview(op) && op.ReviewedCandidateIDs != nil) {
				return fail(fmt.Errorf("invalid_identity_review"))
			}
			if commitNeedsIdentityReview(op) && op.ReviewedCandidateIDs == nil {
				return fail(fmt.Errorf("identity_review_required"))
			}
			allowed := entityCommitFields
			if op.Target == "relation" {
				allowed = relationCommitFields
			}
			paths := []string{}
			for _, p := range op.Patch {
				parts, err := pointerParts(p.Path)
				if err != nil || !contains(allowed, parts[0]) || (p.Remove && len(p.Value) != 0) || (!p.Remove && !json.Valid(p.Value)) {
					return fail(fmt.Errorf("invalid_commit_patch"))
				}
				for _, prior := range paths {
					if prior == p.Path || strings.HasPrefix(prior, p.Path+"/") || strings.HasPrefix(p.Path, prior+"/") {
						return fail(fmt.Errorf("overlapping_patch_paths"))
					}
				}
				paths = append(paths, p.Path)
			}
		default:
			return fail(fmt.Errorf("invalid_commit_action"))
		}
		reviewed := map[string]bool{}
		for _, id := range op.ReviewedCandidateIDs {
			if reviewed[id] {
				return fail(fmt.Errorf("invalid_identity_review"))
			}
			reviewed[id] = true
			if strings.HasPrefix(id, "@local:") {
				if !commitRef.MatchString(strings.TrimPrefix(id, "@local:")) {
					return fail(fmt.Errorf("invalid_identity_review"))
				}
			} else if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
				return fail(fmt.Errorf("invalid_identity_review"))
			}
		}
		if seen[key] {
			return fail(fmt.Errorf("duplicate_commit_target"))
		}
		seen[key] = true
	}
	return nil
}

// Local references are explicit objects {"$ref":"name"}, never title strings.
func resolveCommitRefs(raw json.RawMessage, refs map[string]string) (json.RawMessage, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid_document")
	}
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["$ref"]; ok {
				name, ok := r.(string)
				id, found := refs[name]
				if !ok || !found || len(x) != 1 {
					return nil, fmt.Errorf("invalid_local_reference")
				}
				return id, nil
			}
			for k, child := range x {
				val, err := walk(child)
				if err != nil {
					return nil, err
				}
				x[k] = val
			}
		case []any:
			for i, child := range x {
				val, err := walk(child)
				if err != nil {
					return nil, err
				}
				x[i] = val
			}
		}
		return v, nil
	}
	v, err := walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func strictCommitDocument(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid_document")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid_document")
	}
	return nil
}
func commitMap(v any) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(encode(v)), &m)
	return m
}
func pointerValue(doc map[string]json.RawMessage, parts []string) (json.RawMessage, bool, error) {
	value, ok := doc[parts[0]]
	for _, k := range parts[1:] {
		if !ok {
			return nil, false, nil
		}
		var obj map[string]json.RawMessage
		if string(value) == "null" {
			return nil, false, nil
		}
		if err := json.Unmarshal(value, &obj); err != nil {
			return nil, false, fmt.Errorf("patch_requires_object")
		}
		value, ok = obj[k]
	}
	return value, ok, nil
}
func equalCommitValue(a json.RawMessage, aok bool, b json.RawMessage, bok bool) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	// Canonicalize numbers and maps using the same decoder on both sides.
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return encode(av) == encode(bv)
}
func putPointer(doc map[string]json.RawMessage, parts []string, value json.RawMessage, remove bool) error {
	if len(parts) == 1 {
		if remove {
			delete(doc, parts[0])
		} else {
			doc[parts[0]] = value
		}
		return nil
	}
	child := map[string]json.RawMessage{}
	if raw, ok := doc[parts[0]]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &child); err != nil {
			return fmt.Errorf("patch_requires_object")
		}
	}
	if err := putPointer(child, parts[1:], value, remove); err != nil {
		return err
	}
	doc[parts[0]] = json.RawMessage(encode(child))
	return nil
}
func mergeCommitPatch(base, current map[string]json.RawMessage, patch []CommitPatch) (map[string]json.RawMessage, []string, bool, error) {
	out := map[string]json.RawMessage{}
	for k, v := range current {
		out[k] = v
	}
	conflicts := []string{}
	changed := false
	for _, p := range patch {
		parts, _ := pointerParts(p.Path)
		b, bok, err := pointerValue(base, parts)
		if err != nil {
			return nil, nil, false, err
		}
		c, cok, err := pointerValue(current, parts)
		if err != nil {
			return nil, nil, false, err
		}
		if equalCommitValue(c, cok, p.Value, !p.Remove) {
			continue
		}
		if !equalCommitValue(b, bok, c, cok) {
			conflicts = append(conflicts, p.Path)
			continue
		}
		if err := putPointer(out, parts, p.Value, p.Remove); err != nil {
			return nil, nil, false, err
		}
		changed = true
	}
	return out, conflicts, changed, nil
}

func (s *Store) PushCommit(ctx context.Context, in CatalogCommit, u User, preview bool) (CommitReceipt, error) {
	if err := validateCommit(in); err != nil {
		return CommitReceipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out CommitReceipt
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		// A confirmed serialization rollback restarts the complete commit. Never
		// retain refs, items or a receipt assembled by the abandoned attempt.
		out = CommitReceipt{ID: in.ID, Applied: !preview, Refs: map[string]string{}, Items: []CommitItem{}}
		if !preview {
			res, err := tx.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result) VALUES($1,$2,$3,$4,'null') ON CONFLICT DO NOTHING`, in.ID, u.ID, requestHash(in), encode(in))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				var actor, hash string
				var raw []byte
				if err = tx.QueryRowContext(ctx, `SELECT actor_id,request_hash,result FROM catalog.commits WHERE id=$1`, in.ID).Scan(&actor, &hash, &raw); err != nil {
					return err
				}
				if actor != u.ID || hash != requestHash(in) {
					return errIdempotencyConflict
				}
				if string(raw) == "null" {
					return fmt.Errorf("commit_incomplete")
				}
				return json.Unmarshal(raw, &out)
			}
			if _, err = tx.ExecContext(ctx, `SELECT set_config('metafusion.commit_id',$1,true)`, in.ID); err != nil {
				return err
			}
		}
		// Determine scoped locks before taking rows. There is no global structure
		// lock: SSI detects inconsistent graph and lifecycle dependencies.
		existing := []string{}
		releaseIDs := map[string]bool{}
		for _, op := range in.Operations {
			if op.Action == "create" {
				out.Refs[op.Ref] = newID()
			}
			if op.Target == "relation" {
				continue
			}
			if op.Action == "update" {
				e, err := get(ctx, tx, op.ID)
				if err != nil {
					return err
				}
				if !visible(e, &u) || e.Status == "deleted" || e.Status == "merged" {
					return errForbidden
				}
				existing = append(existing, op.ID)
				switch e.Kind {
				case "release":
					releaseIDs[e.ID] = true
				case "medium":
					releaseIDs[e.ReleaseID] = true
				case "track":
					var rid string
					if err = tx.QueryRowContext(ctx, `SELECT release_id FROM catalog.mediums WHERE id=$1`, e.MediumID).Scan(&rid); err != nil {
						return err
					}
					releaseIDs[rid] = true
				}
			}
		}
		// Every entity writer shares these identity locks. Updates lock both their
		// current and patched identity before definitions, release and row locks.
		identityKeys := map[string]bool{}
		for _, op := range in.Operations {
			if op.Target != "entity" {
				continue
			}
			var e Entity
			if op.Action == "create" {
				raw, err := resolveCommitRefs(op.Document, out.Refs)
				if err != nil {
					return err
				}
				if err = strictCommitDocument(raw, &e); err != nil {
					return err
				}
			} else {
				old, err := get(ctx, tx, op.ID)
				if err != nil {
					return err
				}
				e, err = identityPatchedEntity(old, op.Patch, out.Refs)
				if err != nil {
					return err
				}
				if !commitNeedsIdentityReview(op) && encode(identityWriteDocument(old)) == encode(identityWriteDocument(e)) {
					continue
				}
				if err = addIdentityWriteKeys(ctx, tx, old, identityKeys); err != nil {
					return err
				}
			}
			if err := addIdentityWriteKeys(ctx, tx, e, identityKeys); err != nil {
				return err
			}
		}
		if err := lockIdentityWriteKeys(ctx, tx, identityKeys); err != nil {
			return err
		}
		if err := lockDefinitionsShared(ctx, tx); err != nil {
			return err
		}
		v, err := definitions(ctx, tx)
		if err != nil {
			return err
		}
		if v.ETag != in.DefinitionsETag {
			return errDefinitionsConflict
		}
		// Creates may reference an existing release; acquire it with all other
		// release locks in stable order, even if operations arrive in reverse order.
		for _, op := range in.Operations {
			if op.Target != "entity" || op.Action != "create" {
				continue
			}
			raw, err := resolveCommitRefs(op.Document, out.Refs)
			if err != nil {
				return err
			}
			var e Entity
			if err = strictCommitDocument(raw, &e); err != nil {
				return err
			}
			if e.ReleaseID != "" {
				releaseIDs[e.ReleaseID] = true
			}
			if e.MediumID != "" {
				var rid string
				err = tx.QueryRowContext(ctx, `SELECT release_id FROM catalog.mediums WHERE id=$1`, e.MediumID).Scan(&rid)
				if err == nil {
					releaseIDs[rid] = true
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		rids := keysOf(releaseIDs)
		sort.Strings(rids)
		for _, rid := range rids {
			if err := lockRelease(ctx, tx, rid); err != nil {
				return err
			}
		}
		sort.Strings(existing)
		if len(existing) > 0 {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog.entities WHERE id=ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, pq.Array(existing))
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		for i, op := range in.Operations {
			item, err := s.applyCommitOperation(ctx, tx, op, in, u, out.Refs, i, &v)
			if err != nil {
				return &commitOperationError{i, err}
			}
			out.Items = append(out.Items, item)
		}
		if preview {
			return errPreviewRollback
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog.commits SET result=$2 WHERE id=$1`, in.ID, encode(out))
		return err
	})
	if errors.Is(err, errPreviewRollback) {
		return out, nil
	}
	if err != nil {
		return CommitReceipt{}, err
	}
	return out, nil
}

func (s *Store) applyCommitOperation(ctx context.Context, tx *sql.Tx, op CommitOperation, in CatalogCommit, u User, refs map[string]string, index int, defs *DefinitionConfig) (CommitItem, error) {
	item := CommitItem{Target: op.Target, ID: op.ID, Changed: true}
	var raw []byte
	version := int64(0)
	if op.Action == "create" {
		var err error
		raw, err = resolveCommitRefs(op.Document, refs)
		if err != nil {
			return item, err
		}
	} else {
		var baseRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT snapshot FROM catalog.revisions WHERE target_id=$1 AND version=$2 ORDER BY id DESC LIMIT 1`, op.ID, op.BaseVersion).Scan(&baseRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return item, fmt.Errorf("base_revision_missing")
			}
			return item, err
		}
		var current map[string]json.RawMessage
		if op.Target == "entity" {
			e, err := get(ctx, tx, op.ID)
			if err != nil {
				return item, err
			}
			if !visible(e, &u) || e.Status == "deleted" || e.Status == "merged" {
				return item, errForbidden
			}
			version = e.Version
			current = commitMap(e)
			for _, p := range op.Patch {
				if p.Path == "/contents" || strings.HasPrefix(p.Path, "/contents/") {
					readable, err := visibleEntityContents(ctx, tx, e, &u)
					if err != nil {
						return item, err
					}
					if len(readable.Contents) != len(e.Contents) {
						return item, fmt.Errorf("use_track_contents_endpoint")
					}
				}
			}
		} else {
			r, err := relationByID(ctx, tx, op.ID)
			if err != nil {
				return item, err
			}
			src, err := get(ctx, tx, r.SourceID)
			if err != nil {
				return item, err
			}
			tgt, err := get(ctx, tx, r.TargetID)
			if err != nil {
				return item, err
			}
			if !visible(src, &u) || !visible(tgt, &u) || !canWriteRelation(u, src) || !canAttachToTarget(u, tgt) {
				return item, errForbidden
			}
			version = r.Version
			current = commitMap(r)
		}
		if op.BaseVersion > version {
			return item, errVersionConflict
		}
		base := map[string]json.RawMessage{}
		if err := json.Unmarshal(baseRaw, &base); err != nil {
			return item, err
		}
		patch := make([]CommitPatch, len(op.Patch))
		copy(patch, op.Patch)
		for i := range patch {
			if !patch[i].Remove {
				val, err := resolveCommitRefs(patch[i].Value, refs)
				if err != nil {
					return item, err
				}
				patch[i].Value = val
			}
		}
		merged, conflicts, changed, err := mergeCommitPatch(base, current, patch)
		if err != nil {
			return item, err
		}
		if len(conflicts) > 0 {
			return item, &commitConflictError{CommitConflict{index, op.ID, op.BaseVersion, version, conflicts}}
		}
		item.Merged = op.BaseVersion != version
		item.Changed = changed
		item.Version = version
		if !changed && !commitNeedsIdentityReview(op) {
			return item, nil
		}
		raw = []byte(encode(merged))
	}
	if op.Target == "entity" {
		var e Entity
		if err := strictCommitDocument(raw, &e); err != nil {
			return item, err
		}
		input := Edit{Entity: e, ExpectedVersion: version, EditNote: in.EditNote, Sources: in.Sources, definitions: defs, identityLocksHeld: true}
		if e.Kind == "track" && op.Action == "update" {
			input.preserveContents = true
			for _, p := range op.Patch {
				if p.Path == "/contents" || strings.HasPrefix(p.Path, "/contents/") {
					input.preserveContents = false
				}
			}
		}
		if op.Action == "create" {
			if e.ID != "" || e.Version != 0 || e.CreatedBy != "" || e.RedirectID != "" {
				return item, fmt.Errorf("server_fields_forbidden")
			}
			input.createID = refs[op.Ref]
		}
		if commitNeedsIdentityReview(op) {
			if err := reviewEntityIdentity(ctx, tx, e, op.ReviewedCandidateIDs, u, refs, index); err != nil {
				return item, err
			}
		}
		if !item.Changed {
			return item, nil
		}
		saved, err := s.saveEntityTx(ctx, tx, input, u)
		if err != nil {
			return item, err
		}
		item.ID, item.Version = saved.ID, saved.Version
	} else {
		var r Relation
		if err := strictCommitDocument(raw, &r); err != nil {
			return item, err
		}
		input := RelationEdit{Relation: r, ExpectedVersion: version, EditNote: in.EditNote, Sources: in.Sources, definitions: defs}
		if op.Action == "create" {
			if r.ID != "" || r.Version != 0 || r.Via != "" {
				return item, fmt.Errorf("server_fields_forbidden")
			}
			input.createID = refs[op.Ref]
		}
		saved, err := s.saveRelationTx(ctx, tx, input, u)
		if err != nil {
			return item, err
		}
		item.ID, item.Version = saved.ID, saved.Version
	}
	return item, nil
}

func commitIdentityQuery(e Entity) IdentityCandidateQuery {
	q := IdentityCandidateQuery{Kind: e.Kind, Titles: []string{e.Title}}
	for _, translation := range e.Translations {
		if translation.Title != "" {
			q.Titles = append(q.Titles, translation.Title)
		}
	}
	for provider, value := range e.ExternalIDs {
		q.ExternalIDs = append(q.ExternalIDs, IdentityExternalCriterion{Provider: provider, Value: value})
	}
	switch e.Kind {
	case "expression", "content_unit":
		q.WorkID = e.WorkID
	case "medium":
		q.ReleaseID = e.ReleaseID
	case "track":
		q.MediumID = e.MediumID
	}
	return q
}

func (s *Store) CommitReceipt(ctx context.Context, id string, u User) (CommitReceipt, error) {
	var raw []byte
	if _, err := uuid.Parse(id); err != nil {
		return CommitReceipt{}, fmt.Errorf("invalid_commit_id")
	}
	err := s.DB.QueryRowContext(ctx, `SELECT result FROM catalog.commits WHERE id=$1 AND actor_id=$2`, id, u.ID).Scan(&raw)
	if err != nil {
		return CommitReceipt{}, err
	}
	var out CommitReceipt
	err = json.Unmarshal(raw, &out)
	return out, err
}

type CheckoutRequest struct {
	EntityIDs   []string `json:"entity_ids"`
	RelationIDs []string `json:"relation_ids"`
}
type CatalogCheckout struct {
	ActorID         string     `json:"actor_id"`
	DefinitionsETag string     `json:"definitions_etag"`
	Entities        []Entity   `json:"entities"`
	Relations       []Relation `json:"relations"`
}

func (s *Store) Checkout(ctx context.Context, in CheckoutRequest, u User) (CatalogCheckout, error) {
	if len(in.EntityIDs)+len(in.RelationIDs) > 100 {
		return CatalogCheckout{}, fmt.Errorf("invalid_checkout_size")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return CatalogCheckout{}, err
	}
	defer tx.Rollback()
	v, err := definitions(ctx, tx)
	if err != nil {
		return CatalogCheckout{}, err
	}
	out := CatalogCheckout{ActorID: u.ID, DefinitionsETag: v.ETag, Entities: []Entity{}, Relations: []Relation{}}

	seen := map[string]bool{}
	for _, id := range append(append([]string{}, in.EntityIDs...), in.RelationIDs...) {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return out, fmt.Errorf("invalid_id")
		}
		if seen[id] {
			return out, fmt.Errorf("duplicate_checkout_id")
		}
		seen[id] = true
	}
	relationMap := map[string]Relation{}
	endpointIDs := append([]string{}, in.EntityIDs...)
	if len(in.RelationIDs) > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT document FROM catalog.relations WHERE id=ANY($1::uuid[])`, pq.Array(in.RelationIDs))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return out, err
			}
			var r Relation
			if err = json.Unmarshal(raw, &r); err != nil {
				rows.Close()
				return out, err
			}
			relationMap[r.ID] = r
			endpointIDs = append(endpointIDs, r.SourceID, r.TargetID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	entities, err := getManyFrom(ctx, tx, endpointIDs, &u)
	if err != nil {
		return out, err
	}
	for _, id := range in.EntityIDs {
		e, ok := entities[id]
		if !ok || e.Status == "deleted" || e.Status == "merged" {
			return out, sql.ErrNoRows
		}
		out.Entities = append(out.Entities, e)
	}
	for _, id := range in.RelationIDs {
		r, ok := relationMap[id]
		_, src := entities[r.SourceID]
		_, tgt := entities[r.TargetID]
		if !ok || !src || !tgt {
			return out, sql.ErrNoRows
		}
		out.Relations = append(out.Relations, r)
	}

	return out, tx.Commit()
}
