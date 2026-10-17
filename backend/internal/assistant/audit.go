package assistant

import (
	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

// Record actions and outcomes without copying prompts or secrets to the audit.
func AuditActions() map[string]string {
	return map[string]string{
		"POST /api/assistant/admin/requests/:id/cancel":      "assistant.request_cancelled",
		"POST /api/assistant/credentials":                    "assistant.credential_created",
		"PUT /api/assistant/credentials/:id":                 "assistant.credential_updated",
		"DELETE /api/assistant/credentials/:id":              "assistant.credential_deleted",
		"POST /api/assistant/conversations":                  "assistant.conversation_created",
		"DELETE /api/assistant/conversations/:id":            "assistant.conversation_deleted",
		"POST /api/assistant/chat":                           "assistant.chat_requested",
		"POST /api/assistant/generated/:entity_id":           "assistant.overview_generated",
		"PUT /api/assistant/admin/settings":                  "assistant.settings_updated",
		"POST /api/assistant/admin/providers":                "assistant.provider_created",
		"PUT /api/assistant/admin/providers/:id":             "assistant.provider_updated",
		"DELETE /api/assistant/admin/providers/:id":          "assistant.provider_deleted",
		"POST /api/assistant/admin/models":                   "assistant.model_created",
		"PUT /api/assistant/admin/models/:id":                "assistant.model_updated",
		"DELETE /api/assistant/admin/models/:id":             "assistant.model_deleted",
		"PUT /api/assistant/admin/credentials/:id":           "assistant.admin_credential_updated",
		"DELETE /api/assistant/admin/credentials/:id":        "assistant.admin_credential_deleted",
		"POST /api/assistant/admin/accounts/:user_id/adjust": "assistant.credits_adjusted",
		"DELETE /api/assistant/admin/conversations/:id":      "assistant.admin_conversation_deleted",
		"DELETE /api/assistant/admin/generated/:id":          "assistant.overview_deleted",
	}
}

func (h HTTP) auditMiddleware() gin.HandlerFunc {
	return auditlog.Middleware(auditlog.Options{Recorder: h.Catalog.Audit, Actions: AuditActions(), Actor: catalog.DirectoryActor})
}
