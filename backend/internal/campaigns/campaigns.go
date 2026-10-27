// Package campaigns holds the campaign logic shared by the API and the worker.
package campaigns

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wacampaigns/internal/templates"
)

type Audience struct {
	All      bool     `json:"all"`
	GroupIDs []int64  `json:"group_ids"`
	Tags     []string `json:"tags"`
}

func (a Audience) Empty() bool { return !a.All && len(a.GroupIDs) == 0 && len(a.Tags) == 0 }

func (a Audience) args() []any {
	g, t := a.GroupIDs, a.Tags
	if g == nil {
		g = []int64{}
	}
	if t == nil {
		t = []string{}
	}
	return []any{a.All, g, t}
}

// audienceWhere matches contacts in the audience. Args: $1 all, $2 group ids, $3 tags.
const audienceWhere = `($1 OR c.tags && $3::text[] OR EXISTS (
	SELECT 1 FROM contact_groups cg WHERE cg.contact_id = c.id AND cg.group_id = ANY($2::bigint[])))`

// Count returns how many contacts would receive the campaign and how many
// were excluded because they opted out.
func Count(ctx context.Context, db *pgxpool.Pool, a Audience) (eligible, optedOut int, err error) {
	if a.Empty() {
		return 0, 0, nil
	}
	err = db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT c.opted_out), count(*) FILTER (WHERE c.opted_out)
		FROM contacts c WHERE `+audienceWhere, a.args()...).Scan(&eligible, &optedOut)
	return
}

type Campaign struct {
	ID               int64               `json:"id"`
	Name             string              `json:"name"`
	TemplateSlug     string              `json:"template_slug"`
	TemplateLanguage string              `json:"template_language"`
	Variables        templates.Variables `json:"variables"`
	Audience         Audience            `json:"audience"`
	Status           string              `json:"status"`
	StatusReason     string              `json:"status_reason"`
	ScheduledAt      *time.Time          `json:"scheduled_at"`
	StartedAt        *time.Time          `json:"started_at"`
	CompletedAt      *time.Time          `json:"completed_at"`
	RatePerMessage   float64             `json:"rate_per_message"`
	CreatedBy        *int64              `json:"created_by"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

const SelectCols = `id, name, template_slug, template_language, variables, audience, status, status_reason,
	scheduled_at, started_at, completed_at, rate_per_message::float8, created_by, created_at, updated_at`

func Scan(row pgx.Row) (*Campaign, error) {
	var c Campaign
	var vars, aud []byte
	err := row.Scan(&c.ID, &c.Name, &c.TemplateSlug, &c.TemplateLanguage, &vars, &aud, &c.Status, &c.StatusReason,
		&c.ScheduledAt, &c.StartedAt, &c.CompletedAt, &c.RatePerMessage, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(vars, &c.Variables)
	_ = json.Unmarshal(aud, &c.Audience)
	return &c, nil
}

func Get(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (*Campaign, error) {
	return Scan(q.QueryRow(ctx, `SELECT `+SelectCols+` FROM campaigns WHERE id=$1`, id))
}

// TemplateSpec loads the parsed content of a campaign's template.
func TemplateSpec(ctx context.Context, db *pgxpool.Pool, slug string) (templates.Spec, error) {
	var comps []byte
	if err := db.QueryRow(ctx, `SELECT components FROM templates WHERE slug=$1`, slug).Scan(&comps); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return templates.Spec{}, errors.New("template not found; sync templates from Bird")
		}
		return templates.Spec{}, err
	}
	return templates.Parse(comps)
}

