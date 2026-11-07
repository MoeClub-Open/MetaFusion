package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type HTTP struct {
	Store    *Store
	Catalog  *catalog.Store
	Missions *MissionEngine
}

func decodeBody(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeResponse(c, nil, errors.New("assistant_invalid_payload"))
		return false
	}
	return true
}

func writeResponse(c *gin.Context, v any, err error) {
	if err == nil {
		c.JSON(200, v)
		return
	}
	status, code := responseError(err)
	auditlog.Fail(c, code)
	c.JSON(status, gin.H{"error": code})
}

func responseError(err error) (status int, code string) {
	status, code = 400, safeRuntimeCode(err)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, sql.ErrNoRows):
		status, code = 404, "assistant_not_found"
	case errors.Is(err, ErrConflict):
		status, code = 409, "assistant_request_conflict"
	case errors.Is(err, ErrDisabled), errors.Is(err, ErrEncryptionUnavailable):
		status = 503
	case errors.Is(err, ErrInsufficientCredits), errors.Is(err, ErrBudgetExceeded):
		status = 429
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = 504, "assistant_request_timeout"
	case code == "assistant_forbidden":
		status = 403
	case code == "assistant_upstream_failed":
		status = 502
	case code == "assistant_request_failed":
		status = 500
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		if pg.Code == "22P02" {
			status, code = 400, "assistant_invalid_id"
		} else if pg.Code == "23503" || pg.Code == "23505" {
			status, code = 409, "assistant_record_in_use"
		} else {
			status, code = 500, "assistant_database_error"
		}
	}
	return
}

func pageArgs(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.Query("limit"))
	offset, _ = strconv.Atoi(c.Query("offset"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return
}
func slicePage[T any](items []T, limit, offset int) Page[T] {
	p := Page[T]{Items: []T{}, Total: len(items), Limit: limit, Offset: offset}
	if offset < len(items) {
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		p.Items = items[offset:end]
	}
	return p
}

func assistantIdentity(manage bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		u := catalog.RequestUser(c)
		if u == nil {
			auditlog.Fail(c, "authentication_required")
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication_required"})
			return
		}
		if u.IsThirdParty || manage && !u.Can(PermissionManage) {
			auditlog.Fail(c, "assistant_forbidden")
			c.AbortWithStatusJSON(403, gin.H{"error": "assistant_forbidden"})
			return
		}
		c.Next()
	}
}

