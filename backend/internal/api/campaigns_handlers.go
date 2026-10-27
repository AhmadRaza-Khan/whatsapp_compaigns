package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wacampaigns/internal/bird"
	"wacampaigns/internal/campaigns"
	"wacampaigns/internal/phone"
	"wacampaigns/internal/settings"
	"wacampaigns/internal/templates"
)

type campaignWithStats struct {
	*campaigns.Campaign
	Stats campaigns.Stats `json:"stats"`
}

func (s *Server) listCampaigns(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT `+prefixCols("c")+`, st.* FROM campaigns c
		CROSS JOIN LATERAL (SELECT `+campaigns.StatsSQL+` FROM campaign_recipients r WHERE r.campaign_id=c.id) st
		ORDER BY coalesce(c.started_at, c.scheduled_at, c.created_at) DESC LIMIT 500`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	out := []campaignWithStats{}
	for rows.Next() {
		cw, err := scanCampaignWithStats(rows)
		if err != nil {
			serverError(c, err)
			return
		}
		out = append(out, cw)
	}
	c.JSON(200, out)
}

func prefixCols(alias string) string {
	cols := strings.Split(campaigns.SelectCols, ",")
	for i, col := range cols {
		cols[i] = alias + "." + strings.TrimSpace(col)
	}
	return strings.Join(cols, ", ")
}

// scanCampaignWithStats reads a campaign row followed by StatsSQL columns.
func scanCampaignWithStats(row interface{ Scan(...any) error }) (campaignWithStats, error) {
	var cmp campaigns.Campaign
	var st campaigns.Stats
	var vars, aud []byte
	err := row.Scan(&cmp.ID, &cmp.Name, &cmp.TemplateSlug, &cmp.TemplateLanguage, &vars, &aud, &cmp.Status, &cmp.StatusReason,
		&cmp.ScheduledAt, &cmp.StartedAt, &cmp.CompletedAt, &cmp.RatePerMessage, &cmp.CreatedBy, &cmp.CreatedAt, &cmp.UpdatedAt,
		&st.Recipients, &st.Queued, &st.Accepted, &st.Sent, &st.Delivered, &st.Read, &st.Failed,
		&st.Skipped, &st.Cancelled, &st.Unknown, &st.Clicked, &st.Clicks, &st.Replied, &st.ActualCost, &st.CostCurrency, &st.PricedCount)
	if err != nil {
		return campaignWithStats{}, err
	}
	_ = json.Unmarshal(vars, &cmp.Variables)
	_ = json.Unmarshal(aud, &cmp.Audience)
	return campaignWithStats{Campaign: &cmp, Stats: st}, nil
}

type campaignInput struct {
	Name             string              `json:"name"`
	TemplateSlug     string              `json:"template_slug"`
	TemplateLanguage string              `json:"template_language"`
	Variables        templates.Variables `json:"variables"`
	Audience         campaigns.Audience  `json:"audience"`
}

func (s *Server) bindCampaign(c *gin.Context) (*campaignInput, bool) {
	var in campaignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return nil, false
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.TemplateSlug == "" {
		fail(c, 400, "Campaign name and template are required")
		return nil, false
	}
	var defLang string
	var langs []string
	if err := s.db.QueryRow(c, `SELECT default_language, available_languages FROM templates WHERE slug=$1`, in.TemplateSlug).Scan(&defLang, &langs); err != nil {
		fail(c, 400, "Unknown template. Sync templates from Bird.")
		return nil, false
	}
	if in.TemplateLanguage == "" {
		in.TemplateLanguage = defLang
	}
	in.Variables.ShopURL = strings.TrimSpace(in.Variables.ShopURL)
	in.Variables.HeaderMediaURL = strings.TrimSpace(in.Variables.HeaderMediaURL)
	cleanAudience(&in.Audience)
	return &in, true
}

func cleanAudience(a *campaigns.Audience) {
	if a.GroupIDs == nil {
		a.GroupIDs = []int64{}
	}
	a.Tags = cleanTags(a.Tags)
}

func (s *Server) createCampaign(c *gin.Context) {
	in, ok := s.bindCampaign(c)
	if !ok {
		return
	}
	vars, _ := json.Marshal(in.Variables)
	aud, _ := json.Marshal(in.Audience)
	var id int64
	err := s.db.QueryRow(c, `INSERT INTO campaigns (name, template_slug, template_language, variables, audience, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, in.Name, in.TemplateSlug, in.TemplateLanguage, vars, aud, user(c).ID).Scan(&id)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) updateCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	in, ok := s.bindCampaign(c)
	if !ok {
		return
	}
	vars, _ := json.Marshal(in.Variables)
	aud, _ := json.Marshal(in.Audience)
	tag, err := s.db.Exec(c, `UPDATE campaigns SET name=$2, template_slug=$3, template_language=$4, variables=$5, audience=$6, updated_at=now()
		WHERE id=$1 AND status='draft'`, id, in.Name, in.TemplateSlug, in.TemplateLanguage, vars, aud)
	if err != nil {
		serverError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "Only draft campaigns can be edited. Unschedule it first.")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM campaigns WHERE id=$1 AND (status='draft' OR (status='cancelled' AND started_at IS NULL))`, id)
	if err != nil {
		serverError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "Only drafts can be deleted; sent campaigns are kept for reporting.")
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) duplicateCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var newID int64
	err := s.db.QueryRow(c, `INSERT INTO campaigns (name, template_slug, template_language, variables, audience, created_by)
		SELECT 'Copy of ' || name, template_slug, template_language, variables, audience, $2 FROM campaigns WHERE id=$1 RETURNING id`,
		id, user(c).ID).Scan(&newID)
	if err != nil {
		if isNoRows(err) {
			fail(c, 404, "campaign not found")
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(201, gin.H{"id": newID})
}

type estimate struct {
	Eligible      int     `json:"eligible"`
	OptedOut      int     `json:"opted_out_excluded"`
	Rate          float64 `json:"rate"`
	Currency      string  `json:"currency"`
	EstimatedCost float64 `json:"estimated_cost"`
	SendMinutes   float64 `json:"send_minutes"`
}

func (s *Server) estimateFor(c *gin.Context, a campaigns.Audience) (estimate, error) {
	st, _ := settings.Load(c, s.db)
	el, oo, err := campaigns.Count(c, s.db, a)
	if err != nil {
		return estimate{}, err
	}
	return estimate{Eligible: el, OptedOut: oo, Rate: st.MarketingRate, Currency: st.Currency,
		EstimatedCost: float64(el) * st.MarketingRate, SendMinutes: float64(el) / float64(max(st.SendRatePerMinute, 1))}, nil
}

func (s *Server) estimateAudience(c *gin.Context) {
	var in struct {
		Audience campaigns.Audience `json:"audience"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	cleanAudience(&in.Audience)
	e, err := s.estimateFor(c, in.Audience)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, e)
}

