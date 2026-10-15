package catalog

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Export visible current facts. External edits use the shared commit protocol.
// Attach identity on this subgroup so registration order cannot drop it.
func (h HTTP) registerExchange(api *gin.RouterGroup) {
	s := h.Store
	ex := api.Group("/exchange", attachUser(s))
	ex.GET("/entities/:id", func(c *gin.Context) {
		e, err := s.Get(c.Request.Context(), c.Param("id"), user(c))
		if err != nil {
			respond(c, nil, err)
			return
		}
		b, err := json.Marshal(e)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encode_failed"})
			return
		}
		c.Data(http.StatusOK, "application/json", b)
	})
}
