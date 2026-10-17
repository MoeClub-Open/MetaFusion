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
type CommitContentChange struct {
	Action    string     `json:"action"`
	Position  int        `json:"position"`
	Inclusion *Inclusion `json:"inclusion,omitempty"`
}

type CommitOperation struct {
	ContentChange        *CommitContentChange `json:"content_change,omitempty"`
	Target               string               `json:"target"`
	Action               string               `json:"action"`
	Ref                  string               `json:"ref,omitempty"`
	ID                   string               `json:"id,omitempty"`
	BaseVersion          int64                `json:"base_version,omitempty"`
	Document             json.RawMessage      `json:"document,omitempty"`
	Patch                []CommitPatch        `json:"patch,omitempty"`
	ReviewedCandidateIDs []string             `json:"reviewed_candidate_ids"`
	// SourceRefs 声明本操作由来源池里哪些来源支撑（可细到字段）；必填，至少一项。
	SourceRefs []SourceRef `json:"source_refs"`
}

// SourceRef 引用 CatalogCommit.Sources 中 key 相同的来源；Pointers 为空表示支撑整个操作。
type SourceRef struct {
	Source   string   `json:"source"`
	Pointers []string `json:"pointers,omitempty"`
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
	ID             string            `json:"id"`
	Applied        bool              `json:"applied"`
	Refs           map[string]string `json:"refs"`
	Items          []CommitItem      `json:"items"`
	ReviewStatus   string            `json:"review_status"`
	ReviewDecision string            `json:"review_decision"`
	ReviewedBy     string            `json:"reviewed_by,omitempty"`
	ReviewedAt     *time.Time        `json:"reviewed_at,omitempty"`
	ReviewNote     string            `json:"review_note,omitempty"`
	Preview        []CommitPreview   `json:"preview,omitempty"`
	// QualityWarnings 只在 preview 返回：对受影响实体的自动质量诊断，纯建议，不阻止提交。
	QualityWarnings []QualityIssue `json:"quality_warnings,omitempty"`
}

// FieldConflict is one divergent JSON Pointer from a three-way merge.
// Values are decoded JSON (null when the pointer was absent). Suggestions use
// the project vocabulary: manual_merge, take_current, rebase_keep_mine.
type FieldConflict struct {
	Operation   int      `json:"operation"`
	Pointer     string   `json:"pointer"`
	Base        any      `json:"base"`
	Current     any      `json:"current"`
	Proposed    any      `json:"proposed"`
	Remove      bool     `json:"remove,omitempty"`
	Kind        string   `json:"kind"`
	Suggestions []string `json:"suggestions"`
}