func (s *Server) getCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	cmp, err := campaigns.Get(c, s.db, id)
	if err != nil {
		if isNoRows(err) {
			fail(c, 404, "campaign not found")
			return
		}
		serverError(c, err)
		return
	}
	stats, err := campaigns.GetStats(c, s.db, id)
	if err != nil {
		serverError(c, err)
		return
	}
	resp := gin.H{"campaign": cmp, "stats": stats}
	if t, err := scanTemplate(s.db.QueryRow(c, `SELECT `+templateCols+` FROM templates WHERE slug=$1`, cmp.TemplateSlug)); err == nil {
		resp["template"] = t
	}
	st, _ := settings.Load(c, s.db)
	resp["currency"] = st.Currency
	if cmp.Status == "draft" || cmp.Status == "scheduled" {
		if e, err := s.estimateFor(c, cmp.Audience); err == nil {
			resp["estimate"] = e
		}
	} else {
		resp["estimated_cost"] = float64(stats.Recipients-stats.Skipped-stats.Cancelled) * cmp.RatePerMessage
	}
	// Error reasons summary for the report.
	rows, err := s.db.Query(c, `SELECT coalesce(nullif(error_reason,''), error_code), count(*) FROM campaign_recipients
		WHERE campaign_id=$1 AND status IN ('failed','skipped','unknown') GROUP BY 1 ORDER BY 2 DESC LIMIT 20`, id)
	if err == nil {
		type reason struct {
			Reason string `json:"reason"`
			Count  int    `json:"count"`
		}
		reasons := []reason{}
		for rows.Next() {
			var r reason
			if rows.Scan(&r.Reason, &r.Count) == nil {
				reasons = append(reasons, r)
			}
		}
		rows.Close()
		resp["failure_reasons"] = reasons
	}
	c.JSON(200, resp)
}

