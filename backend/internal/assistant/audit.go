package assistant

import (
	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

// Record actions and outcomes without copying prompts or secrets to the audit.
func AuditActions() map[string]string {
	return map[string]string{
		"POST /api/assistant/search/:operation":              "assistant.web_requested",
		"POST /api/assistant/admin/search/providers":         "assistant.search_provider_created",
		"PUT /api/assistant/admin/search/providers/:id":      "assistant.search_provider_updated",
		"POST /api/assistant/admin/requests/:id/cancel":      "assistant.request_cancelled",
		"POST /api/assistant/missions":                       "assistant.mission_created",
		"DELETE /api/assistant/missions/:id":                 "assistant.mission_deleted",
		"POST /api/assistant/missions/:id/steer":             "assistant.mission_steered",
		"POST /api/assistant/missions/:id/pause":             "assistant.mission_paused",
		"POST /api/assistant/missions/:id/resume":            "assistant.mission_resumed",
		"POST /api/assistant/missions/:id/stop":              "assistant.mission_stopped",
		"POST /api/assistant/admin/missions/:id/stop":        "assistant.admin_mission_stopped",
		"POST /api/assistant/byo/providers":                  "assistant.byo_provider_created",
		"PUT /api/assistant/byo/providers/:id":               "assistant.byo_provider_updated",
		"DELETE /api/assistant/byo/providers/:id":            "assistant.byo_provider_deleted",
		"POST /api/assistant/byo/models":                     "assistant.byo_model_created",
		"PUT /api/assistant/byo/models/:id":                  "assistant.byo_model_updated",
		"DELETE /api/assistant/byo/models/:id":               "assistant.byo_model_deleted",
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
		"PUT /api/assistant/admin/byo/providers/:id":         "assistant.admin_byo_provider_updated",
		"DELETE /api/assistant/admin/byo/providers/:id":      "assistant.admin_byo_provider_deleted",
		"PUT /api/assistant/admin/byo/models/:id":            "assistant.admin_byo_model_updated",
		"DELETE /api/assistant/admin/byo/models/:id":         "assistant.admin_byo_model_deleted",
		"POST /api/assistant/admin/accounts/:user_id/adjust": "assistant.credits_adjusted",
		"DELETE /api/assistant/admin/conversations/:id":      "assistant.admin_conversation_deleted",
		"DELETE /api/assistant/admin/generated/:id":          "assistant.overview_deleted",
	}
}

func (h HTTP) auditMiddleware() gin.HandlerFunc {
	return auditlog.Middleware(auditlog.Options{Recorder: h.Catalog.Audit, Actions: AuditActions(), Actor: catalog.DirectoryActor})
}
