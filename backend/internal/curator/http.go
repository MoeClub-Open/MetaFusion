package curator

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/assistant"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type HTTP struct {
	Service *Service
}

func (h HTTP) Register(api *gin.RouterGroup) {
	g := api.Group("/curator", h.adminIdentity(false))
	g.GET("/summary", h.summary)
	g.GET("/settings", h.settings)
	g.GET("/findings", h.findings)
	g.GET("/runs", h.runs)
	g.GET("/runs/:id", h.run)
	write := g.Group("", h.adminIdentity(true))
	write.PUT("/settings", h.saveSettings)
	write.POST("/pause", h.pause)
	write.POST("/resume", h.resume)
	write.POST("/sweep", h.sweep)
	write.POST("/findings/:id/reopen", h.reopen)
	write.POST("/findings/:id/ignore", h.ignore)
}

func (h HTTP) adminIdentity(write bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		u := catalog.RequestUser(c)
		if u == nil || u.ID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication_required"})
			return
		}
		if u.IsThirdParty || !u.Can(assistant.PermissionManage) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "curator_forbidden"})
			return
		}
		if write && !u.Can(catalog.PermissionReviewManage) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "curator_review_required"})
			return
		}
		c.Next()
	}
}

func (h HTTP) summary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	v, err := h.Service.Summary(ctx)
	respond(c, v, err)
}

func (h HTTP) settings(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	v, err := h.Service.Settings(ctx)
	respond(c, v, err)
}

func (h HTTP) saveSettings(c *gin.Context) {
	var in Settings
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "curator_invalid_settings"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	v, err := h.Service.SaveSettings(ctx, in)
	if err == nil {
		h.Service.Wake()
		h.audit(c, "curator.settings_updated", "curator_settings", "", map[string]any{
			"enabled": v.Enabled, "model_id": v.ModelID, "interval_seconds": v.IntervalSeconds,
			"max_runs_per_hour": v.MaxRunsPerHour, "monthly_credit_allowance": v.MonthlyCreditAllowance,
			"auto_merge_low_risk": v.AutoMergeLowRisk, "allow_structures": v.AllowStructures,
			"allow_sources": v.AllowSources, "allow_names": v.AllowNames, "allow_dedupe": v.AllowDedupe,
		})
	}
	respond(c, v, err)
}

func (h HTTP) pause(c *gin.Context) {
	var in struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	reason := truncate(strings.TrimSpace(in.Reason), 200)
	if reason == "" {
		reason = "manual"
	}
	settings, err := h.Service.SetPaused(ctx, true, reason)
	if err == nil {
		h.audit(c, "curator.paused", "curator_settings", "", map[string]any{"reason": reason})
	}
	respond(c, settings, err)
}

func (h HTTP) resume(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	settings, err := h.Service.SetPaused(ctx, false, "")
	if err == nil {
		h.Service.Wake()
		h.audit(c, "curator.resumed", "curator_settings", "", nil)
	}
	respond(c, settings, err)
}

func (h HTTP) sweep(c *gin.Context) {
	h.Service.ForceSweep()
	h.audit(c, "curator.sweep_requested", "curator_settings", "", nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h HTTP) findings(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	limit, offset := pageArgs(c)
	v, err := h.Service.Findings(ctx, FindingFilter{Status: c.Query("status"), Code: c.Query("code"), Kind: c.Query("kind")}, limit, offset)
	respond(c, v, err)
}

func (h HTTP) runs(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	limit, offset := pageArgs(c)
	v, err := h.Service.Runs(ctx, RunFilter{Kind: c.Query("kind")}, limit, offset)
	respond(c, v, err)
}

func (h HTTP) run(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	v, err := h.Service.GetRun(ctx, c.Param("id"))
	respond(c, v, err)
}

func (h HTTP) reopen(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	v, err := h.Service.ReopenFinding(ctx, c.Param("id"))
	if err == nil {
		h.Service.Wake()
		h.audit(c, "curator.finding_reopened", "finding", v.ID, map[string]any{"entity_id": v.EntityID, "code": v.Code})
	}
	respond(c, v, err)
}

func (h HTTP) ignore(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	v, err := h.Service.IgnoreFinding(ctx, c.Param("id"))
	if err == nil {
		h.audit(c, "curator.finding_ignored", "finding", v.ID, map[string]any{"entity_id": v.EntityID, "code": v.Code})
	}
	respond(c, v, err)
}

func (h HTTP) audit(c *gin.Context, action, targetType, targetID string, changes map[string]any) {
	if h.Service.Catalog.Audit == nil {
		return
	}
	u := catalog.RequestUser(c)
	if u == nil {
		return
	}
	credential := "session"
	if u.FromPAT {
		credential = "pat"
	}
	h.Service.Catalog.Audit.Record(auditlog.Entry{
		Action: action, ActorUserID: u.ID, ActorUsername: u.Username, CredentialType: credential,
		TargetType: targetType, TargetID: targetID, Changes: changes, Result: "success",
		RequestMethod: c.Request.Method, Route: c.FullPath(), HTTPStatus: http.StatusOK,
	})
}

func respond(c *gin.Context, v any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, v)
		return
	}
	status, code := http.StatusBadRequest, "curator_invalid"
	switch {
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "curator_not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "curator_conflict"
	case errors.Is(err, ErrInvalidSettings):
		status, code = http.StatusBadRequest, "curator_invalid_settings"
	case errors.Is(err, ErrAllowanceRequired):
		status, code = http.StatusBadRequest, "curator_allowance_required"
	case errors.Is(err, ErrAllowanceExceeded):
		status, code = http.StatusTooManyRequests, "curator_monthly_allowance_exceeded"
	case errors.Is(err, ErrAssistantDisabled):
		status, code = http.StatusConflict, "curator_assistant_disabled"
	case errors.Is(err, ErrModelRequired):
		status, code = http.StatusConflict, "curator_model_required"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "curator_timeout"
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		slog.Error("curator database error", "sqlstate", string(pg.Code), "detail", pg.Detail)
		status, code = http.StatusInternalServerError, "curator_database_error"
	}
	auditlog.Fail(c, code)
	c.JSON(status, gin.H{"error": code})
}

func pageArgs(c *gin.Context) (int, int) {
	limit, offset := 50, 0
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if raw := c.Query("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			offset = n
		}
	}
	return pageBounds(limit, offset)
}