// validateForSend checks everything that must be right before messages go out.
func (s *Server) validateForSend(c *gin.Context, cmp *campaigns.Campaign) (templates.Spec, bool) {
	t, err := scanTemplate(s.db.QueryRow(c, `SELECT `+templateCols+` FROM templates WHERE slug=$1`, cmp.TemplateSlug))
	if err != nil {
		fail(c, 400, "Template not found. Sync templates from Bird.")
		return templates.Spec{}, false
	}
	if t.Status != "active" || !slices.Contains(t.AvailableLanguages, cmp.TemplateLanguage) {
		fail(c, 400, fmt.Sprintf("Template %q is not approved for language %q yet (status: %s)", t.Name, cmp.TemplateLanguage, t.Status))
		return templates.Spec{}, false
	}
	if err := t.Spec.Validate(cmp.Variables); err != nil {
		fail(c, 400, err.Error())
		return templates.Spec{}, false
	}
	if !s.bird.Configured() {
		fail(c, 400, "Bird API key is not configured on the server")
		return templates.Spec{}, false
	}
	return t.Spec, true
}

func (s *Server) testSend(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Phone string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "phone is required")
		return
	}
	cmp, err := campaigns.Get(c, s.db, id)
	if err != nil {
		fail(c, 404, "campaign not found")
		return
	}
	spec, ok := s.validateForSend(c, cmp)
	if !ok {
		return
	}
	st, _ := settings.Load(c, s.db)
	p, err := phone.Normalize(in.Phone, st.DefaultRegion)
	if err != nil {
		fail(c, 400, "Phone: "+err.Error())
		return
	}
	req := bird.SendRequest{
		To: p,
		Template: bird.TemplateRef{Slug: cmp.TemplateSlug, Language: cmp.TemplateLanguage,
			Components: spec.Build(cmp.Variables, user(c).Name, st.NameFallback, fmt.Sprintf("%d/test", cmp.ID))},
		Tags: []bird.Tag{{Name: "campaign_id", Value: strconv.FormatInt(cmp.ID, 10)}, {Name: "test", Value: "true"}},
	}
	if st.BirdFrom != "" && !strings.HasPrefix(cmp.TemplateSlug, "bird_") {
		req.From = st.BirdFrom
	}
	msg, err := s.bird.SendTemplate(c, req, "")
	if err != nil {
		fail(c, 502, "Bird did not accept the test: "+err.Error())
		return
	}
	c.JSON(200, gin.H{"ok": true, "message_id": msg.ID, "to": p})
}

func (s *Server) sendCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		ScheduledAt string `json:"scheduled_at"` // "2026-11-01T10:00" in Asia/Qatar; empty = now
	}
	_ = c.ShouldBindJSON(&in)
	cmp, err := campaigns.Get(c, s.db, id)
	if err != nil {
		fail(c, 404, "campaign not found")
		return
	}
	if cmp.Status != "draft" {
		fail(c, 409, "This campaign has already been scheduled or sent")
		return
	}
	if _, ok := s.validateForSend(c, cmp); !ok {
		return
	}
	if cmp.Audience.Empty() {
		fail(c, 400, "Choose who receives the campaign")
		return
	}
	el, _, err := campaigns.Count(c, s.db, cmp.Audience)
	if err != nil {
		serverError(c, err)
		return
	}
	if el == 0 {
		fail(c, 400, "No subscribed contacts match the selected audience")
		return
	}
	when := time.Now()
	if in.ScheduledAt != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", in.ScheduledAt, Qatar)
		if err != nil {
			fail(c, 400, "Invalid schedule time")
			return
		}
		if t.Before(time.Now().Add(-time.Minute)) {
			fail(c, 400, "The scheduled time is in the past")
			return
		}
		when = t
	}
	st, _ := settings.Load(c, s.db)
	tag, err := s.db.Exec(c, `UPDATE campaigns SET status='scheduled', scheduled_at=$2, rate_per_message=$3, status_reason='', updated_at=now()
		WHERE id=$1 AND status='draft'`, id, when, st.MarketingRate)
	if err != nil {
		serverError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "This campaign has already been scheduled or sent")
		return
	}
	c.JSON(200, gin.H{"ok": true, "scheduled_at": when})
}

