package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Store) EditWorkspaceTag(ctx context.Context, wid string, in WorkspaceTagEdit, u User) (Workspace, error) {
	var out Workspace
	if !collaborationID(in.ID) || in.TagID != "" && !collaborationID(in.TagID) {
		return out, fmt.Errorf("invalid_version_request")
	}
	if !in.Delete && (!collaborationID(in.CommitID) || strings.TrimSpace(in.Name) == "" || len([]rune(in.Name)) > 200 || len([]rune(in.Message)) > 2000) {
		return out, fmt.Errorf("invalid_version_tag")
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		w, err := workspaceFrom(ctx, tx, wid, u, true, true)
		if err != nil {
			return err
		}
		if w.OwnerID != u.ID {
			return errForbidden
		}
		if replay, ok, err := versionActionReplay(ctx, tx, w, in.ID, in, u); err != nil {
			return err
		} else if ok {
			out = replay.Workspace
			return nil
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if w.HeadCommitID != nil && in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if in.Delete {
			if !collaborationID(in.TagID) {
				return fmt.Errorf("invalid_version_tag")
			}
			result, err := tx.ExecContext(ctx, `DELETE FROM catalog.workspace_tags WHERE id=$1 AND workspace_id=$2`, in.TagID, wid)
			if err != nil {
				return err
			}
			count, _ := result.RowsAffected()
			if count == 0 {
				return sql.ErrNoRows
			}
		} else {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog.workspace_commits WHERE workspace_id=$1 AND commit_id=$2) OR EXISTS(SELECT 1 FROM catalog.workspaces WHERE id=$1 AND base_commit_id=$2)`, wid, in.CommitID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return sql.ErrNoRows
			}
			if in.TagID == "" {
				if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_tags(id,workspace_id,name,commit_id,message,created_by) VALUES($1,$2,$3,$4,$5,$6)`, in.ID, wid, strings.TrimSpace(in.Name), in.CommitID, in.Message, u.ID); err != nil {
					return err
				}
			} else {
				result, err := tx.ExecContext(ctx, `UPDATE catalog.workspace_tags SET name=$3,commit_id=$4,message=$5,version=version+1 WHERE id=$1 AND workspace_id=$2`, in.TagID, wid, strings.TrimSpace(in.Name), in.CommitID, in.Message)
				if err != nil {
					return err
				}
				count, _ := result.RowsAffected()
				if count == 0 {
					return sql.ErrNoRows
				}
			}
		}
		if err = recordVersionEvent(ctx, tx, w, in.ID, "tag", in, u); err != nil {
			return err
		}
		if err = bumpWorkspace(ctx, tx, wid); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, wid, u, false, false)
		return err
	})
	return out, err
}

