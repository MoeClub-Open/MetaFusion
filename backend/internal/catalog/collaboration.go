package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

func collaborationUser(u User) error {
	if u.ID == "" || u.IsThirdParty {
		return errForbidden
	}
	return nil
}
func collaborationID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.String() == id
}
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return string(raw)
}
func rawNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }
func equalDocument(a, b json.RawMessage) bool {
	if rawNull(a) || rawNull(b) {
		return rawNull(a) && rawNull(b)
	}
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && encode(av) == encode(bv)
}
func collaborationName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 200 || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid_workspace_name")
	}
	return name, nil
}

func workspaceFrom(ctx context.Context, q queryer, id string, u User, lock bool, write bool) (Workspace, error) {
	var w Workspace
	if err := collaborationUser(u); err != nil {
		return w, err
	}
	if !collaborationID(id) {
		return w, sql.ErrNoRows
	}
	query := `SELECT id::text,owner_id,name,version,head_commit_id::text,state,created_at,updated_at FROM catalog.workspaces WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	if err := q.QueryRowContext(ctx, query, id).Scan(&w.ID, &w.OwnerID, &w.Name, &w.Version, &w.HeadCommitID, &w.State, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return w, err
	}
	w.Members = []string{}
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM catalog.workspace_members WHERE workspace_id=$1 ORDER BY user_id`, id)
	if err != nil {
		return w, err
	}
	member := w.OwnerID == u.ID
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			rows.Close()
			return w, err
		}
		w.Members = append(w.Members, s)
		member = member || s == u.ID
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return w, err
	}
	if !member && (write || !u.Can(PermissionReviewManage)) {
		return Workspace{}, sql.ErrNoRows
	}
	if write && w.State != "active" {
		return w, fmt.Errorf("workspace_archived")
	}
	w.Objects = []WorkspaceObject{}
	rows, err = q.QueryContext(ctx, `SELECT target,target_id::text,base_version,COALESCE(base_document,'null'::jsonb),COALESCE(head_document,'null'::jsonb),COALESCE(document,'null'::jsonb) FROM catalog.workspace_objects WHERE workspace_id=$1 ORDER BY target,target_id`, id)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var o WorkspaceObject
		if err = rows.Scan(&o.Target, &o.ID, &o.BaseVersion, &o.Base, &o.Committed, &o.Document); err != nil {
			rows.Close()
			return w, err
		}
		o.Dirty = !equalDocument(o.Committed, o.Document)
		if o.Dirty {
			w.DirtyCount++
		}
		w.Objects = append(w.Objects, o)
	}
	err = rows.Err()
	rows.Close()
	return w, err
}
func checkWorkspace(w Workspace, version int64, head string) error {
	if version < 1 || version != w.Version {
		return fmt.Errorf("workspace_version_conflict")
	}
	if head != "" && (w.HeadCommitID == nil || *w.HeadCommitID != head) {
		return fmt.Errorf("workspace_head_conflict")
	}
	return nil
}
func bumpWorkspace(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog.workspaces SET version=version+1,updated_at=now() WHERE id=$1`, id)
	return err
}
func (s *Store) CreateWorkspace(ctx context.Context, in WorkspaceCreate, u User) (Workspace, error) {
	if err := collaborationUser(u); err != nil {
		return Workspace{}, err
	}
	name, err := collaborationName(in.Name)
	if err != nil {
		return Workspace{}, err
	}
	id := newID()
	var out Workspace
	err = s.writeCatalog(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.workspaces(id,owner_id,name) VALUES($1,$2,$3)`, id, u.ID, name); err != nil {
			return err
		}
		if err := s.checkoutWorkspaceObjects(ctx, tx, id, in.EntityIDs, in.RelationIDs, u); err != nil {
			return err
		}
		var err error
		out, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}
