package assistant

import (
	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type WebSearchRequest struct {
	SearchInput
	ClientRequestID string `json:"client_request_id"`
	ProviderID      string `json:"provider_id,omitempty"`
	BYOProviderID   string `json:"byo_provider_id,omitempty"`
}

func (h HTTP) registerSearch(self *gin.RouterGroup, admin *gin.RouterGroup) {
	self.GET("/search/providers", func(c *gin.Context) {
		v, e := h.Store.SearchProviders(c.Request.Context(), true)
		writeResponse(c, v, e)
	})
	self.POST("/search/:operation", func(c *gin.Context) {
		var in WebSearchRequest
		if !decodeBody(c, &in) {
			return
		}
		v, e := h.Store.WebSearch(c.Request.Context(), SearchOptions{RequestID: in.ClientRequestID, UserID: catalog.RequestUser(c).ID, ProviderID: in.ProviderID, BYOProviderID: in.BYOProviderID, Purpose: "search"}, c.Param("operation"), in.SearchInput)
		writeResponse(c, v, e)
	})
	self.GET("/search/requests", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.SearchRequests(c.Request.Context(), catalog.RequestUser(c).ID, false, l, o)
		writeResponse(c, v, e)
	})
	admin.GET("/search/providers", func(c *gin.Context) {
		v, e := h.Store.SearchProviders(c.Request.Context(), false)
		writeResponse(c, v, e)
	})
	save := func(update bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			var in SearchProviderInput
			if !decodeBody(c, &in) {
				return
			}
			if update {
				in.ID = c.Param("id")
			} else {
				in.ID = ""
			}
			v, e := h.Store.SaveSearchProvider(c.Request.Context(), in)
			writeResponse(c, v, e)
		}
	}
	admin.POST("/search/providers", save(false))
	admin.PUT("/search/providers/:id", save(true))
	admin.GET("/search/requests", func(c *gin.Context) {
		l, o := pageArgs(c)
		v, e := h.Store.SearchRequests(c.Request.Context(), c.Query("user_id"), true, l, o)
		writeResponse(c, v, e)
	})
}
