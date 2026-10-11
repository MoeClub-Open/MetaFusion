package catalog

import (
	"errors"

	"github.com/gin-gonic/gin"
	auditlog "github.com/metafusion/metafusion-app/internal/audit"
)

func respondEntityWrite(c *gin.Context, v Entity, err error) {
	var identity *commitIdentityError
	if errors.As(err, &identity) {
		auditlog.Fail(c, "identity_candidates_changed")
		c.JSON(409, gin.H{"error": "identity_candidates_changed", "applied": false, "identity_review": identity})
		return
	}
	respond(c, v, err)
}
