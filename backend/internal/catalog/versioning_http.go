package catalog

import (
	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

func (h HTTP) registerVersioning(cat *gin.RouterGroup) {
	s := h.Store
	cat.GET("/workspaces/:id/diff", required(""), func(c *gin.Context) {
		if err := versionTextQuery(c, "basis"); err != nil {
			respond(c, nil, err)
			return
		}
		q, err := versionReadQuery(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.WorkspaceDiff(c.Request.Context(), c.Param("id"), c.Query("basis"), q, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/commits/:id/diff", func(c *gin.Context) {
		q, err := versionReadQuery(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.CommitDiff(c.Request.Context(), c.Param("id"), q, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/pull-requests/:id/diff", func(c *gin.Context) {
		q, err := versionReadQuery(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.PullRequestDiff(c.Request.Context(), c.Param("id"), q, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/pull-requests/:id/commits", func(c *gin.Context) {
		q, err := versionHistoryQuery(c, false)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.PullRequestCommitHistory(c.Request.Context(), c.Param("id"), q, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/head", func(c *gin.Context) {
		out, err := s.CatalogVersionHead(c.Request.Context())
		respondCollaboration(c, out, err)
	})
	cat.GET("/history", func(c *gin.Context) {
		query, err := versionHistoryQuery(c, false)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.CatalogVersionHistory(c.Request.Context(), query, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/fork", required(""), func(c *gin.Context) {
		var in WorkspaceFork
		if !body(c, &in) {
			return
		}
		out, err := s.ForkWorkspace(c.Request.Context(), in, *user(c))
		if err == nil {
			auditlog.Describe(c, workspaceAuditDetail(out))
		}
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/version", required(""), func(c *gin.Context) {
		var in WorkspaceVersionAction
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id"), Changes: map[string]any{"operation": in.Operation, "source_commits": in.CommitIDs}})
		out, err := s.ApplyWorkspaceVersion(c.Request.Context(), c.Param("id"), in, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id/status", required(""), func(c *gin.Context) {
		out, err := s.WorkspaceVersionStatus(c.Request.Context(), c.Param("id"), *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id/events", required(""), func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.WorkspaceEvents(c.Request.Context(), c.Param("id"), limit, offset, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id/tags", required(""), func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.WorkspaceTags(c.Request.Context(), c.Param("id"), limit, offset, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/tags", required(""), func(c *gin.Context) {
		var in WorkspaceTagEdit
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id")})
		out, err := s.EditWorkspaceTag(c.Request.Context(), c.Param("id"), in, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/workspaces/:id/stashes", required(""), func(c *gin.Context) {
		limit, offset, err := collaborationPagination(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.WorkspaceStashes(c.Request.Context(), c.Param("id"), limit, offset, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.POST("/workspaces/:id/stashes", required(""), func(c *gin.Context) {
		var in WorkspaceStashAction
		if !body(c, &in) {
			return
		}
		auditlog.Describe(c, auditlog.Detail{TargetType: "workspace", TargetID: c.Param("id"), Changes: map[string]any{"operation": in.Operation}})
		out, err := s.ApplyWorkspaceStash(c.Request.Context(), c.Param("id"), in, *user(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/commits/:id/snapshot", func(c *gin.Context) {
		query, err := versionReadQuery(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.GetCommitSnapshot(c.Request.Context(), c.Param("id"), query, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/commits/:id/compare", func(c *gin.Context) {
		query, err := versionReadQuery(c)
		if err != nil {
			respond(c, nil, err)
			return
		}
		out, err := s.CompareVersions(c.Request.Context(), c.Query("base"), c.Param("id"), query, collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
	cat.GET("/commits/:id/blame", func(c *gin.Context) {
		out, err := s.VersionFieldAuthor(c.Request.Context(), c.Param("id"), c.Query("target"), c.Query("record_id"), c.Query("pointer"), collaborationViewer(c))
		respondCollaboration(c, out, err)
	})
}
