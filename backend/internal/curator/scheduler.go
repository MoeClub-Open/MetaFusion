package curator

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type schedulerLease struct {
	conn   interface{ PingContext(context.Context) error }
	cancel context.CancelFunc
	lost   atomic.Bool
}

func (lease *schedulerLease) check(ctx context.Context) error {
	if lease.lost.Load() {
		return ErrLeaseLost
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := lease.conn.PingContext(probe); err != nil {
		lease.lost.Store(true)
		lease.cancel()
		return ErrLeaseLost
	}
	return nil
}

func (s *Service) checkLease(ctx context.Context) error {
	lease := s.lease.Load()
	if lease == nil {
		return ErrLeaseLost
	}
	return lease.check(ctx)
}

const (
	leaseRetry        = time.Minute
	sweepBacklogLimit = 200
)

// Run keeps one scheduler per database. The advisory lock is session-scoped,
// so a crashed process releases it when its connection closes.
func (s *Service) Run(ctx context.Context) {
	s.wg.Add(1)
	defer s.wg.Done()
	if s.Assistant != nil {
		s.Assistant.SubscribeWake(ctx, func(extra string) {
			if extra == "" || extra == "curator" {
				s.wakeLocal()
			}
		})
	}
	for ctx.Err() == nil {
		conn, err := s.Catalog.DB.Conn(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("curator cannot open scheduler connection", "err", err)
			}
			s.wait(ctx, leaseRetry)
			continue
		}
		ok, err := s.claimLease(ctx, conn)
		if err != nil || !ok {
			if err != nil && ctx.Err() == nil {
				slog.Warn("curator scheduler lease failed", "err", err)
			}
			_ = conn.Close()
			s.wait(ctx, leaseRetry)
			continue
		}
		s.leaseHeld.Store(true)
		leaseCtx, cancel := context.WithCancel(ctx)
		lease := &schedulerLease{conn: conn, cancel: cancel}
		s.lease.Store(lease)
		watched := make(chan struct{})
		go func() {
			defer close(watched)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-leaseCtx.Done():
					return
				case <-ticker.C:
					if lease.check(leaseCtx) != nil {
						slog.Warn("curator scheduler lost lease connection")
						return
					}
				}
			}
		}()
		slog.Info("curator scheduler acquired lease")
		s.schedule(leaseCtx)
		cancel()
		<-watched
		s.lease.Store(nil)
		s.leaseHeld.Store(false)
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		s.releaseLease(releaseCtx, conn)
		releaseCancel()
		_ = conn.Close()
		s.wait(ctx, leaseRetry)
	}
}

func (s *Service) schedule(ctx context.Context) {
	for ctx.Err() == nil {
		settings, err := s.Settings(ctx)
		if err != nil {
			slog.Warn("curator settings unavailable", "err", err)
			s.wait(ctx, 30*time.Second)
			continue
		}
		if !settings.Enabled || settings.Paused {
			s.wait(ctx, 30*time.Second)
			continue
		}
		if s.checkLease(ctx) != nil {
			return
		}
		if err = s.recoverInterrupted(ctx); err != nil {
			slog.Warn("curator recovery failed", "err", err)
			s.wait(ctx, 30*time.Second)
			continue
		}
		runs, err := s.countRunsLastHour(ctx)
		if err != nil || runs >= int64(settings.MaxRunsPerHour) {
			s.wait(ctx, time.Minute)
			continue
		}
		if err = s.discoverReviews(ctx, settings); err != nil {
			slog.Warn("curator review discovery failed", "err", err)
		}
		forced := s.forceSweep.Swap(false) || settings.SweepRequestedAt != nil
		if pending, e := s.PendingCount(ctx, settings); e == nil && (forced || pending < sweepBacklogLimit) {
			if settings.SweepRequestedAt != nil {
				_ = s.AckSweepRequest(ctx, *settings.SweepRequestedAt)
			}
			s.runSweep(ctx, settings)
		}
		tasks, e := s.dueTasks(ctx, settings)
		if e != nil {
			slog.Warn("curator task discovery failed", "err", e)
		} else if len(tasks) > 0 {
			if _, e = s.runManager(ctx, settings, tasks); e != nil {
				slog.Warn("curator manager failed", "err", e)
			}
		}
		s.wait(ctx, time.Duration(settings.IntervalSeconds)*time.Second)
	}
}

func (s *Service) runSweep(ctx context.Context, settings Settings) {
	started := time.Now()
	if settings.SweepDetector < 0 && settings.AllowStructures {
		n, err := s.AuditQualityBatch(ctx, settings)
		detail := map[string]any{"detector": "quality", "findings": n}
		errText := ""
		if err != nil {
			errText = safeCode(err)
			slog.Warn("curator quality sweep failed", "err", err)
			// Keep the quality cursor for a later retry, but let other detectors run.
			_ = s.SetSweepState(ctx, 0)
		}
		if n > 0 || err != nil {
			s.RecordSweep(ctx, kindQuality, detail, started, errText)
		}
		return
	}
	next, n, err := s.SweepDetector(ctx, settings, settings.SweepDetector)
	detail := map[string]any{"detector": settings.SweepDetector, "findings": n}
	errText := ""
	if err != nil {
		errText = safeCode(err)
		slog.Warn("curator detector failed", "detector", settings.SweepDetector, "err", err)
	}
	if n > 0 || err != nil {
		s.RecordSweep(ctx, kindSweep, detail, started, errText)
	}
	_ = s.SetSweepState(ctx, next)
}

func (s *Service) SetSweepState(ctx context.Context, detector int) error {
	_, err := s.Catalog.DB.ExecContext(ctx, `UPDATE curator.settings SET sweep_detector=$1,last_sweep_at=now(),updated_at=now() WHERE singleton`, detector)
	return err
}

func (s *Service) wait(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-s.wake:
	case <-timer.C:
	}
}