func (s *Server) transition(c *gin.Context, sql, conflictMsg string) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	tag, err := s.db.Exec(c, sql, id)
	if err != nil {
		serverError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, conflictMsg)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) pauseCampaign(c *gin.Context) {
	s.transition(c, `UPDATE campaigns SET status='paused', status_reason='Paused by user', updated_at=now() WHERE id=$1 AND status='sending'`,
		"Only a sending campaign can be paused")
}

func (s *Server) resumeCampaign(c *gin.Context) {
	s.transition(c, `UPDATE campaigns SET status='sending', status_reason='', updated_at=now() WHERE id=$1 AND status='paused'`,
		"Only a paused campaign can be resumed")
}

func (s *Server) unscheduleCampaign(c *gin.Context) {
	s.transition(c, `UPDATE campaigns SET status='draft', scheduled_at=NULL, updated_at=now() WHERE id=$1 AND status='scheduled'`,
		"Only a scheduled campaign that has not started can be unscheduled")
}

func (s *Server) cancelCampaign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	tx, err := s.db.Begin(c)
	if err != nil {
		serverError(c, err)
		return
	}
	defer tx.Rollback(c)
	tag, err := tx.Exec(c, `UPDATE campaigns SET status='cancelled', status_reason='Cancelled by user', completed_at=now(), updated_at=now()
		WHERE id=$1 AND status IN ('scheduled','sending','paused')`, id)
	if err != nil {
		serverError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(c, 409, "This campaign cannot be cancelled")
		return
	}
	// Messages already handed to Bird cannot be recalled; the rest are dropped.
	if _, err := tx.Exec(c, `UPDATE campaign_recipients SET status='cancelled', updated_at=now() WHERE campaign_id=$1 AND status='pending'`, id); err != nil {
		serverError(c, err)
		return
	}
	if err := tx.Commit(c); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

type recipient struct {
	ID          int64      `json:"id"`
	ContactID   *int64     `json:"contact_id"`
	Phone       string     `json:"phone"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	ErrorCode   string     `json:"error_code"`
	ErrorReason string     `json:"error_reason"`
	SentAt      *time.Time `json:"sent_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	ReadAt      *time.Time `json:"read_at"`
	FailedAt    *time.Time `json:"failed_at"`
	ClickedAt   *time.Time `json:"clicked_at"`
	ClickCount  int        `json:"click_count"`
	RepliedAt   *time.Time `json:"replied_at"`
	Cost        *float64   `json:"cost"`
	MessageID   *string    `json:"message_id"`
}

func recipientFilter(c *gin.Context, id int64) (string, []any) {
	where := "r.campaign_id=$1"
	args := []any{id}
	switch c.Query("status") {
	case "queued":
		where += " AND r.status IN ('pending','sending')"
	case "sent":
		where += " AND (r.sent_at IS NOT NULL OR r.status IN ('sent','delivered','read'))"
	case "delivered":
		where += " AND (r.delivered_at IS NOT NULL OR r.status IN ('delivered','read'))"
	case "read":
		where += " AND (r.read_at IS NOT NULL OR r.status='read')"
	case "clicked":
		where += " AND r.clicked_at IS NOT NULL"
	case "replied":
		where += " AND r.replied_at IS NOT NULL"
	case "failed", "skipped", "cancelled", "unknown", "accepted":
		args = append(args, c.Query("status"))
		where += " AND r.status=$2"
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		args = append(args, "%"+q+"%")
		where += fmt.Sprintf(" AND (r.phone ILIKE $%d OR r.contact_name ILIKE $%d)", len(args), len(args))
	}
	return where, args
}

