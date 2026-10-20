package catalog

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

func collaborationViewer(c *gin.Context) User {
	if u := user(c); u != nil {
		return *u
	}
	return User{}
}

func respondCollaboration(c *gin.Context, value any, err error) {
	c.Header("Cache-Control", "no-store")
	var conflict *workspaceConflictError
	if errors.As(err, &conflict) {
		auditlog.Fail(c, "workspace_conflict")
		c.JSON(409, gin.H{"error": "workspace_conflict", "applied": false, "conflicts": conflict.Conflicts})
		return
	}
	var operation *commitOperationError
	if err != nil && firstCode(err.Error()) == "storage_unavailable" {
		auditlog.Fail(c, "storage_unavailable")
		c.Header("Retry-After", "1")
		out := gin.H{"error": "storage_unavailable", "applied": false}
		if errors.As(err, &operation) {
			out["operation"] = operation.Index
		}
		c.JSON(503, out)
		return
	}
	if errors.As(err, &operation) && !internalFailure(operation.Cause) {
		status := 400
		if errors.Is(operation.Cause, errForbidden) {
			status = 403
		}
		auditlog.Fail(c, operation.Error())
		c.JSON(status, gin.H{"error": operation.Error(), "operation": operation.Index, "applied": false})
		return
	}
	if err != nil {
		code := err.Error()
		if strings.HasSuffix(code, "_version_conflict") || strings.HasSuffix(code, "_head_conflict") || contains([]string{"uncommitted_changes", "pull_request_merged", "pull_request_closed", "workspace_archived", "no_changes", "workspace_head_required", "resolution_stale", "published_history"}, code) {
			auditlog.Fail(c, code)
			c.JSON(409, gin.H{"error": code, "applied": false})
			return
		}
	}
	respond(c, value, err)
}

func collaborationPagination(c *gin.Context) (int, int, error) {
	limit, offset, err := listPagination(c)
	if c.Query("limit") == "" {
		if c.Query("page") != "" {
			offset = (offset / defaultListLimit) * 20
		}
		limit = 20
	}
	return limit, offset, err
}

func (h HTTP) registerCollaboration(cat *gin.RouterGroup) {
	s := h.Store
	h.registerVersioning(cat)
	cat.POST("/workspaces", required(""), func(c *gin.Context) {
		var in WorkspaceCreate
		if !body(c, &in) {
			return
		}
		out, err := s.CreateWorkspace(c.Request.Context(), in, *user(c))
		if err == nil {
			auditlog.Describe(c, workspaceAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces", required(""), func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.ListWorkspacePage(c.Request.Context(), limit, offset, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id", required(""), func(c *gin.Context) {
		out, err := s.GetWorkspace(c.Request.Context(), c.Param("id"), *user(c))
		respondCollaboration(c, out, err)
	})
	cat.PATCH("/workspaces/:id", required(""), func(c *gin.Context) {
		var in WorkspaceEdit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.EditWorkspace(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			auditlog.Describe(c, workspaceAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/checkout", required(""), func(c *gin.Context) {
		var in WorkspaceCheckout
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.CheckoutWorkspace(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			d := workspaceAuditDetail(out)
			d.Changes["entity_count"], d.Changes["relation_count"] = len(in.EntityIDs), len(in.RelationIDs)
			auditlog.Describe(c, d)
		}
		respondCollaboration(c, out, err)
	})
	cat.PUT("/workspaces/:id/changes", required(""), func(c *gin.Context) {
		var in WorkspaceChanges
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id"), Changes: map[string]any{"operation_count": len(in.Operations)}})
		out, err := s.SaveWorkspaceChanges(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			d := workspaceAuditDetail(out.Workspace)
			d.Changes["operation_count"] = len(in.Operations)
			auditlog.Describe(c, d)
		}
		respondCollaboration(c, out, err)
	})
	cat.PUT("/workspaces/:id/members", required(""), func(c *gin.Context) {
		var in WorkspaceMembersEdit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.SetWorkspaceMembers(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			auditlog.Describe(c, workspaceAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/commits", required(""), func(c *gin.Context) {
		var in WorkspaceCommitCreate
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "commit", TargetID: in.ID})
		out, err := s.CommitWorkspace(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			auditlog.Describe(c, commitAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id/commits", required(""), func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.VersionHistory(c.Request.Context(), c.Param("id"), limit, offset, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/push", required(""), func(c *gin.Context) {
		var in WorkspacePush
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.PushWorkspace(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			auditlog.Describe(c, pushAuditDetail(out, c.Param("id")))
		}
		if err == nil && !out.Applied {
			c.Header("Cache-Control", "no-store")
			c.JSON(202, out)
			return
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/rebase", required(""), func(c *gin.Context) {
		var in WorkspaceRebase
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.RebaseWorkspace(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			d := workspaceAuditDetail(out.Workspace)
			d.Changes["resolution_count"] = len(in.Resolutions)
			if out.Commit != nil {
				d.Changes["commit_id"] = out.Commit.ID
			}
			auditlog.Describe(c, d)
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/pull-requests", required(""), func(c *gin.Context) {
		var in PullRequestCreate
		if !body(c, &in) {
			return
		}
		out, err := s.CreatePullRequest(c.Request.Context(), in, *user(c))
		if err == nil {
			auditlog.Describe(c, pullRequestAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.GET("/pull-requests", func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.ListPullRequestPage(c.Request.Context(), c.Query("status"), c.Query("workspace_id"), limit, offset, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/pull-requests/:id", func(c *gin.Context) {
		out, err := s.GetPullRequest(c.Request.Context(), c.Param("id"), collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.PATCH("/pull-requests/:id", required(""), func(c *gin.Context) {
		var in PullRequestEdit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "pull_request", TargetID: c.Param("id")})
		out, err := s.EditPullRequest(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil {
			auditlog.Describe(c, pullRequestAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/pull-requests/:id/merge", required(PermissionReviewManage), func(c *gin.Context) {
		var in PullRequestMerge
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "pull_request", TargetID: c.Param("id")})
		out, err := s.MergePullRequest(c.Request.Context(), c.Param("id"), in, *user(c))
		if err == nil && out.PullRequest != nil {
			d := pullRequestAuditDetail(*out.PullRequest)
			d.Changes["applied"] = out.Applied
			auditlog.Describe(c, d)
		}
		respondCollaboration(c, out, err)
	})
	cat.GET("/commits/:id", func(c *gin.Context) {
		out, err := s.GetCollaborationCommit(c.Request.Context(), c.Param("id"), collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
}