func (h HTTP) Register(api *gin.RouterGroup) {
	a := api.Group("/assistant")
	a.Use(h.auditMiddleware())
	a.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 150*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	a.GET("/models", func(c *gin.Context) {
		items, err := h.Store.Models(c.Request.Context(), true)
		limit, offset := pageArgs(c)
		writeResponse(c, slicePage(items, limit, offset), err)
	})
	a.GET("/generated/:entity_id", h.getGenerated)
	self := a.Group("", assistantIdentity(false))
	self.GET("/account", func(c *gin.Context) {
		balance, err := h.Store.SyncCredits(c.Request.Context(), catalog.RequestUser(c).ID)
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		settings, err := h.Store.GetSettings(c.Request.Context())
		writeResponse(c, gin.H{"balance": balance, "registration_credits": settings.RegistrationCredits, "accepted_edit_credits": settings.AcceptedEditCredits, "default_model_id": settings.DefaultModelID}, err)
	})
	self.GET("/byo/providers", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.BYOProviders(c.Request.Context(), catalog.RequestUser(c).ID, false, l, o)
		writeResponse(c, v, e)
	})
	self.POST("/byo/providers", h.saveBYOProvider(false, false))
	self.PUT("/byo/providers/:id", h.saveBYOProvider(false, true))
	self.DELETE("/byo/providers/:id", h.deleteBYOProvider(false))
	self.GET("/byo/models", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.BYOModels(c.Request.Context(), catalog.RequestUser(c).ID, false, false, l, o)
		writeResponse(c, v, e)
	})
	self.POST("/byo/models", h.saveBYOModel(false, false))
	self.PUT("/byo/models/:id", h.saveBYOModel(false, true))
	self.DELETE("/byo/models/:id", h.deleteBYOModel(false))
	self.GET("/usage", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.UsageRecords(c.Request.Context(), catalog.RequestUser(c).ID, false, l, o)
		writeResponse(c, v, e)
	})
	self.GET("/conversations", h.listConversations(false))
	self.POST("/conversations", h.createConversation)
	self.GET("/conversations/:id", h.getConversation(false))
	self.DELETE("/conversations/:id", h.deleteConversation(false))
	self.GET("/requests", h.listRequests(false))
	self.GET("/requests/:id", h.getRequest(false))
	self.POST("/chat", h.chat)
	self.POST("/context", h.previewContext)
	self.POST("/generated/:entity_id", h.generate)
	admin := a.Group("/admin", assistantIdentity(true))
	h.registerSearch(self, admin)
	h.registerMissions(self, admin)
	admin.GET("/settings", func(c *gin.Context) { v, e := h.Store.GetSettings(c.Request.Context()); writeResponse(c, v, e) })
	admin.PUT("/settings", func(c *gin.Context) {
		var in Settings
		if !decodeBody(c, &in) {
			return
		}
		v, e := h.Store.SaveSettings(c.Request.Context(), in)
		writeResponse(c, v, e)
	})
	admin.GET("/providers", func(c *gin.Context) {
		v, e := h.Store.Providers(c.Request.Context())
		l, o := pageArgs(c)
		writeResponse(c, slicePage(v, l, o), e)
	})
	admin.POST("/providers", h.saveProvider(false))
	admin.PUT("/providers/:id", h.saveProvider(true))
	admin.DELETE("/providers/:id", func(c *gin.Context) {
		e := h.Store.DeleteProvider(c.Request.Context(), c.Param("id"))
		if e != nil {
			writeResponse(c, nil, e)
			return
		}
		c.Status(204)
	})
	admin.GET("/models", func(c *gin.Context) {
		v, e := h.Store.Models(c.Request.Context(), false)
		l, o := pageArgs(c)
		writeResponse(c, slicePage(v, l, o), e)
	})
	admin.POST("/models", h.saveModel(false))
	admin.PUT("/models/:id", h.saveModel(true))
	admin.DELETE("/models/:id", func(c *gin.Context) {
		e := h.Store.DeleteModel(c.Request.Context(), c.Param("id"))
		if e != nil {
			writeResponse(c, nil, e)
			return
		}
		c.Status(204)
	})
	admin.GET("/byo/providers", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.BYOProviders(c.Request.Context(), c.Query("user_id"), true, l, o)
		writeResponse(c, v, e)
	})
	admin.PUT("/byo/providers/:id", h.saveBYOProvider(true, true))
	admin.DELETE("/byo/providers/:id", h.deleteBYOProvider(true))
	admin.GET("/byo/models", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.BYOModels(c.Request.Context(), c.Query("user_id"), true, false, l, o)
		writeResponse(c, v, e)
	})
	admin.PUT("/byo/models/:id", h.saveBYOModel(true, true))
	admin.DELETE("/byo/models/:id", h.deleteBYOModel(true))
	admin.GET("/usage", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.UsageRecords(c.Request.Context(), c.Query("user_id"), true, l, o)
		writeResponse(c, v, e)
	})
	admin.GET("/accounts", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.Accounts(c.Request.Context(), l, o)
		writeResponse(c, v, e)
	})
	admin.POST("/accounts/:user_id/adjust", func(c *gin.Context) {
		var in struct {
			ID      string `json:"id"`
			Credits int64  `json:"credits"`
			Note    string `json:"note"`
		}
		if !decodeBody(c, &in) {
			return
		}
		if _, e := uuid.Parse(in.ID); e != nil {
			writeResponse(c, nil, errors.New("assistant_adjustment_id_required"))
			return
		}
		v, e := h.Store.AdjustCredits(c.Request.Context(), CreditAdjustment{ID: in.ID, UserID: c.Param("user_id"), Credits: in.Credits, Note: in.Note})
		writeResponse(c, v, e)
	})
	admin.GET("/ledger", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.CreditEntries(c.Request.Context(), c.Query("user_id"), true, l, o)
		writeResponse(c, v, e)
	})
	admin.GET("/requests", h.listRequests(true))
	admin.GET("/requests/:id", h.getRequest(true))
	admin.POST("/requests/:id/cancel", h.cancelRequest)
	admin.GET("/budgets", func(c *gin.Context) {
		v, e := h.Store.Budgets(c.Request.Context())
		l, o := pageArgs(c)
		writeResponse(c, slicePage(v, l, o), e)
	})
	admin.GET("/conversations", h.listConversations(true))
	admin.GET("/conversations/:id", h.getConversation(true))
	admin.DELETE("/conversations/:id", h.deleteConversation(true))
	admin.GET("/generated", h.adminGenerated)
	admin.DELETE("/generated/:id", h.deleteGenerated)
}