const recipientCols = `r.id, r.contact_id, r.phone, r.contact_name, r.status, r.attempts, r.error_code, r.error_reason,
	r.sent_at, r.delivered_at, r.read_at, r.failed_at, r.clicked_at, r.click_count, r.replied_at, r.cost::float8, r.bird_message_id`

func scanRecipient(row interface{ Scan(...any) error }) (recipient, error) {
	var r recipient
	err := row.Scan(&r.ID, &r.ContactID, &r.Phone, &r.Name, &r.Status, &r.Attempts, &r.ErrorCode, &r.ErrorReason,
		&r.SentAt, &r.DeliveredAt, &r.ReadAt, &r.FailedAt, &r.ClickedAt, &r.ClickCount, &r.RepliedAt, &r.Cost, &r.MessageID)
	return r, err
}

func (s *Server) listRecipients(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	where, args := recipientFilter(c, id)
	page, size, offset := pageParams(c)
	var total int
	if err := s.db.QueryRow(c, `SELECT count(*) FROM campaign_recipients r WHERE `+where, args...).Scan(&total); err != nil {
		serverError(c, err)
		return
	}
	rows, err := s.db.Query(c, fmt.Sprintf(`SELECT %s FROM campaign_recipients r WHERE %s ORDER BY r.id LIMIT %d OFFSET %d`,
		recipientCols, where, size, offset), args...)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	items := []recipient{}
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			serverError(c, err)
			return
		}
		items = append(items, r)
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) listReplies(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	rows, err := s.db.Query(c, `SELECT m.id, m.phone, coalesce(ct.name, ''), m.text, m.is_opt_out, m.received_at
		FROM inbound_messages m LEFT JOIN contacts ct ON ct.id=m.contact_id
		WHERE m.campaign_id=$1 ORDER BY m.received_at DESC LIMIT 500`, id)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	type reply struct {
		ID         int64     `json:"id"`
		Phone      string    `json:"phone"`
		Name       string    `json:"name"`
		Text       string    `json:"text"`
		IsOptOut   bool      `json:"is_opt_out"`
		ReceivedAt time.Time `json:"received_at"`
	}
	out := []reply{}
	for rows.Next() {
		var r reply
		if rows.Scan(&r.ID, &r.Phone, &r.Name, &r.Text, &r.IsOptOut, &r.ReceivedAt) == nil {
			out = append(out, r)
		}
	}
	c.JSON(200, out)
}

func (s *Server) exportRecipients(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	where, args := recipientFilter(c, id)
	rows, err := s.db.Query(c, `SELECT `+recipientCols+` FROM campaign_recipients r WHERE `+where+` ORDER BY r.id`, args...)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="campaign-%d-report.csv"`, id))
	c.Writer.Write([]byte("\xEF\xBB\xBF"))
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"phone", "name", "status", "sent_at", "delivered_at", "read_at", "clicked_at", "clicks",
		"replied_at", "failed_at", "error_code", "error_reason", "cost", "bird_message_id"})
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			break
		}
		cost, mid := "", ""
		if r.Cost != nil {
			cost = strconv.FormatFloat(*r.Cost, 'f', 5, 64)
		}
		if r.MessageID != nil {
			mid = *r.MessageID
		}
		_ = w.Write([]string{r.Phone, csvSafe(r.Name), r.Status, fmtTime(r.SentAt), fmtTime(r.DeliveredAt), fmtTime(r.ReadAt),
			fmtTime(r.ClickedAt), strconv.Itoa(r.ClickCount), fmtTime(r.RepliedAt), fmtTime(r.FailedAt),
			r.ErrorCode, csvSafe(r.ErrorReason), cost, mid})
	}
	w.Flush()
}
