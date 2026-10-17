package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// These inventories belong to the lifecycle management surface. Public lists
// deliberately continue to exclude terminal entities, and developer logs remain
// scoped to their owner. Neither inventory reads another service's tables.
type AdminEntityInventoryQuery struct {
	State, Kind, Query string
	Limit, Offset      int
}

type AdminEntityInventory struct {
	Items   []Entity `json:"items"`
	Total   int64    `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
	HasMore bool     `json:"has_more"`
}

type AdminRequestLogQuery struct {
	UserID, Credential, Path string
	Status                   int
	Limit, Offset            int
}

type AdminRequestLogEntry struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	RequestLogEntry
}

type AdminRequestLogInventory struct {
	Items   []AdminRequestLogEntry `json:"items"`
	Total   int64                  `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset"`
	HasMore bool                   `json:"has_more"`
}

func validateAdminInventoryPage(limit, offset int) error {
	if limit < 1 || limit > maxListLimit {
		return errParam(codeInvalidLimit)
	}
	if offset < 0 {
		return errParam(codeInvalidOffset)
	}
	return nil
}

func adminEntityInventoryWhere(in AdminEntityInventoryQuery) (string, []any, error) {
	if err := validateAdminInventoryPage(in.Limit, in.Offset); err != nil {
		return "", nil, err
	}
	for _, value := range []string{in.State, in.Kind, in.Query} {
		if err := checkQueryText(value); err != nil {
			return "", nil, err
		}
	}
	if in.State != "" && !contains([]string{"active", "deleted", "merged"}, in.State) || in.Kind != "" && !contains(Kinds, in.Kind) {
		return "", nil, errParam(codeInvalidQueryParam)
	}
	where, args := []string{"true"}, []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if in.State != "" {
		add("e.state=$%d", in.State)
	}
	if in.Kind != "" {
		add("e.kind=$%d", in.Kind)
	}
	if query := strings.TrimSpace(in.Query); query != "" {
		add(`(e.title ILIKE $%[1]d OR e.id::text ILIKE $%[1]d OR EXISTS (
		 SELECT 1 FROM jsonb_each(CASE WHEN jsonb_typeof(e.document->'translations')='object'
		 THEN e.document->'translations' ELSE '{}'::jsonb END) t
		 WHERE t.value->>'title' ILIKE $%[1]d))`, likeContains(query))
	}
	return strings.Join(where, " AND "), args, nil
}

func (s *Store) ListAdminEntities(ctx context.Context, in AdminEntityInventoryQuery, u *User) (AdminEntityInventory, error) {
	out := AdminEntityInventory{Items: []Entity{}, Limit: in.Limit, Offset: in.Offset}
	if u == nil || !u.Can(PermissionLifecycleManage) {
		return out, errForbidden
	}
	where, args, err := adminEntityInventoryWhere(in)
	if err != nil {
		return out, err
	}
	if s == nil || s.DB == nil {
		return out, sql.ErrConnDone
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM catalog.entities e WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, in.Limit, in.Offset)
	rows, err := tx.QueryContext(ctx, "SELECT e.id::text FROM catalog.entities e WHERE "+where+fmt.Sprintf(" ORDER BY e.updated_at DESC,e.id LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	// Use the shared structural reader and authoritative visibility columns,
	// rather than returning the embedded document as a separate entity model.
	entities, err := getManyFrom(ctx, tx, ids, u)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if entity, ok := entities[id]; ok {
			out.Items = append(out.Items, entity)
		}
	}
	out.HasMore = int64(in.Offset)+int64(len(out.Items)) < out.Total
	return out, tx.Commit()
}

func adminRequestLogWhere(in AdminRequestLogQuery) (string, []any, error) {
	if err := validateAdminInventoryPage(in.Limit, in.Offset); err != nil {
		return "", nil, err
	}
	for _, value := range []string{in.UserID, in.Credential, in.Path} {
		if err := checkQueryText(value); err != nil {
			return "", nil, err
		}
	}
	if in.UserID != "" {
		if _, err := uuid.Parse(in.UserID); err != nil {
			return "", nil, errParam(codeInvalidQueryParam)
		}
	}
	if in.Status != 0 && (in.Status < 100 || in.Status > 599) {
		return "", nil, errParam(codeInvalidQueryParam)
	}
	where, args := []string{"true"}, []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if in.UserID != "" {
		add("user_id=$%d::uuid", in.UserID)
	}
	if credential := strings.TrimSpace(in.Credential); credential != "" {
		add("credential_name=$%d", credential)
	}
	if in.Status != 0 {
		add("status=$%d", in.Status)
	}
	if in.Path != "" {
		add("route ILIKE $%d", likeContains(in.Path))
	}
	return strings.Join(where, " AND "), args, nil
}

func (s *Store) ListAdminRequestLogs(ctx context.Context, in AdminRequestLogQuery, u *User) (AdminRequestLogInventory, error) {
	out := AdminRequestLogInventory{Items: []AdminRequestLogEntry{}, Limit: in.Limit, Offset: in.Offset}
	if u == nil || !u.Can(PermissionLifecycleManage) {
		return out, errForbidden
	}
	where, args, err := adminRequestLogWhere(in)
	if err != nil {
		return out, err
	}
	if s == nil || s.DB == nil {
		return out, sql.ErrConnDone
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM catalog.api_request_logs WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, in.Limit, in.Offset)
	rows, err := tx.QueryContext(ctx, "SELECT id::text,user_id::text,at,credential_type,credential_name,method,route,status,ms FROM catalog.api_request_logs WHERE "+where+fmt.Sprintf(" ORDER BY at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item AdminRequestLogEntry
		var at time.Time
		if err = rows.Scan(&item.ID, &item.UserID, &at, &item.CredentialType, &item.CredentialName, &item.Method, &item.Route, &item.Status, &item.MS); err != nil {
			rows.Close()
			return out, err
		}
		item.At = at.Format(time.RFC3339Nano)
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	out.HasMore = int64(in.Offset)+int64(len(out.Items)) < out.Total
	return out, tx.Commit()
}

func (h HTTP) registerAdminInventory(api *gin.RouterGroup) {
	admin := api.Group("/admin", attachUser(h.Store), required(PermissionLifecycleManage))
	admin.GET("/catalog-entities", func(c *gin.Context) {
		if err := validateTextQuery(c, "state", "kind", "q"); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset, err := listPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := h.Store.ListAdminEntities(c.Request.Context(), AdminEntityInventoryQuery{
			State: c.Query("state"), Kind: c.Query("kind"), Query: c.Query("q"), Limit: limit, Offset: offset,
		}, user(c))
		respond(c, out, err)
	})
	admin.GET("/catalog-request-logs", func(c *gin.Context) {
		if err := validateTextQuery(c, "user_id", "credential", "status", "path"); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset, err := listPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		status := 0
		if raw := c.Query("status"); raw != "" {
			status, err = strconv.Atoi(raw)
			if err != nil || status < 100 || status > 599 {
				respond(c, nil, errParam(codeInvalidQueryParam))
				return
			}
		}
		out, err := h.Store.ListAdminRequestLogs(c.Request.Context(), AdminRequestLogQuery{
			UserID: c.Query("user_id"), Credential: c.Query("credential"), Status: status, Path: c.Query("path"), Limit: limit, Offset: offset,
		}, user(c))
		respond(c, out, err)
	})
}