func (h HTTP) saveProvider(update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in ProviderInput
		if !decodeBody(c, &in) {
			return
		}
		if update {
			in.ID = c.Param("id")
		} else {
			in.ID = ""
		}
		v, e := h.Store.SaveProvider(c.Request.Context(), in)
		writeResponse(c, v, e)
	}
}
func (h HTTP) saveModel(update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in Model
		if !decodeBody(c, &in) {
			return
		}
		if update {
			in.ID = c.Param("id")
		} else {
			in.ID = ""
		}
		v, e := h.Store.SaveModel(c.Request.Context(), in)
		writeResponse(c, v, e)
	}
}
func (h HTTP) saveBYOProvider(admin, update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in BYOProviderInput
		if !decodeBody(c, &in) {
			return
		}
		owner := catalog.RequestUser(c).ID
		if update {
			in.ID = c.Param("id")
		} else {
			in.ID = ""
		}
		if admin {
			if e := h.Store.DB.QueryRowContext(c.Request.Context(), `SELECT owner_id FROM assistant.byo_providers WHERE id=$1`, in.ID).Scan(&owner); e != nil {
				writeResponse(c, nil, e)
				return
			}
		}
		in.OwnerID = owner
		v, e := h.Store.SaveBYOProvider(c.Request.Context(), owner, in, admin)
		writeResponse(c, v, e)
	}
}
func (h HTTP) deleteBYOProvider(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		e := h.Store.DeleteBYOProvider(c.Request.Context(), catalog.RequestUser(c).ID, c.Param("id"), admin)
		if e != nil {
			writeResponse(c, nil, e)
			return
		}
		c.Status(204)
	}
}
func (h HTTP) saveBYOModel(admin, update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in BYOModel
		if !decodeBody(c, &in) {
			return
		}
		owner := catalog.RequestUser(c).ID
		if update {
			in.ID = c.Param("id")
		} else {
			in.ID = ""
		}
		if admin {
			if e := h.Store.DB.QueryRowContext(c.Request.Context(), `SELECT owner_id FROM assistant.byo_models WHERE id=$1`, in.ID).Scan(&owner); e != nil {
				writeResponse(c, nil, e)
				return
			}
		}
		in.OwnerID = owner
		v, e := h.Store.SaveBYOModel(c.Request.Context(), owner, in, admin)
		writeResponse(c, v, e)
	}
}
func (h HTTP) deleteBYOModel(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		e := h.Store.DeleteBYOModel(c.Request.Context(), catalog.RequestUser(c).ID, c.Param("id"), admin)
		if e != nil {
			writeResponse(c, nil, e)
			return
		}
		c.Status(204)
	}
}

func (h HTTP) chat(c *gin.Context) {
	var in ChatRequest
	if !decodeBody(c, &in) {
		return
	}
	if e := validateChatRequest(in); e != nil {
		writeResponse(c, nil, e)
		return
	}
	u := *catalog.RequestUser(c)
	conv, replay, err := h.Store.claimChat(c.Request.Context(), u.ID, in)
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	if replay != nil {
		c.JSON(200, replay)
		return
	}
	if in.ModelID == "" {
		in.ModelID = conv.ModelID
	}
	conv.ModelID = in.ModelID
	messages := chatMessages(conv.Messages, in.Message)
	initial := ChatResponse{
		ConversationID: conv.ID,
		RequestID:      in.ClientRequestID,
		Status:         "pending",
		CompletionRun:  CompletionRun{Messages: messages},
	}
	if balance, bErr := h.Store.SyncCredits(c.Request.Context(), u.ID); bErr == nil {
		initial.Balance = balance
	}
	if err = h.Store.persistChat(c.Request.Context(), u.ID, in.ClientRequestID, conv, initial, "pending"); err != nil {
		writeResponse(c, nil, err)
		return
	}
	bgBase := context.WithoutCancel(c.Request.Context())
	done := make(chan ChatResponse, 1)
	h.Store.bg.Add(1)
	go func() {
		defer h.Store.bg.Done()
		in.checkpoint = func(progress CompletionRun) error {
			checkpointCtx, cancel := context.WithTimeout(bgBase, 10*time.Second)
			defer cancel()
			return h.Store.persistChat(checkpointCtx, u.ID, in.ClientRequestID, conv, ChatResponse{
				ConversationID: conv.ID,
				RequestID:      in.ClientRequestID,
				Status:         "pending",
				CompletionRun:  progress,
			}, "pending")
		}
		runCtx, cancelRun := context.WithTimeout(bgBase, 5*time.Minute)
		defer cancelRun()
		run, runErr := h.complete(runCtx, u, in, messages, true, "chat", "")
		if run.Messages == nil {
			run.Messages = messages
		}
		response := ChatResponse{ConversationID: conv.ID, RequestID: in.ClientRequestID, CompletionRun: run}
		if runErr != nil {
			response.Error = safeRuntimeCode(runErr)
		}
		finishCtx, cancelFinish := context.WithTimeout(bgBase, 10*time.Second)
		defer cancelFinish()
		if finishErr := h.Store.finishChat(finishCtx, u.ID, in.ClientRequestID, conv, response); finishErr != nil {
			response.Status = "failed"
			response.Error = "assistant_response_persistence_failed"
		} else if response.Error != "" {
			response.Status = "failed"
		} else {
			response.Status = "completed"
		}
		done <- response
	}()
	if in.Background {
		c.JSON(200, initial)
		return
	}
	select {
	case response := <-done:
		if response.Error == "assistant_response_persistence_failed" {
			auditlog.Fail(c, response.Error)
		}
		c.JSON(200, response)
	case <-c.Request.Context().Done():
		writeResponse(c, nil, c.Request.Context().Err())
	}
}

