// Package worker runs everything that must not happen inside a web request:
// sending campaign messages, starting scheduled campaigns, processing Bird
// webhook events and fetching per-message costs.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wacampaigns/internal/bird"
	"wacampaigns/internal/config"
	"wacampaigns/internal/settings"
)

// Only one worker may send at a time; a Postgres advisory lock enforces it
// even if a second worker is started by mistake.
const leaderLockID = 72_001_974

type Worker struct {
	db   *pgxpool.Pool
	bird *bird.Client
	cfg  *config.Config

	mu          sync.Mutex
	settings    settings.Settings
	settingsAt  time.Time
	lastSendAt  time.Time
	campaignMem map[int64]*campaignCache
}

func New(db *pgxpool.Pool, bc *bird.Client, cfg *config.Config) *Worker {
	return &Worker{db: db, bird: bc, cfg: cfg, campaignMem: map[int64]*campaignCache{}}
}

// Settings are re-read every 10s so admin changes (send rate, rate card,
// stop keywords) apply without a restart.
func (w *Worker) Settings(ctx context.Context) settings.Settings {
	w.mu.Lock()
	defer w.mu.Unlock()
	if time.Since(w.settingsAt) > 10*time.Second {
		if s, err := settings.Load(ctx, w.db); err == nil {
			w.settings = s
			w.settingsAt = time.Now()
		} else if w.settingsAt.IsZero() {
			w.settings = settings.Defaults()
		}
	}
	return w.settings
}

func (w *Worker) Run(ctx context.Context) error {
	conn, err := w.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	for {
		var ok bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, leaderLockID).Scan(&ok); err != nil {
			return err
		}
		if ok {
			// The connection returns to the pool afterwards, so unlock explicitly.
			defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, leaderLockID)
			break
		}
		slog.Info("another worker holds the lock; waiting")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Second):
		}
	}
	slog.Info("worker started", "bird_configured", w.bird.Configured())

	// We hold the lock, so nothing else is mid-send: rows left in "sending" by
	// a crash or restart are recovered before sending resumes.
	w.recoverStale(ctx, 0)

	var wg sync.WaitGroup
	loops := []struct {
		name  string
		every time.Duration
		fn    func(context.Context)
	}{
		{"scheduler", 5 * time.Second, w.schedulerTick},
		{"webhooks", 2 * time.Second, w.processWebhooks},
		{"costs", 60 * time.Second, w.fetchCosts},
		{"template-sync", time.Hour, w.syncTemplates},
	}
	for _, l := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runEvery(ctx, l.name, l.every, l.fn)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.sendLoop(ctx)
	}()
	wg.Wait()
	slog.Info("worker stopped")
	return nil
}

func runEvery(ctx context.Context, name string, every time.Duration, fn func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("worker loop panic", "loop", name, "panic", r)
				}
			}()
			fn(ctx)
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
