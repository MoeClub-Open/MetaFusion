// Package httplog provides the catalog HTTP request-id middleware and access log.
// request_id is accepted from X-Request-ID or generated, then written to the Gin
// context, the response header and every access-log line so Leo can match a
// returned request_id to container stdout.
package httplog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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

// AccessLogger writes one gin-style access line that includes request_id.
// Mount after RequestID so the id is already on the context.
func AccessLogger(out io.Writer) gin.HandlerFunc {
	if out == nil {
		out = gin.DefaultWriter
	}
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Output: out,
		Formatter: func(p gin.LogFormatterParams) string {
			rid, _ := p.Keys[ContextKey].(string)
			if rid == "" {
				rid = "-"
			}
			return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %s | %-7s %s\n",
				p.TimeStamp.Format("2006/01/02 - 15:04:05"),
				p.StatusCode,
				p.Latency,
				p.ClientIP,
				rid,
				p.Method,
				p.Path,
			)
		},
	})
}
