package assistant

import (
	"errors"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type ContextRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
	ModelID        string `json:"model_id"`
	Message        string `json:"message,omitempty"`
}

// ContextPreview uses the same conservative bound as the completion runtime.
// It is neither provider-reported token usage nor a sum of billed calls.
type ContextPreview struct {
	InputTokensBound int64 `json:"input_tokens_bound"`
	ContextWindow    int64 `json:"context_window"`
	MaxOutputTokens  int64 `json:"max_output_tokens"`
	MessageCount     int   `json:"message_count"`
	ToolCount        int   `json:"tool_count"`
	Estimated        bool  `json:"estimated"`
}

func (h HTTP) previewContext(c *gin.Context) {
	var in ContextRequest
	if !decodeBody(c, &in) {
		return
	}
	if !validID(in.ModelID) || (in.ConversationID != "" && !validID(in.ConversationID)) || len(in.Message) > 32768 || !utf8.ValidString(in.Message) {
		writeResponse(c, nil, errors.New("assistant_invalid_payload"))
		return
	}
	models, err := h.Store.Models(c.Request.Context(), true)
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	var model Model
	for _, item := range models {
		if item.ID == in.ModelID {
			model = item
			break
		}
	}
	if model.ID == "" {
		writeResponse(c, nil, ErrNotFound)
		return
	}
	var history []Message
	if in.ConversationID != "" {
		conv, err := scanConversation(h.Store.DB.QueryRowContext(c.Request.Context(), `SELECT id::text,owner_id,title,coalesce(model_id::text,''),coalesce(byo_credential_id::text,''),messages,created_at,updated_at FROM assistant.conversations WHERE id=$1 AND owner_id=$2`, in.ConversationID, catalog.RequestUser(c).ID))
		if err != nil {
			writeResponse(c, nil, err)
			return
		}
		history = conv.Messages
	}
	tools, err := completionTools()
	if err != nil {
		writeResponse(c, nil, err)
		return
	}
	messages := chatMessages(history, in.Message)
	writeResponse(c, ContextPreview{InputTokensBound: completionInputBound(messages, tools), ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens, MessageCount: len(messages), ToolCount: len(tools), Estimated: true}, nil)
}