func (s *Store) WorkspaceTags(ctx context.Context, wid string, limit, offset int, u User) (WorkspaceTagPage, error) {
	limit, offset = collaborationPage(limit, offset)
	out := WorkspaceTagPage{Items: []WorkspaceTag{}, Limit: limit, Offset: offset}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, wid, u, false)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, sql.ErrNoRows
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog.workspace_tags WHERE workspace_id=$1`, wid).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,name,commit_id::text,message,version,created_by,created_at FROM catalog.workspace_tags WHERE workspace_id=$1 ORDER BY name,id LIMIT $2 OFFSET $3`, wid, limit, offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v WorkspaceTag
		if err = rows.Scan(&v.ID, &v.Name, &v.CommitID, &v.Message, &v.Version, &v.CreatedBy, &v.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) WorkspaceStashes(ctx context.Context, wid string, limit, offset int, u User) (WorkspaceStashPage, error) {
	limit, offset = collaborationPage(limit, offset)
	out := WorkspaceStashPage{Items: []WorkspaceStash{}, Limit: limit, Offset: offset}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	allowed, err := workspaceReadable(ctx, tx, wid, u, false)
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, sql.ErrNoRows
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog.workspace_stashes WHERE workspace_id=$1 AND state='active'`, wid).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,name,created_by,created_at FROM catalog.workspace_stashes WHERE workspace_id=$1 AND state='active' ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, wid, limit, offset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v WorkspaceStash
		if err = rows.Scan(&v.ID, &v.Name, &v.CreatedBy, &v.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) ApplyWorkspaceStash(ctx context.Context, wid string, in WorkspaceStashAction, u User) (Workspace, error) {
	var out Workspace
	if !collaborationID(in.ID) || !contains([]string{"save", "apply", "pop", "drop"}, in.Operation) || len([]rune(in.Name)) > 200 {
		return out, fmt.Errorf("invalid_stash_operation")
	}
	if in.Operation != "save" && !collaborationID(in.StashID) {
		return out, fmt.Errorf("invalid_stash_id")
	}
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		resolutions := newVersionResolutions(in.Resolutions)
		w, err := workspaceFrom(ctx, tx, wid, u, true, true)
		if err != nil {
			return err
		}
		if replay, ok, err := versionActionReplay(ctx, tx, w, in.ID, in, u); err != nil {
			return err
		} else if ok {
			out = replay.Workspace
			return nil
		}
		if err = checkWorkspace(w, in.ExpectedVersion, in.ExpectedHead); err != nil {
			return err
		}
		if w.HeadCommitID != nil && in.ExpectedHead == "" {
			return fmt.Errorf("workspace_head_conflict")
		}
		if in.Operation == "save" {
			if w.DirtyCount == 0 {
				return fmt.Errorf("no_changes")
			}
			saved := []WorkspaceObject{}
			for _, o := range w.Objects {
				if o.Dirty {
					o.Base = o.Committed
					saved = append(saved, o)
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.workspace_stashes(id,workspace_id,name,objects,created_by) VALUES($1,$2,$3,$4,$5)`, in.ID, wid, in.Name, encode(saved), u.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_objects SET document=head_document,staged_authors='{}' WHERE workspace_id=$1`, wid); err != nil {
				return err
			}
		} else {
			var raw []byte
			var actor string
			if err = tx.QueryRowContext(ctx, `SELECT objects,created_by FROM catalog.workspace_stashes WHERE id=$1 AND workspace_id=$2 AND state='active' FOR UPDATE`, in.StashID, wid).Scan(&raw, &actor); err != nil {
				return err
			}
			if in.Operation == "drop" && actor != u.ID && w.OwnerID != u.ID {
				return errForbidden
			}
			if in.Operation != "drop" {
				var saved []WorkspaceObject
				if err = json.Unmarshal(raw, &saved); err != nil {
					return err
				}
				objects := map[string]WorkspaceObject{}
				for _, o := range w.Objects {
					objects[o.Target+":"+o.ID] = o
				}
				for _, v := range saved {
					key := v.Target + ":" + v.ID
					o, ok := objects[key]
					if !ok {
						o = WorkspaceObject{Target: v.Target, ID: v.ID}
						if err = s.versionCheckoutObject(ctx, tx, wid, &o, u); err != nil {
							return err
						}
					}
					v.Committed = v.Document
					conflictVersion := versionDocumentNumber(o.Document, o.BaseVersion)
					merged, conflicts, err := mergeWorkspaceObject(v, o.Document, conflictVersion)
					if err != nil {
						return err
					}
					merged, _, err = resolutions.resolve(merged, conflicts, conflictVersion)
					if err != nil {
						return err
					}
					o.Document = merged
					if o.Contributors == nil {
						o.Contributors = map[string]string{}
					}
					for id, name := range v.Contributors {
						o.Contributors[id] = name
					}
					if err = s.versionSaveObject(ctx, tx, wid, o, u); err != nil {
						return err
					}
				}
			}
			if in.Operation == "drop" || in.Operation == "pop" {
				if _, err = tx.ExecContext(ctx, `UPDATE catalog.workspace_stashes SET state='dropped' WHERE id=$1`, in.StashID); err != nil {
					return err
				}
			}
		}
		if err = resolutions.validate(); err != nil {
			return err
		}
		if err = recordVersionEvent(ctx, tx, w, in.ID, "stash_"+in.Operation, in, u); err != nil {
			return err
		}
		if err = bumpWorkspace(ctx, tx, wid); err != nil {
			return err
		}
		out, err = workspaceFrom(ctx, tx, wid, u, false, false)
		return err
	})
	return out, err
}
