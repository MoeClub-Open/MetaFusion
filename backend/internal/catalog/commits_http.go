package catalog

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

func respondCommit(c *gin.Context, v CommitReceipt, err error) {
	if errors.Is(err, errTransactionBusy) {
		auditlog.Fail(c, "transaction_busy")
		c.Header("Retry-After", "1")
		c.JSON(503, gin.H{"error": "transaction_busy", "applied": false})
		return
	}
	var identity *commitIdentityError
	if errors.As(err, &identity) {
		auditlog.Fail(c, "identity_candidates_changed")
		c.JSON(409, gin.H{"error": "identity_candidates_changed", "applied": false, "identity_review": identity})
		return
	}
	var conflict *commitConflictError
	if errors.As(err, &conflict) {
		auditlog.Fail(c, "commit_conflict")
		// Top-level conflicts mirrors conflict.conflicts so agents and the
		// three-column UI can read field diagnostics without digging.
		c.JSON(409, gin.H{
			"error":     "commit_conflict",
			"applied":   false,
			"conflict":  conflict.Conflict,
			"conflicts": conflict.Conflict.Conflicts,
		})
		return
	}
	if errors.Is(err, errDefinitionsConflict) {
		auditlog.Fail(c, "definitions_conflict")
		c.JSON(409, gin.H{"error": "definitions_conflict", "applied": false})
		return
	}
	// Per-operation evidence errors (source_refs_required, unknown_source_ref, …):
	// stable code + 0-based operation index. Old commit formats are rejected, not adapted.
	var opErr *commitOperationError
	if errors.As(err, &opErr) {
		code := opErr.Cause.Error()
		auditlog.Fail(c, code)
		c.JSON(400, gin.H{"error": code, "applied": false, "operation": opErr.Index})
		return
	}
	respond(c, v, err)
}

func (h HTTP) registerCommits(cat *gin.RouterGroup) {
	s := h.Store
	cat.POST("/checkout", required(""), func(c *gin.Context) {
		var in CheckoutRequest
		if !body(c, &in) {
			return
		}
		out, err := s.Checkout(c.Request.Context(), in, *user(c))
		respond(c, out, err)
	})
	cat.POST("/commits/preview", required(""), func(c *gin.Context) {
		var in CatalogCommit
		if !body(c, &in) {
			return
		}
		out, err := s.PushCommit(c.Request.Context(), in, *user(c), true)
		respondCommit(c, out, err)
	})
	cat.POST("/commits", required(""), func(c *gin.Context) {
		var in CatalogCommit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "commit", TargetID: in.ID})
		out, err := s.PushCommit(c.Request.Context(), in, *user(c), false)
		if err == nil {
			auditlog.Describe(c, auditlog.Detail{TargetType: "commit", TargetID: in.ID, Changes: map[string]any{"operation_count": len(out.Items)}})
		}
		if err == nil && out.ReviewStatus == "pending_review" {
			c.JSON(202, out)
			return
		}
		respondCommit(c, out, err)
	})
	cat.GET("/commits", required(""), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.Query("limit"))
		offset, _ := strconv.Atoi(c.Query("offset"))
		out, err := s.CommitSubmissions(c.Request.Context(), c.Query("review_status"), limit, offset, *user(c))
		respond(c, out, err)
	})
	cat.GET("/commits/:id/submission", required(""), func(c *gin.Context) {
		out, err := s.CommitSubmission(c.Request.Context(), c.Param("id"), *user(c))
		respond(c, out, err)
	})
	cat.POST("/commits/:id/review", required(PermissionReviewManage), func(c *gin.Context) {
		var in CommitReviewEdit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "commit", TargetID: c.Param("id"), Changes: map[string]any{"decision": in.Decision}})
		out, err := s.ReviewCommit(c.Request.Context(), c.Param("id"), in, *user(c))
		respondCommit(c, out, err)
	})

	cat.GET("/commits/:id", required(""), func(c *gin.Context) {
		out, err := s.CommitReceipt(c.Request.Context(), c.Param("id"), *user(c))
		respond(c, out, err)
	})
}