// Launch freezes the recipient list of a due campaign and moves it to
// "sending". Opted-out contacts are excluded here and re-checked at send time.
// Returns false if the campaign was not due (e.g. another worker took it).
func Launch(ctx context.Context, db *pgxpool.Pool, id int64) (bool, int64, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	c, err := Scan(tx.QueryRow(ctx, `SELECT `+SelectCols+` FROM campaigns
		WHERE id=$1 AND status='scheduled' AND scheduled_at <= now() FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	args := append(c.Audience.args(), c.ID)
	tag, err := tx.Exec(ctx, `
		INSERT INTO campaign_recipients (campaign_id, contact_id, phone, contact_name, token)
		SELECT $4, c.id, c.phone, c.name, substr(replace(gen_random_uuid()::text, '-', ''), 1, 16)
		FROM contacts c WHERE NOT c.opted_out AND `+audienceWhere+`
		ON CONFLICT (campaign_id, contact_id) DO NOTHING`, args...)
	if err != nil {
		return false, 0, err
	}
	n := tag.RowsAffected()
	status := "sending"
	reason := ""
	if n == 0 {
		status, reason = "completed", "No eligible recipients"
	}
	_, err = tx.Exec(ctx, `UPDATE campaigns SET status=$2, status_reason=$3, started_at=now(),
		completed_at=CASE WHEN $2='completed' THEN now() END, updated_at=now() WHERE id=$1`, c.ID, status, reason)
	if err != nil {
		return false, 0, err
	}
	return true, n, tx.Commit(ctx)
}

type Stats struct {
	Recipients   int     `json:"recipients"`
	Queued       int     `json:"queued"`
	Accepted     int     `json:"accepted"`
	Sent         int     `json:"sent"`
	Delivered    int     `json:"delivered"`
	Read         int     `json:"read"`
	Failed       int     `json:"failed"`
	Skipped      int     `json:"skipped"`
	Cancelled    int     `json:"cancelled"`
	Unknown      int     `json:"unknown"`
	Clicked      int     `json:"clicked"`
	Clicks       int     `json:"clicks"`
	Replied      int     `json:"replied"`
	ActualCost   float64 `json:"actual_cost"`
	CostCurrency string  `json:"cost_currency"`
	PricedCount  int     `json:"priced_count"`
}

// StatsSQL aggregates campaign_recipients rows aliased "r".
const StatsSQL = `count(*),
	count(*) FILTER (WHERE r.status IN ('pending','sending')),
	count(*) FILTER (WHERE r.bird_message_id IS NOT NULL),
	count(*) FILTER (WHERE r.sent_at IS NOT NULL OR r.status IN ('sent','delivered','read')),
	count(*) FILTER (WHERE r.delivered_at IS NOT NULL OR r.status IN ('delivered','read')),
	count(*) FILTER (WHERE r.read_at IS NOT NULL OR r.status = 'read'),
	count(*) FILTER (WHERE r.status = 'failed'),
	count(*) FILTER (WHERE r.status = 'skipped'),
	count(*) FILTER (WHERE r.status = 'cancelled'),
	count(*) FILTER (WHERE r.status = 'unknown'),
	count(*) FILTER (WHERE r.clicked_at IS NOT NULL),
	coalesce(sum(r.click_count), 0),
	count(*) FILTER (WHERE r.replied_at IS NOT NULL),
	coalesce(sum(r.cost), 0)::float8,
	coalesce(max(r.cost_currency), ''),
	count(r.cost)`

func ScanStats(row pgx.Row) (Stats, error) {
	var s Stats
	err := row.Scan(&s.Recipients, &s.Queued, &s.Accepted, &s.Sent, &s.Delivered, &s.Read, &s.Failed,
		&s.Skipped, &s.Cancelled, &s.Unknown, &s.Clicked, &s.Clicks, &s.Replied, &s.ActualCost, &s.CostCurrency, &s.PricedCount)
	return s, err
}

func GetStats(ctx context.Context, db *pgxpool.Pool, campaignID int64) (Stats, error) {
	return ScanStats(db.QueryRow(ctx, `SELECT `+StatsSQL+` FROM campaign_recipients r WHERE r.campaign_id=$1`, campaignID))
}

// OptOut marks a phone number as opted out. Pending sends to it are skipped by
// the worker's send-time check. Unknown numbers are stored as opted-out
// contacts so a later import cannot re-subscribe them.
func OptOut(ctx context.Context, db *pgxpool.Pool, phone, source string) (bool, error) {
	tag, err := db.Exec(ctx, `INSERT INTO contacts (phone, opted_out, opted_out_at, opt_out_source)
		VALUES ($1, true, now(), $2)
		ON CONFLICT (phone) DO UPDATE SET opted_out=true, opted_out_at=now(), opt_out_source=$2, updated_at=now()
		WHERE NOT contacts.opted_out`, phone, source)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
