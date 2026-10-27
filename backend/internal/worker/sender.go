package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"wacampaigns/internal/bird"
	"wacampaigns/internal/campaigns"
	"wacampaigns/internal/templates"
)

const maxAttempts = 5

type campaignCache struct {
	c        *campaigns.Campaign
	spec     templates.Spec
	loadedAt time.Time
}

type job struct {
	id         int64
	campaignID int64
	contactID  *int64
	phone      string
	name       string
	token      string
	attempts   int
}

func (w *Worker) sendLoop(ctx context.Context) {
	for ctx.Err() == nil {
		// Throttle to the Bird plan's per-minute limit.
		rate := w.Settings(ctx).SendRatePerMinute
		if rate < 1 {
			rate = 1
		}
		interval := time.Minute / time.Duration(rate)
		if wait := time.Until(w.lastSendAt.Add(interval)); wait > 0 {
			if !sleepCtx(ctx, wait) {
				return
			}
		}

		j, err := w.claim(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				slog.Error("claim failed", "err", err)
			}
			if !sleepCtx(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if w.process(ctx, j) {
			w.lastSendAt = time.Now()
		}
	}
}

// claim atomically moves one due recipient of a sending campaign to "sending".
// SKIP LOCKED plus the status change means a row is never claimed twice.
func (w *Worker) claim(ctx context.Context) (*job, error) {
	var j job
	err := w.db.QueryRow(ctx, `
		UPDATE campaign_recipients r SET status='sending', locked_at=now(), attempts=r.attempts+1, updated_at=now()
		WHERE r.id = (
			SELECT r2.id FROM campaign_recipients r2 JOIN campaigns c ON c.id = r2.campaign_id
			WHERE r2.status='pending' AND r2.next_attempt_at <= now() AND c.status='sending'
			ORDER BY c.started_at, r2.id
			FOR UPDATE OF r2 SKIP LOCKED LIMIT 1)
		RETURNING r.id, r.campaign_id, r.contact_id, r.phone, r.contact_name, r.token, r.attempts`).
		Scan(&j.id, &j.campaignID, &j.contactID, &j.phone, &j.name, &j.token, &j.attempts)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (w *Worker) campaign(ctx context.Context, id int64) (*campaignCache, error) {
	if cc, ok := w.campaignMem[id]; ok && time.Since(cc.loadedAt) < 30*time.Second {
		return cc, nil
	}
	c, err := campaigns.Get(ctx, w.db, id)
	if err != nil {
		return nil, err
	}
	spec, err := campaigns.TemplateSpec(ctx, w.db, c.TemplateSlug)
	if err != nil {
		return nil, err
	}
	cc := &campaignCache{c: c, spec: spec, loadedAt: time.Now()}
	w.campaignMem[id] = cc
	return cc, nil
}

// IdempotencyKey is stable per recipient row, so a retry after a crash or a
// lost response replays Bird's original result instead of sending twice.
func IdempotencyKey(campaignID, recipientID int64) string {
	return fmt.Sprintf("campaign/%d/recipient/%d", campaignID, recipientID)
}

// process sends one message. It returns true if a request reached Bird
// (and so counts against the rate limit).
func (w *Worker) process(ctx context.Context, j *job) bool {
	// Finish a claimed job even during shutdown, so its outcome is recorded.
	ctx = context.WithoutCancel(ctx)

	// Send-time opt-out check: a STOP may have arrived after launch.
	var optedOut bool
	if j.contactID == nil {
		w.finish(ctx, j.id, "skipped", "", "Contact was deleted")
		return false
	}
	if err := w.db.QueryRow(ctx, `SELECT opted_out FROM contacts WHERE id=$1`, *j.contactID).Scan(&optedOut); err != nil {
		w.finish(ctx, j.id, "skipped", "", "Contact was deleted")
		return false
	}
	if optedOut {
		w.finish(ctx, j.id, "skipped", "opted_out", "Contact opted out")
		return false
	}

	cc, err := w.campaign(ctx, j.campaignID)
	if err != nil {
		w.finish(ctx, j.id, "failed", "config", err.Error())
		return false
	}
	s := w.Settings(ctx)
	req := bird.SendRequest{
		To: j.phone,
		Template: bird.TemplateRef{
			Slug:       cc.c.TemplateSlug,
			Language:   cc.c.TemplateLanguage,
			Components: cc.spec.Build(cc.c.Variables, j.name, s.NameFallback, fmt.Sprintf("%d/%s", j.campaignID, j.token)),
		},
		Tags: []bird.Tag{
			{Name: "campaign_id", Value: strconv.FormatInt(j.campaignID, 10)},
			{Name: "recipient_id", Value: strconv.FormatInt(j.id, 10)},
			{Name: "contact_id", Value: strconv.FormatInt(*j.contactID, 10)},
		},
		Metadata: map[string]any{"campaign_id": j.campaignID, "recipient_id": j.id, "contact_id": *j.contactID},
	}
	if s.BirdFrom != "" && !isBirdManaged(cc.c.TemplateSlug) {
		req.From = s.BirdFrom
	}

	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := w.bird.SendTemplate(sendCtx, req, IdempotencyKey(j.campaignID, j.id))
	if err == nil {
		_, err := w.db.Exec(ctx, `UPDATE campaign_recipients SET
			status = CASE WHEN status IN ('sending','pending','unknown') THEN 'accepted' ELSE status END,
			bird_message_id = $2, accepted_at = coalesce(accepted_at, now()), locked_at = NULL,
			error_code = '', error_reason = '', updated_at = now()
			WHERE id = $1`, j.id, msg.ID)
		if err != nil {
			slog.Error("store message id failed", "recipient", j.id, "err", err)
		}
		return true
	}

	var ae *bird.APIError
	isAPI := errors.As(err, &ae)
	switch {
	case isAPI && (ae.Status == 401 || ae.Status == 403 || ae.Status == 402):
		// Account-level problem: don't burn attempts; pause until fixed.
		reason := "Bird rejected the API key. Check BIRD_API_KEY, then resume."
		if ae.Status == 402 {
			reason = "Bird wallet balance is empty. Top up in Bird, then resume."
		}
		w.requeue(ctx, j.id, 0, err.Error(), true)
		w.pause(ctx, j.campaignID, reason)
	case bird.IsTemporary(err) && j.attempts < maxAttempts:
		backoff := time.Duration(30*(1<<(2*(j.attempts-1)))) * time.Second // 30s, 2m, 8m, 32m
		w.requeue(ctx, j.id, backoff, err.Error(), false)
	default:
		code, reason := "send_error", err.Error()
		if isAPI {
			code = ae.Code
			if code == "" {
				code = strconv.Itoa(ae.Status)
			}
			reason = ae.Message
		}
		w.finish(ctx, j.id, "failed", code, reason)
	}
	slog.Warn("send failed", "recipient", j.id, "attempt", j.attempts, "err", err)
	return true
}

func isBirdManaged(slug string) bool { return len(slug) > 5 && slug[:5] == "bird_" }

func (w *Worker) finish(ctx context.Context, id int64, status, code, reason string) {
	_, err := w.db.Exec(ctx, `UPDATE campaign_recipients SET status=$2, error_code=$3, error_reason=$4,
		failed_at = CASE WHEN $2='failed' THEN now() END, locked_at=NULL, updated_at=now()
		WHERE id=$1 AND status='sending'`, id, status, code, reason)
	if err != nil {
		slog.Error("update recipient failed", "recipient", id, "err", err)
	}
}

func (w *Worker) requeue(ctx context.Context, id int64, after time.Duration, reason string, refundAttempt bool) {
	refund := 0
	if refundAttempt {
		refund = 1
	}
	_, err := w.db.Exec(ctx, `UPDATE campaign_recipients SET status='pending', locked_at=NULL,
		next_attempt_at = now() + make_interval(secs => $2), attempts = attempts - $3,
		error_reason=$4, updated_at=now()
		WHERE id=$1 AND status='sending'`, id, after.Seconds(), refund, reason)
	if err != nil {
		slog.Error("requeue failed", "recipient", id, "err", err)
	}
}

func (w *Worker) pause(ctx context.Context, campaignID int64, reason string) {
	_, _ = w.db.Exec(ctx, `UPDATE campaigns SET status='paused', status_reason=$2, updated_at=now()
		WHERE id=$1 AND status='sending'`, campaignID, reason)
	delete(w.campaignMem, campaignID)
	slog.Warn("campaign paused", "campaign", campaignID, "reason", reason)
}
