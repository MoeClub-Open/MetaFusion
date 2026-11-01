// Package httplog provides HTTP request identifiers for errors and operation audits.
package httplog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	Header     = "X-Request-ID"
	ContextKey = "request_id"
)

// NewID returns a 32-char hex request id; falls back to a nanosecond stamp.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// RequestID accepts X-Request-ID (trimmed, max 128 chars of printable ASCII) or
// generates one, then stores it in the Gin context and the response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := sanitize(c.GetHeader(Header))
		if rid == "" {
			rid = NewID()
		}
		c.Set(ContextKey, rid)
		c.Header(Header, rid)
		c.Next()
	}
}

func sanitize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 128 {
		return ""
	}
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	return v
}

// FromContext returns the request_id set by RequestID, or "".
func FromContext(c *gin.Context) string {
	v, _ := c.Get(ContextKey)
	s, _ := v.(string)
	return s
}
