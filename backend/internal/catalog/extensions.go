package catalog

import "github.com/gin-gonic/gin"

// RegisterModule gives a module the same authenticated, rate-limited and audited
// /api boundary as the catalog. Modules must still enforce their own ownership
// and permissions; this does not grant the caller any additional capability.
func (h HTTP) RegisterModule(r *gin.Engine, register func(*gin.RouterGroup)) {
	api := r.Group("/api")
	api.Use(attachUser(h.Store), routeLimiter(DefaultRateLimitPerMinute, h.Store.DB), auditMiddleware(h.Store), requestLogMiddleware(h.Store))
	register(api)
}

// RequestUser is the identity resolved by the catalog's normal auth middleware.
// The returned value must never be replaced by a user supplied in a request body.
func RequestUser(c *gin.Context) *User { return user(c) }