// CommitConflict is the 409 commit_conflict payload. Paths were replaced by
// Conflicts (no compatibility shim): each entry carries mine/current values and
// suggested resolutions for the three-column compare UI.
type CommitConflict struct {
	Operation      int             `json:"operation"`
	ID             string          `json:"id"`
	BaseVersion    int64           `json:"base_version"`
	CurrentVersion int64           `json:"current_version"`
	Conflicts      []FieldConflict `json:"conflicts"`
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

var entityCommitFields = strings.Fields("title original_language translations attributes external_links pictures content_unit_id parent_id position number contents subjects")
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
		if op.Action != "content" && op.ContentChange != nil {
			return fail(fmt.Errorf("invalid_content_operation"))
		}
		switch op.Action {
		case "content":
			id, err := uuid.Parse(op.ID)
			c := op.ContentChange
			if op.Target != "entity" || err != nil || id.String() != op.ID || op.BaseVersion < 1 || op.Ref != "" || len(op.Document) != 0 || len(op.Patch) != 0 || op.ReviewedCandidateIDs != nil || c == nil {
				return fail(fmt.Errorf("invalid_content_operation"))
			}
			if !contains([]string{"add", "replace", "delete"}, c.Action) || c.Position < 0 || c.Action != "delete" && c.Inclusion == nil || c.Action == "delete" && c.Inclusion != nil {
				return fail(fmt.Errorf("invalid_content_operation"))
			}
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
		case "delete":
			id, err := uuid.Parse(op.ID)
			if op.Target != "relation" || err != nil || id.String() != op.ID || op.BaseVersion < 1 || op.Ref != "" || len(op.Document) != 0 || len(op.Patch) != 0 || op.ReviewedCandidateIDs != nil {
				return fail(fmt.Errorf("invalid_delete_operation"))
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

// decodePointerValue turns a present/absent RawMessage into a JSON value for
// conflict payloads (absent → null).
func decodePointerValue(v json.RawMessage, ok bool) any {
	if !ok {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(v, &decoded); err != nil {
		return nil
	}
	return decoded
}

// fieldConflictSuggestions picks resolution hints for one divergent pointer.
// Vocabulary: manual_merge (inspect both), take_current (accept server),
// rebase_keep_mine (re-checkout then re-apply proposed).
func fieldConflictSuggestions(kind string) []string {
	switch kind {
	case "stale_base":
		return []string{"take_current", "rebase_keep_mine"}
	default:
		return []string{"manual_merge", "take_current", "rebase_keep_mine"}
	}
}

func mergeCommitPatch(base, current map[string]json.RawMessage, patch []CommitPatch) (map[string]json.RawMessage, []FieldConflict, bool, error) {
	out := map[string]json.RawMessage{}
	for k, v := range current {
		out[k] = v
	}
	conflicts := []FieldConflict{}
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
		proposedOK := !p.Remove
		if equalCommitValue(c, cok, p.Value, proposedOK) {
			continue
		}
		if !equalCommitValue(b, bok, c, cok) {
			kind := "concurrent_edit"
			if equalCommitValue(b, bok, p.Value, proposedOK) {
				kind = "stale_base"
			}
			conflicts = append(conflicts, FieldConflict{
				Pointer:     p.Path,
				Base:        decodePointerValue(b, bok),
				Current:     decodePointerValue(c, cok),
				Proposed:    decodePointerValue(p.Value, proposedOK),
				Remove:      p.Remove,
				Kind:        kind,
				Suggestions: fieldConflictSuggestions(kind),
			})
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
	if u.ID == "" || u.IsThirdParty {
		return CommitReceipt{}, errForbidden
	}
	if err := validateCommit(in); err != nil {
		return CommitReceipt{}, err
	}
	if err := validateCommitSourceRefs(in); err != nil {
		return CommitReceipt{}, err
	}
	var err error
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	review, approving := ctx.Value(commitReviewContextKey{}).(commitReviewApproval)
	queued := !preview && !approving && !u.CanBypassReview()
	var out CommitReceipt
	err = s.writeCatalog(ctx, func(tx *sql.Tx) error {
		// A confirmed serialization rollback restarts the complete commit. Never
		// retain refs, items or a receipt assembled by the abandoned attempt.
		out = CommitReceipt{ID: in.ID, Applied: !preview, Refs: map[string]string{}, Items: []CommitItem{}}
		out.ReviewStatus, out.ReviewDecision = "reviewed", "exempt"
		if queued || preview {
			out.ReviewStatus, out.ReviewDecision = "pending_review", "pending"
		}
		if approving {
			now := time.Now().UTC()
			out.ReviewedBy, out.ReviewedAt, out.ReviewNote = review.Reviewer.ID, &now, review.Note
			out.ReviewDecision = "approved"
		}
		if !preview {
			res, err := tx.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot) VALUES($1,$2,$3,$4,'null',$5) ON CONFLICT DO NOTHING`, in.ID, u.ID, requestHash(in), encode(in), encode(commitAuthor{u.ID, u.Username, u.Permissions}))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				var actor, hash string
				var raw []byte
				if err = tx.QueryRowContext(ctx, `SELECT actor_id,request_hash,result FROM catalog.commits WHERE id=$1 FOR UPDATE`, in.ID).Scan(&actor, &hash, &raw); err != nil {
					return err
				}
				if actor != u.ID || hash != requestHash(in) {
					return errIdempotencyConflict
				}
				if string(raw) == "null" {
					return fmt.Errorf("commit_incomplete")
				}
				var prior CommitReceipt
				if err = json.Unmarshal(raw, &prior); err != nil {
					return err
				}
				if !approving || prior.ReviewStatus != "pending_review" {
					out = prior
					return nil
				}
				out.Refs, out.Preview = prior.Refs, prior.Preview
			}
			if _, err = tx.ExecContext(ctx, `SELECT set_config('metafusion.commit_id',$1,true)`, in.ID); err != nil {
				return err
			}
		}
		// Validate the entire proposal using the real write path, then discard
		// every fact/revision/outbox/notification while retaining its receipt.
		if queued {
			if _, err := tx.ExecContext(ctx, "SAVEPOINT proposed_facts"); err != nil {
				return err
			}
		}
		// Determine scoped locks before taking rows. There is no global structure
		// lock: SSI detects inconsistent graph and lifecycle dependencies.
		existing := []string{}
		releaseIDs := map[string]bool{}
		for _, op := range in.Operations {
			if op.Action == "create" {
				if out.Refs[op.Ref] == "" {
					out.Refs[op.Ref] = newID()
				}
			}
			if op.Target == "relation" {
				continue
			}
			if op.Action == "update" || op.Action == "content" {
				e, err := get(ctx, tx, op.ID)
				if err != nil {
					return err
				}
				if !visible(e, &u) || e.State == "deleted" || e.State == "merged" {
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
			var before any
			if !approving && op.Action != "create" {
				if op.Target == "entity" {
					before, err = commitPreviewEntity(ctx, tx, op.ID, u)
				} else {
					before, err = relationByID(ctx, tx, op.ID)
				}
				if err != nil {
					return err
				}
			}
			item, err := s.applyCommitOperation(ctx, tx, op, in, u, out.Refs, i, &v)
			if err != nil {
				return &commitOperationError{i, err}
			}
			out.Items = append(out.Items, item)
			if !approving {
				var after any
				if op.Action != "delete" {
					if op.Target == "entity" {
						after, err = commitPreviewEntity(ctx, tx, item.ID, u)
					} else {
						after, err = relationByID(ctx, tx, item.ID)
					}
					if err != nil {
						return err
					}
				}
				out.Preview = append(out.Preview, CommitPreview{op.Target, op.Action, item.ID, frozenCommitPreview(before), frozenCommitPreview(after)})
			}
		}
		if preview {
			out.QualityWarnings = commitQualityWarnings(ctx, tx, v, out.Items, &u)
			return errPreviewRollback
		}
		if queued {
			if _, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT proposed_facts"); err != nil {
				return err
			}
			out.Applied = false
		}
		if approving {
			if err = notifyCommitReview(ctx, tx, in.ID, u, review.Reviewer, true, review.Note); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog.commits SET result=$2,review_status=$3,review_decision=$4,reviewed_by=$5,reviewed_at=$6,review_note=$7 WHERE id=$1`, in.ID, encode(out), out.ReviewStatus, out.ReviewDecision, out.ReviewedBy, out.ReviewedAt, out.ReviewNote)
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
	if op.Target == "relation" && !u.Can(PermissionRelationEdit) {
		return item, errForbidden
	}
	if op.Action == "content" {
		e, err := get(ctx, tx, op.ID)
		if err != nil {
			return item, err
		}
		if e.Version != op.BaseVersion {
			return item, errVersionConflict
		}
		if !visible(e, &u) || !canEditEntity(u, e) {
			return item, errForbidden
		}
		c := op.ContentChange
		saved, err := s.saveEntityTx(ctx, tx, Edit{Entity: Entity{ID: e.ID, Kind: e.Kind}, ExpectedVersion: op.BaseVersion, EditNote: in.EditNote, Sources: operationSources(in, op), definitions: defs, identityLocksHeld: true, contentPatch: &trackContentPatch{action: c.Action, position: c.Position, inclusion: c.Inclusion}}, u)
		item.Version = saved.Version
		return item, err
	}

	if op.Action == "delete" {
		err := s.deleteRelationTx(ctx, tx, op.ID, op.BaseVersion, in.EditNote, operationSources(in, op), u)
		item.Version = op.BaseVersion + 1
		return item, err
	}
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
			if !visible(e, &u) || e.State == "deleted" || e.State == "merged" {
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
			for i := range conflicts {
				conflicts[i].Operation = index
			}
			return item, &commitConflictError{CommitConflict{Operation: index, ID: op.ID, BaseVersion: op.BaseVersion, CurrentVersion: version, Conflicts: conflicts}}
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
		input := Edit{Entity: e, ExpectedVersion: version, EditNote: in.EditNote, Sources: operationSources(in, op), definitions: defs, identityLocksHeld: true}
		if e.Kind == "track" && op.Action == "update" {
			input.preserveContents = true
			for _, p := range op.Patch {
				if p.Path == "/contents" || strings.HasPrefix(p.Path, "/contents/") {
					input.preserveContents = false
				}
			}
		}
		if op.Action == "create" {
			if e.ID != "" || e.Version != 0 || e.CreatedBy != "" || e.RedirectID != "" || e.State != "" || e.ReviewStatus != "" {
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
		input := RelationEdit{Relation: r, ExpectedVersion: version, EditNote: in.EditNote, Sources: operationSources(in, op), definitions: defs}
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
	for _, link := range e.ExternalLinks {
		value := link.ExternalID
		if value == "" {
			value = link.URL
		}
		if value == "" || link.Kind == "" {
			continue
		}
		q.ExternalLinks = append(q.ExternalLinks, IdentityExternalCriterion{Kind: link.Kind, Value: value})
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
	err := s.DB.QueryRowContext(ctx, `SELECT result FROM catalog.commits WHERE id=$1 AND (actor_id=$2 OR $3::boolean)`, id, u.ID, u.Can(PermissionReviewManage)).Scan(&raw)
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
		if !ok || e.State == "deleted" || e.State == "merged" {
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
