// Package telemetry aggregates API traffic without retaining individual requests.
// Copies in the four services are checked by scripts/check_api_telemetry.py.
package telemetry

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed schema.sql
var Schema string

type key struct {
	Day           time.Time
	Method, Route string
	Status        int
}

type counters struct{ Count, TotalMS, MaxMS int64 }

type Collector struct {
	db      *sql.DB
	service string
	mu      sync.Mutex
	pending map[key]counters
	stop    chan struct{}
	done    chan struct{}
}

func New(db *sql.DB, service string) *Collector {
	c := &Collector{db: db, service: service, pending: make(map[key]counters), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.flush()
			case <-c.stop:
				c.flush()
				return
			}
		}
	}()
	return c
}

// Close runs after HTTP shutdown so completed in-flight requests are flushed too.
func (c *Collector) Close() { close(c.stop); <-c.done }

func (c *Collector) Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start := time.Now()
		ctx.Next()
		route := ctx.FullPath()
		if !strings.HasPrefix(ctx.Request.URL.Path, "/api/") || route == "/api/version" ||
			route == "/api/admin/api-statistics" {
			return
		}
		if route == "" {
			route = "<unmatched>"
		}
		k := key{Day: start.UTC().Truncate(24 * time.Hour), Method: ctx.Request.Method, Route: route, Status: ctx.Writer.Status()}
		ms := time.Since(start).Milliseconds()
		c.mu.Lock()
		v := c.pending[k]
		v.Count++
		v.TotalMS += ms
		v.MaxMS = max(v.MaxMS, ms)
		c.pending[k] = v
		c.mu.Unlock()
	}
}

func (c *Collector) flush() {
	c.mu.Lock()
	batch := c.pending
	c.pending = make(map[key]counters)
	c.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.persist(ctx, batch)
	if err == nil {
		return
	}
	// Retry aggregates on the next flush; never write one row per request.
	c.mu.Lock()
	for k, v := range batch {
		old := c.pending[k]
		v.Count += old.Count
		v.TotalMS += old.TotalMS
		v.MaxMS = max(v.MaxMS, old.MaxMS)
		c.pending[k] = v
	}
	c.mu.Unlock()
	slog.Error("API statistics flush failed", "service", c.service, "error", err)
}

func (c *Collector) persist(ctx context.Context, batch map[key]counters) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO audit.api_statistics(day,service,method,route,status,requests,total_ms,max_ms)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
	 ON CONFLICT(day,service,method,route,status) DO UPDATE SET
	 requests=api_statistics.requests+EXCLUDED.requests,
	 total_ms=api_statistics.total_ms+EXCLUDED.total_ms,
	 max_ms=GREATEST(api_statistics.max_ms,EXCLUDED.max_ms)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, v := range batch {
		if _, err = stmt.ExecContext(ctx, k.Day, c.service, k.Method, k.Route, k.Status, v.Count, v.TotalMS, v.MaxMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}