func withoutSystemMessages(in []Message) []Message {
	out := make([]Message, 0, len(in))
	missing := map[string]bool{}
	for _, m := range in {
		if m.Role != "system" {
			out = append(out, m)
			for _, call := range m.ToolCalls {
				missing[call.ID] = true
			}
			if m.Role == "tool" {
				delete(missing, m.ToolCallID)
			}
		}
	}
	// An interrupted process may have persisted the planned tool call but not
	// its result. Preserve the uncertainty; never automatically replay a write.
	for id := range missing {
		out = append(out, Message{Role: "tool", ToolCallID: id, Content: `{"error":"assistant_interrupted_tool_result_unknown","retry_mutation":false}`})
	}
	return out
}

func (h HTTP) listConversations(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := catalog.RequestUser(c).ID
		if admin {
			owner = c.Query("user_id")
		}
		l, o := pageArgs(c)
		page := Page[Conversation]{Items: []Conversation{}, Limit: l, Offset: o}
		where := ` WHERE ($1='' OR owner_id=$1)`
		if err := h.Store.DB.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM assistant.conversations`+where, owner).Scan(&page.Total); err != nil {
			writeResponse(c, nil, err)
			return
		}
		rows, err := h.Store.DB.QueryContext(c.Request.Context(), `SELECT c.id::text,c.owner_id,c.title,coalesce(c.model_id::text,''),coalesce((SELECT r.id::text FROM assistant.chat_requests r WHERE r.conversation_id=c.id AND r.status='pending' LIMIT 1),''),'[]'::jsonb,c.created_at,c.updated_at FROM assistant.conversations c`+where+` ORDER BY c.updated_at DESC,c.id LIMIT $2 OFFSET $3`, owner, l, o)
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanConversation(rows)
			if e != nil {
				writeResponse(c, nil, e)
				return
			}
			page.Items = append(page.Items, v)
		}
		writeResponse(c, page, rows.Err())
	}
}
func (h HTTP) getConversation(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := catalog.RequestUser(c).ID
		if admin {
			owner = ""
		}
		v, e := scanConversation(h.Store.DB.QueryRowContext(c.Request.Context(), `SELECT c.id::text,c.owner_id,c.title,coalesce(c.model_id::text,''),coalesce((SELECT r.id::text FROM assistant.chat_requests r WHERE r.conversation_id=c.id AND r.status='pending' LIMIT 1),''),c.messages,c.created_at,c.updated_at FROM assistant.conversations c WHERE c.id=$1 AND ($2='' OR c.owner_id=$2)`, c.Param("id"), owner))
		writeResponse(c, v, e)
	}
}
func (h HTTP) createConversation(c *gin.Context) {
	var in struct {
		Title   string `json:"title"`
		ModelID string `json:"model_id"`
	}
	if !decodeBody(c, &in) {
		return
	}
	v := Conversation{ID: uuid.NewString(), OwnerID: catalog.RequestUser(c).ID, Title: shortTitle(in.Title), ModelID: in.ModelID, Messages: []Message{}}
	err := h.Store.DB.QueryRowContext(c.Request.Context(), `INSERT INTO assistant.conversations(id,owner_id,title,model_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid) RETURNING created_at,updated_at`, v.ID, v.OwnerID, v.Title, v.ModelID).Scan(&v.CreatedAt, &v.UpdatedAt)
	writeResponse(c, v, err)
}
func (h HTTP) deleteConversation(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := catalog.RequestUser(c).ID
		if admin {
			owner = ""
		}
		tx, err := h.Store.DB.BeginTx(c.Request.Context(), nil)
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		defer tx.Rollback()
		var id string
		err = tx.QueryRowContext(c.Request.Context(), `DELETE FROM assistant.conversations WHERE id=$1 AND ($2='' OR owner_id=$2) AND NOT EXISTS (SELECT 1 FROM assistant.chat_requests WHERE conversation_id=$1 AND status='pending') RETURNING id::text`, c.Param("id"), owner).Scan(&id)
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		// Keep the sealed request ID after deleting message content. Otherwise a
		// delayed retry could spend tokens and apply a catalog mutation again.
		_, err = tx.ExecContext(c.Request.Context(), `UPDATE assistant.chat_requests SET response=jsonb_build_object('conversation_id',$1::text,'error','assistant_conversation_deleted'),updated_at=now() WHERE conversation_id=$1`, id)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		c.Status(204)
	}
}
