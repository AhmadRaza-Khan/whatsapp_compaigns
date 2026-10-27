package worker

import (
	"context"
	"log/slog"
	"time"

	"wacampaigns/internal/campaigns"
	"wacampaigns/internal/templates"
)

func (w *Worker) schedulerTick(ctx context.Context) {
	rows, err := w.db.Query(ctx, `SELECT id FROM campaigns WHERE status='scheduled' AND scheduled_at <= now() ORDER BY scheduled_at`)
	if err != nil {
		slog.Error("scheduler query failed", "err", err)
		return
	}
	var due []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			due = append(due, id)
		}
	}
	rows.Close()
	for _, id := range due {
		ok, n, err := campaigns.Launch(ctx, w.db, id)
		if err != nil {
			slog.Error("launch failed", "campaign", id, "err", err)
			continue
		}
		if ok {
			slog.Info("campaign started", "campaign", id, "recipients", n)
		}
	}

	w.recoverStale(ctx, 2*time.Minute)

	// A campaign is complete once nothing is waiting to be sent.
	tag, err := w.db.Exec(ctx, `UPDATE campaigns c SET status='completed', completed_at=now(), updated_at=now()
		WHERE c.status='sending' AND NOT EXISTS (
			SELECT 1 FROM campaign_recipients r WHERE r.campaign_id=c.id AND r.status IN ('pending','sending'))`)
	if err == nil && tag.RowsAffected() > 0 {
		slog.Info("campaigns completed", "count", tag.RowsAffected())
	}
}

// recoverStale handles rows stuck in "sending" because the worker stopped
// mid-request. Within Bird's idempotency window (~3h) they are retried with
// the same Idempotency-Key, which replays the original result rather than
// sending again. Older ones are marked "unknown" instead of risking a double
// send; a late webhook (matched by recipient_id tag) still fills in the status.
func (w *Worker) recoverStale(ctx context.Context, minAge time.Duration) {
	_, err := w.db.Exec(ctx, `UPDATE campaign_recipients SET status='pending', locked_at=NULL, next_attempt_at=now(), updated_at=now()
		WHERE status='sending' AND locked_at <= now() - make_interval(secs => $1) AND locked_at > now() - interval '150 minutes'`, minAge.Seconds())
	if err != nil {
		slog.Error("recover stale failed", "err", err)
	}
	_, _ = w.db.Exec(ctx, `UPDATE campaign_recipients SET status='unknown', locked_at=NULL, updated_at=now(),
		error_reason='Worker was stopped for hours during this send; not retried to avoid a duplicate. Check Bird.'
		WHERE status='sending' AND locked_at <= now() - interval '150 minutes'`)
}

func (w *Worker) syncTemplates(ctx context.Context) {
	if !w.bird.Configured() {
		return
	}
	if n, err := templates.Sync(ctx, w.db, w.bird); err != nil {
		slog.Warn("template sync failed", "err", err)
	} else {
		slog.Info("templates synced", "count", n)
	}
	_, _ = w.db.Exec(ctx, `DELETE FROM webhook_events WHERE processed_at < now() - interval '30 days'`)
}

func (w *Worker) fetchCosts(ctx context.Context) {
	if !w.bird.Configured() {
		return
	}
	rows, err := w.db.Query(ctx, `SELECT id, bird_message_id FROM campaign_recipients
		WHERE bird_message_id IS NOT NULL AND cost IS NULL AND cost_checks < 15
		  AND status IN ('sent','delivered','read','failed') AND updated_at < now() - interval '1 minute'
		ORDER BY updated_at LIMIT 100`)
	if err != nil {
		return
	}
	type item struct {
		id  int64
		mid string
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.mid) == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		msg, err := w.bird.GetMessage(ctx, it.mid)
		if err != nil || msg.Cost == nil || msg.Cost.Amount == "" {
			_, _ = w.db.Exec(ctx, `UPDATE campaign_recipients SET cost_checks=cost_checks+1, updated_at=now() WHERE id=$1`, it.id)
		} else {
			_, _ = w.db.Exec(ctx, `UPDATE campaign_recipients SET cost=$2::numeric, cost_currency=$3, updated_at=now() WHERE id=$1`,
				it.id, msg.Cost.Amount, msg.Cost.CurrencyCode)
		}
		if !sleepCtx(ctx, 300*time.Millisecond) {
			return
		}
	}
}
