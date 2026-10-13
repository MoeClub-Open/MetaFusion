package catalog

// 出站抓取令牌桶：按外部来源站点限速（等待），与 catalog.api_rate_windows / rate_budget.go 无关。
// rate_budget 只对入站 API 计数/拒绝，不做排队；这里是对 Bangumi / DLsite / DMM 的 outbound Wait。

import (
	"context"
	"strings"
	"sync"
	"time"
)

type outboundLimiter struct {
	rate   float64 // tokens per second
	burst  float64
	mu     sync.Mutex
	tokens float64
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

func newOutboundLimiter(ratePerSecond float64, burst int) *outboundLimiter {
	if burst <= 0 {
		burst = 1
	}
	return &outboundLimiter{
		rate:   ratePerSecond,
		burst:  float64(burst),
		tokens: float64(burst),
		now:    time.Now,
		sleep:  outboundSleepCtx,
	}
}

func outboundSleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (l *outboundLimiter) Wait(ctx context.Context) error {
	if l == nil || l.rate <= 0 {
		return nil
	}
	for {
		l.mu.Lock()
		now := l.now()
		if l.last.IsZero() {
			l.last = now
		}
		elapsed := now.Sub(l.last).Seconds()
		l.tokens = l.tokens + elapsed*l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()
		if err := l.sleep(ctx, need); err != nil {
			return err
		}
	}
}

// 默认约 1 req/s（MusicBrainz 量级）；单测可替换 map 内条目。
var outboundLimiters = map[string]*outboundLimiter{
	"bangumi": newOutboundLimiter(1, 1),
	"dlsite":  newOutboundLimiter(1, 1),
	"dmm":     newOutboundLimiter(1, 1),
}

func waitOutbound(ctx context.Context, source string) error {
	src := strings.ToLower(strings.TrimSpace(source))
	if src == "" || src == "auto" {
		src = "bangumi"
	}
	lim := outboundLimiters[src]
	if lim == nil {
		lim = outboundLimiters["bangumi"]
	}
	return lim.Wait(ctx)
}