func (s *Store) checkoutWorkspaceObjects(ctx context.Context, tx *sql.Tx, wid string, entityIDs, relationIDs []string, u User) error {
	if len(entityIDs)+len(relationIDs) > 100 {
		return fmt.Errorf("invalid_checkout_size")
	}
	for _, target := range []string{"entity", "relation"} {
		ids := entityIDs
		if target == "relation" {
			ids = relationIDs
		}
		for _, id := range ids {
			if !collaborationID(id) {
				return fmt.Errorf("invalid_id")
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.workspace_objects WHERE workspace_id=$1 AND target=$2 AND target_id=$3)`, wid, target, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			raw, version, err := workspaceMainObject(ctx, tx, target, id, u)
			if err != nil {
				return err
			}
			if rawNull(raw) {
				return sql.ErrNoRows
			}
			if target == "entity" {
				var e Entity
				_ = json.Unmarshal(raw, &e)
				if !canEditEntity(u, e) {
					return errForbidden
				}
			} else if !u.Can(PermissionRelationEdit) {
				return errForbidden
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document) VALUES($1,$2,$3,$4,$5,$5,$5)`, wid, target, id, version, string(raw)); err != nil {
				return err
			}
			if target == "entity" {
				if err = s.reserveWorkspaceCoverAssets(ctx, wid, raw, raw, u); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func workspaceMainObject(ctx context.Context, q queryer, target, id string, u User) (json.RawMessage, int64, error) {
	if target == "entity" {
		e, err := get(ctx, q, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		if !visible(e, &u) || e.State != "active" {
			return nil, e.Version, nil
		}
		e, err = visibleEntityContents(ctx, q, e, &u)
		if err != nil {
			return nil, 0, err
		}
		return json.RawMessage(encode(e)), e.Version, nil
	}
	r, err := relationByID(ctx, q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	for _, eid := range []string{r.SourceID, r.TargetID} {
		e, err := get(ctx, q, eid)
		if err != nil {
			return nil, 0, err
		}
		if !visible(e, &u) || e.State != "active" {
			return nil, r.Version, nil
		}
	}
	return json.RawMessage(encode(r)), r.Version, nil
}
func (s *Store) EditWorkspace(ctx context.Context, id string, in WorkspaceEdit, u User) (Workspace, error) {
	var out Workspace
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, false)
		if err != nil {
			return err
		}
		if w.OwnerID != u.ID {
			return errForbidden
		}
		if err = checkWorkspace(w, in.ExpectedVersion, ""); err != nil {
			return err
		}
		if in.Name != nil {
			v, e := collaborationName(*in.Name)
			if e != nil {
				return e
			}
			w.Name = v
		}
		if in.State != nil {
			if *in.State != "active" && *in.State != "archived" {
				return fmt.Errorf("invalid_workspace_state")
			}
			w.State = *in.State
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspaces SET name=$2,state=$3,version=version+1,updated_at=now() WHERE id=$1`, id, w.Name, w.State); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}
func (s *Store) CheckoutWorkspace(ctx context.Context, id string, in WorkspaceCheckout, u User) (Workspace, error) {
	var out Workspace
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		if err = checkWorkspace(w, in.ExpectedVersion, ""); err != nil {
			return err
		}
		if err = s.checkoutWorkspaceObjects(ctx, tx, id, in.EntityIDs, in.RelationIDs, u); err != nil {
			return err
		}
		if err = bumpWorkspace(ctx, tx, id); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}
func (s *Store) SetWorkspaceMembers(ctx context.Context, id string, in WorkspaceMembersEdit, u User) (Workspace, error) {
	var out Workspace
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		if w.OwnerID != u.ID {
			return errForbidden
		}
		if err = checkWorkspace(w, in.ExpectedVersion, ""); err != nil {
			return err
		}
		if len(in.UserIDs) > 100 {
			return fmt.Errorf("invalid_workspace_members")
		}
		seen := map[string]bool{}
		for _, uid := range in.UserIDs {
			if !collaborationID(uid) || seen[uid] {
				return fmt.Errorf("invalid_workspace_members")
			}
			seen[uid] = true
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog.workspace_members WHERE workspace_id=$1`, id); err != nil {
			return err
		}
		for uid := range seen {
			if uid == w.OwnerID {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_members(workspace_id,user_id) VALUES($1,$2)`, id, uid); err != nil {
				return err
			}
		}
		if err = bumpWorkspace(ctx, tx, id); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}

func workspaceEditableMap(raw json.RawMessage, target string) map[string]json.RawMessage {
	all := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &all)
	allowed := entityCommitFields
	if target == "relation" {
		allowed = relationCommitFields
	}
	out := map[string]json.RawMessage{}
	for _, k := range allowed {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out
}
func normalizeWorkspaceDocument(raw, current json.RawMessage, target, id string, create bool) (json.RawMessage, error) {
	if rawNull(raw) {
		return nil, fmt.Errorf("invalid_document")
	}
	if target == "entity" {
		var e Entity
		if err := strictCommitDocument(raw, &e); err != nil {
			return nil, err
		}
		if create {
			if e.ID != "" || e.Version != 0 || e.CreatedBy != "" || e.State != "" || e.ReviewStatus != "" || e.RedirectID != "" {
				return nil, fmt.Errorf("server_fields_forbidden")
			}
			if !contains(Kinds, e.Kind) {
				return nil, fmt.Errorf("invalid_kind")
			}
			e.ID = id
			return json.RawMessage(encode(e)), nil
		}
		var old Entity
		if err := json.Unmarshal(current, &old); err != nil {
			return nil, err
		}
		if (e.ID != "" && e.ID != id) || e.Kind != old.Kind || e.WorkID != old.WorkID || e.ReleaseID != old.ReleaseID || e.MediumID != old.MediumID {
			return nil, fmt.Errorf("immutable_scope")
		}
		out := commitMap(old)
		for _, key := range entityCommitFields {
			delete(out, key)
		}
		for key, value := range workspaceEditableMap(raw, target) {
			out[key] = value
		}
		return json.RawMessage(encode(out)), nil
	}
	var r Relation
	if err := strictCommitDocument(raw, &r); err != nil {
		return nil, err
	}
	if create {
		if r.ID != "" || r.Version != 0 {
			return nil, fmt.Errorf("server_fields_forbidden")
		}
		r.ID = id
		return json.RawMessage(encode(r)), nil
	}
	var old Relation
	if err := json.Unmarshal(current, &old); err != nil {
		return nil, err
	}
	if (r.ID != "" && r.ID != id) || r.SourceID != old.SourceID || r.TargetID != old.TargetID || r.Type != old.Type {
		return nil, fmt.Errorf("immutable_scope")
	}
	r.ID = id
	r.Version = old.Version
	return json.RawMessage(encode(r)), nil
}
func workspaceContent(raw json.RawMessage, c *CommitContentChange) (json.RawMessage, error) {
	var e Entity
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	if e.Kind != "track" || c == nil || c.Position < 0 {
		return nil, fmt.Errorf("invalid_content_operation")
	}
	index := -1
	for i, item := range e.Contents {
		if item.Position == c.Position {
			index = i
			break
		}
	}
	switch c.Action {
	case "add":
		if c.Inclusion == nil || index >= 0 || c.Inclusion.Position != c.Position {
			return nil, fmt.Errorf("invalid_content_operation")
		}
		e.Contents = append(e.Contents, *c.Inclusion)
	case "replace":
		if c.Inclusion == nil || index < 0 {
			return nil, fmt.Errorf("invalid_content_operation")
		}
		for i, item := range e.Contents {
			if i != index && item.Position == c.Inclusion.Position {
				return nil, fmt.Errorf("invalid_content_operation")
			}
		}
		e.Contents[index] = *c.Inclusion
	case "delete":
		if c.Inclusion != nil || index < 0 {
			return nil, fmt.Errorf("invalid_content_operation")
		}
		e.Contents = append(e.Contents[:index], e.Contents[index+1:]...)
	default:
		return nil, fmt.Errorf("invalid_content_operation")
	}
	return json.RawMessage(encode(e)), nil
}

func workspacePermissionEntity(ctx context.Context, q queryer, wid, id string, u User) (Entity, error) {
	var raw json.RawMessage
	err := q.QueryRowContext(ctx, `SELECT COALESCE(document,'null'::jsonb) FROM catalog.workspace_objects WHERE workspace_id=$1 AND target='entity' AND target_id=$2`, wid, id).Scan(&raw)
	if err == nil {
		if rawNull(raw) {
			return Entity{}, sql.ErrNoRows
		}
		var e Entity
		if err = json.Unmarshal(raw, &e); err != nil {
			return e, err
		}
		if e.State == "" {
			e.State = "active"
		}
		return e, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Entity{}, err
	}
	if planned, ok := ctx.Value(collaborationReferencesKey{}).(map[string]Entity); ok {
		if e, found := planned[id]; found {
			return e, nil
		}
	}
	e, err := get(ctx, q, id)
	if err != nil {
		return e, err
	}
	if !visible(e, &u) || e.State != "active" {
		return e, sql.ErrNoRows
	}
	return e, nil
}
func workspaceAuthorizeObject(ctx context.Context, q queryer, wid, target string, raw json.RawMessage, u User) error {
	if target == "entity" {
		if u.FromPAT && !u.Can(PermissionEntityEdit) {
			return errForbidden
		}
		var e Entity
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		if !canEditEntity(u, e) {
			return errForbidden
		}
		return nil
	}
	if !u.Can(PermissionRelationEdit) {
		return errForbidden
	}
	var r Relation
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	for index, id := range []string{r.SourceID, r.TargetID} {
		e, err := workspacePermissionEntity(ctx, q, wid, id, u)
		if err != nil {
			return err
		}
		if index == 0 && !canWriteRelation(u, e) || index == 1 && !canAttachToTarget(u, e) {
			return errForbidden
		}
	}
	return nil
}
func workspaceAuthorizeSubmission(ctx context.Context, q queryer, w Workspace, u User, dirtyOnly bool) error {
	if u.FromPAT && !u.Can(PermissionEntityEdit) && !u.Can(PermissionRelationEdit) {
		return errForbidden
	}
	for _, o := range w.Objects {
		raw := o.Committed
		if dirtyOnly {
			if !o.Dirty {
				continue
			}
			raw = o.Document
		} else if workspaceSameFact(o.Base, o.Committed, o.Target) {
			continue
		}
		if rawNull(raw) {
			raw = o.Base
		}
		if rawNull(raw) {
			continue
		}
		if err := workspaceAuthorizeObject(ctx, q, w.ID, o.Target, raw, u); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SaveWorkspaceChanges(ctx context.Context, id string, in WorkspaceChanges, u User) (WorkspaceChangesResult, error) {
	out := WorkspaceChangesResult{Refs: map[string]string{}}
	if len(in.Operations) == 0 || len(in.Operations) > 100 {
		return out, fmt.Errorf("invalid_commit_size")
	}
	// IDs belong to the operation, not a retry attempt or preview. This makes
	// references stable across later saves and commits in the same workspace.
	for _, op := range in.Operations {
		if op.Action == "create" {
			if !commitRef.MatchString(op.Ref) || out.Refs[op.Ref] != "" {
				return out, fmt.Errorf("invalid_local_reference")
			}
			out.Refs[op.Ref] = newID()
		}
	}
	planned := map[string]Entity{}
	for _, op := range in.Operations {
		if op.Target != "entity" || op.Action != "create" {
			continue
		}
		raw, err := resolveCommitRefs(op.Document, out.Refs)
		if err != nil {
			return out, err
		}
		normalized, err := normalizeWorkspaceDocument(raw, nil, "entity", out.Refs[op.Ref], true)
		if err != nil {
			return out, err
		}
		var e Entity
		if err = json.Unmarshal(normalized, &e); err != nil {
			return out, err
		}
		e.CreatedBy = u.ID
		e.State = "active"
		planned[e.ID] = e
	}
	ctx = context.WithValue(ctx, collaborationReferencesKey{}, planned)
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		if err = checkWorkspace(w, in.ExpectedVersion, ""); err != nil {
			return err
		}
		for index, op := range in.Operations {
			fail := func(e error) error { return &commitOperationError{index, e} }
			if op.Target != "entity" && op.Target != "relation" {
				return fail(fmt.Errorf("invalid_commit_target"))
			}
			if op.Target == "relation" && !u.Can(PermissionRelationEdit) {
				return fail(errForbidden)
			}
			if op.Target == "entity" && u.FromPAT && !u.Can(PermissionEntityEdit) {
				return fail(errForbidden)
			}
			oid := op.ID
			if strings.HasPrefix(oid, "@local:") {
				oid = out.Refs[strings.TrimPrefix(oid, "@local:")]
			}
			if op.Action == "create" {
				if op.ID != "" || len(op.Patch) > 0 || op.ContentChange != nil {
					return fail(fmt.Errorf("invalid_create_operation"))
				}
				oid = out.Refs[op.Ref]
				if !u.Can(PermissionEntityEdit) && op.Target == "entity" {
					return fail(errForbidden)
				}
			} else if !collaborationID(oid) {
				return fail(fmt.Errorf("invalid_id"))
			}
			var base, head, current json.RawMessage
			var version int64
			if op.Action != "create" {
				err = tx.QueryRowContext(ctx, `SELECT base_version,COALESCE(base_document,'null'::jsonb),COALESCE(head_document,'null'::jsonb),COALESCE(document,'null'::jsonb) FROM catalog.workspace_objects WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, id, op.Target, oid).Scan(&version, &base, &head, &current)
				if errors.Is(err, sql.ErrNoRows) {
					eids, rids := []string{}, []string{}
					if op.Target == "entity" {
						eids = append(eids, oid)
					} else {
						rids = append(rids, oid)
					}
					if err = s.checkoutWorkspaceObjects(ctx, tx, id, eids, rids, u); err != nil {
						return fail(err)
					}
					err = tx.QueryRowContext(ctx, `SELECT base_version,COALESCE(base_document,'null'::jsonb),COALESCE(head_document,'null'::jsonb),COALESCE(document,'null'::jsonb) FROM catalog.workspace_objects WHERE workspace_id=$1 AND target=$2 AND target_id=$3`, id, op.Target, oid).Scan(&version, &base, &head, &current)
				}
				if err != nil {
					return fail(err)
				}
				if rawNull(current) {
					return fail(fmt.Errorf("workspace_object_deleted"))
				}
				if op.Target == "entity" {
					var e Entity
					_ = json.Unmarshal(current, &e)
					if !canEditEntity(u, e) {
						return fail(errForbidden)
					}
				}
			}
			var next json.RawMessage
			switch op.Action {
			case "create":
				raw, e := resolveCommitRefs(op.Document, out.Refs)
				if e != nil {
					return fail(e)
				}
				next, err = normalizeWorkspaceDocument(raw, nil, op.Target, oid, true)
				if err == nil && op.Target == "entity" {
					var e Entity
					_ = json.Unmarshal(next, &e)
					e.CreatedBy = u.ID
					next = json.RawMessage(encode(e))
				}
			case "update":
				if (len(op.Document) == 0) == (len(op.Patch) == 0) || op.ContentChange != nil {
					return fail(fmt.Errorf("invalid_update_operation"))
				}
				if len(op.Document) > 0 {
					raw, e := resolveCommitRefs(op.Document, out.Refs)
					if e != nil {
						return fail(e)
					}
					next, err = normalizeWorkspaceDocument(raw, current, op.Target, oid, false)
				} else {
					doc := map[string]json.RawMessage{}
					_ = json.Unmarshal(current, &doc)
					allowed := entityCommitFields
					if op.Target == "relation" {
						allowed = relationCommitFields
					}
					if len(op.Patch) > 200 {
						return fail(fmt.Errorf("invalid_commit_size"))
					}
					for _, p := range op.Patch {
						parts, e := pointerParts(p.Path)
						if e != nil || !contains(allowed, parts[0]) || p.Remove && len(p.Value) > 0 || !p.Remove && !json.Valid(p.Value) {
							return fail(fmt.Errorf("invalid_commit_patch"))
						}
						value := p.Value
						if !p.Remove {
							value, e = resolveCommitRefs(value, out.Refs)
							if e != nil {
								return fail(e)
							}
						}
						if e = putPointer(doc, parts, value, p.Remove); e != nil {
							return fail(e)
						}
					}
					next = json.RawMessage(encode(doc))
				}
			case "content":
				if op.Target != "entity" || len(op.Document) > 0 || len(op.Patch) > 0 {
					return fail(fmt.Errorf("invalid_content_operation"))
				}
				next, err = workspaceContent(current, op.ContentChange)
			case "delete":
				if len(op.Document) > 0 || len(op.Patch) > 0 || op.ContentChange != nil {
					return fail(fmt.Errorf("invalid_delete_operation"))
				}
				if op.Target == "entity" && !rawNull(base) {
					return fail(fmt.Errorf("use_lifecycle_endpoint"))
				}
			default:
				return fail(fmt.Errorf("invalid_commit_action"))
			}
			if err != nil {
				return fail(err)
			}
			if op.Target == "relation" {
				authDoc := next
				if rawNull(authDoc) {
					authDoc = current
				}
				if err = workspaceAuthorizeObject(ctx, tx, id, op.Target, authDoc, u); err != nil {
					return fail(err)
				}
			}
			if op.Target == "entity" {
				if err = s.reserveWorkspaceCoverAssets(ctx, id, current, next, u); err != nil {
					return fail(err)
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document,staged_authors) VALUES($1,$2,$3,$4,$5,$6,$7,jsonb_build_object($8::text,$9::text)) ON CONFLICT(workspace_id,target,target_id) DO UPDATE SET document=EXCLUDED.document,staged_authors=CASE WHEN catalog.workspace_objects.head_document IS NOT DISTINCT FROM EXCLUDED.document THEN '{}'::jsonb ELSE catalog.workspace_objects.staged_authors || EXCLUDED.staged_authors END,updated_at=now()`, id, op.Target, oid, version, nullableJSON(base), nullableJSON(head), nullableJSON(next), u.ID, u.Username); err != nil {
				return err
			}
		}
		if err = bumpWorkspace(ctx, tx, id); err != nil {
			return err
		}
		out.Workspace, err = workspaceFrom(ctx, tx, id, u, false, false)
		return err
	})
	return out, err
}

func insertWorkspaceCommit(ctx context.Context, tx *sql.Tx, w Workspace, id, message, kind string, request any, u User) (CollaborationCommit, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,workspace_id,parent_id,message,kind) VALUES($1,$2,$3,$4,'{}',$5,$6,$7,$8,$9)`, id, u.ID, requestHash(request), encode(request), encode(commitAuthor{ID: u.ID, Username: u.Username}), w.ID, w.HeadCommitID, message, kind); err != nil {
		return CollaborationCommit{}, err
	}
	_, rebasing := request.(WorkspaceRebase)
	for _, o := range w.Objects {
		if !o.Dirty && !rebasing {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, o.Target, o.ID, o.BaseVersion, nullableJSON(o.Base), nullableJSON(o.Committed), nullableJSON(o.Document)); err != nil {
			return CollaborationCommit{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name) SELECT $2,key,value FROM catalog.workspace_objects o CROSS JOIN LATERAL jsonb_each_text(o.staged_authors) WHERE o.workspace_id=$1 AND o.head_document IS DISTINCT FROM o.document ON CONFLICT DO NOTHING`, w.ID, id); err != nil {
		return CollaborationCommit{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, u.ID, u.Username); err != nil {
		return CollaborationCommit{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET head_document=document,staged_authors='{}',updated_at=now() WHERE workspace_id=$1`, w.ID); err != nil {
		return CollaborationCommit{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog.workspaces SET head_commit_id=$2,version=version+1,updated_at=now() WHERE id=$1`, w.ID, id); err != nil {
		return CollaborationCommit{}, err
	}
	return collaborationCommitFrom(ctx, tx, id)
}
func (s *Store) CommitWorkspace(ctx context.Context, id string, in WorkspaceCommitCreate, u User) (CollaborationCommit, error) {
	var out CollaborationCommit
	if !collaborationID(in.ID) {
		return out, fmt.Errorf("invalid_commit_id")
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" || len([]rune(in.Message)) > 2000 {
		return out, fmt.Errorf("invalid_commit_message")
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, id, u, true, true)
		if err != nil {
			return err
		}
		var actor, hash, wid string
		err = tx.QueryRowContext(ctx, `SELECT actor_id,request_hash,workspace_id::text FROM catalog.commits WHERE id=$1`, in.ID).Scan(&actor, &hash, &wid)
		if err == nil {
			if actor != u.ID || hash != requestHash(in) || wid != id {
				return errIdempotencyConflict
			}
			out, err = collaborationCommitFrom(ctx, tx, in.ID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = checkWorkspace(w, in.ExpectedVersion, ""); err != nil {
			return err
		}
		if w.DirtyCount == 0 {
			return fmt.Errorf("no_changes")
		}
		if err = workspaceAuthorizeSubmission(ctx, tx, w, u, true); err != nil {
			return err
		}
		out, err = insertWorkspaceCommit(ctx, tx, w, in.ID, in.Message, "edit", in, u)
		return err
	})
	return out, err
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func createPullRequestTx(ctx context.Context, tx *sql.Tx, w Workspace, in WorkspacePush, u User) (PullRequest, error) {
	if err := workspaceAuthorizeSubmission(ctx, tx, w, u, false); err != nil {
		return PullRequest{}, err
	}
	if err := checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
		return PullRequest{}, err
	}
	if in.ExpectedHead == "" || w.HeadCommitID == nil {
		return PullRequest{}, fmt.Errorf("workspace_head_conflict")
	}
	if w.DirtyCount != 0 {
		return PullRequest{}, fmt.Errorf("uncommitted_changes")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog.commits WHERE workspace_id=$1 AND merged_at IS NULL`, w.ID).Scan(&count); err != nil {
		return PullRequest{}, err
	}
	if count == 0 {
		return PullRequest{}, fmt.Errorf("no_changes")
	}
	var headMessage string
	err := tx.QueryRowContext(ctx, `SELECT message FROM catalog.commits WHERE id=$1 AND workspace_id=$2`, in.ExpectedHead, w.ID).Scan(&headMessage)
	if err != nil {
		return PullRequest{}, err
	}
	title := strings.TrimSpace(in.Title)
	var sourceBase *string
	if err = tx.QueryRowContext(ctx, `WITH RECURSIVE chain AS (
		SELECT id,parent_id,merged_at,0 AS depth,ARRAY[id] AS path FROM catalog.commits WHERE id=$1 AND workspace_id=$2
		UNION ALL SELECT c.id,c.parent_id,c.merged_at,chain.depth+1,chain.path||c.id
		FROM catalog.commits c JOIN chain ON c.id=chain.parent_id
		WHERE c.workspace_id=$2 AND NOT c.id=ANY(chain.path)
	) SELECT (SELECT id::text FROM chain WHERE merged_at IS NOT NULL ORDER BY depth LIMIT 1)`, in.ExpectedHead, w.ID).Scan(&sourceBase); err != nil {
		return PullRequest{}, err
	}
	var id, oldTitle, oldHead string
	var oldBase *string
	err = tx.QueryRowContext(ctx, `SELECT id::text,title,source_head_id::text,source_base_id::text FROM catalog.pull_requests WHERE workspace_id=$1 AND status='open' FOR UPDATE`, w.ID).Scan(&id, &oldTitle, &oldHead, &oldBase)
	creating := errors.Is(err, sql.ErrNoRows)
	if err != nil && !creating {
		return PullRequest{}, err
	}
	if title == "" && !creating {
		title = oldTitle
	}
	if title == "" {
		title = strings.TrimSpace(strings.SplitN(headMessage, "\n", 2)[0])
		if runes := []rune(title); len(runes) > 200 {
			title = string(runes[:200])
		}
	}
	if title == "" {
		title = strings.TrimSpace(w.Name)
	}
	if title == "" || len([]rune(title)) > 200 {
		return PullRequest{}, fmt.Errorf("invalid_pull_request_title")
	}
	if creating {
		id = newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog.pull_requests(id,workspace_id,author_id,title,source_head_id,source_base_id) VALUES($1,$2,$3,$4,$5,$6)`, id, w.ID, u.ID, title, in.ExpectedHead, sourceBase)
	} else if err == nil && (oldTitle != title || oldHead != in.ExpectedHead || !sameOptionalString(oldBase, sourceBase)) {
		_, err = tx.ExecContext(ctx, `UPDATE catalog.pull_requests SET title=$2,source_head_id=$3,source_base_id=$4,version=version+1,updated_at=now() WHERE id=$1`, id, title, in.ExpectedHead, sourceBase)
	}
	if err != nil {
		return PullRequest{}, err
	}
	return pullRequestFrom(ctx, tx, id, u, false)
}
func sameOptionalString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (s *Store) CreatePullRequest(ctx context.Context, in PullRequestCreate, u User) (PullRequest, error) {
	var out PullRequest
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, in.WorkspaceID, u, true, true)
		if err != nil {
			return err
		}
		out, err = createPullRequestTx(ctx, tx, w, in.WorkspacePush, u)
		return err
	})
	return out, err
}
func (s *Store) EditPullRequest(ctx context.Context, id string, in PullRequestEdit, u User) (PullRequest, error) {
	var out PullRequest
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		p, err := pullRequestFrom(ctx, tx, id, u, true)
		if err != nil {
			return err
		}
		if p.Version != in.ExpectedVersion {
			return fmt.Errorf("pull_request_version_conflict")
		}
		if p.Status == "merged" {
			return fmt.Errorf("pull_request_merged")
		}
		if in.Title != nil {
			title := strings.TrimSpace(*in.Title)
			if title == "" || len([]rune(title)) > 200 {
				return fmt.Errorf("invalid_pull_request_title")
			}
			p.Title = title
		}
		oldStatus := p.Status
		if in.Status != nil {
			if *in.Status != "open" && *in.Status != "closed" {
				return fmt.Errorf("invalid_pull_request_status")
			}
			p.Status = *in.Status
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog.pull_requests SET title=$2,status=$3,version=version+1,updated_at=now() WHERE id=$1`, id, p.Title, p.Status); err != nil {
			return err
		}
		if oldStatus != "closed" && p.Status == "closed" {
			if err = notifyPullRequestDecision(ctx, tx, p, u, false); err != nil {
				return err
			}
		}
		out, err = pullRequestFrom(ctx, tx, id, u, false)
		return err
	})
	return out, err
}
