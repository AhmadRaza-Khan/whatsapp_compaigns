package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wacampaigns/internal/bird"
	"wacampaigns/internal/campaigns"
)

type envelope struct {
	Type       string          `json:"type"`
	Timestamp  *time.Time      `json:"timestamp"`
	OccurredAt *time.Time      `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type address struct {
	PhoneNumber string `json:"phone_number"`
}

type messageData struct {
	ID                 string         `json:"id"`
	WhatsAppID         string         `json:"whatsapp_id"`
	Direction          string         `json:"direction"`
	From               address        `json:"from"`
	To                 address        `json:"to"`
	Tags               []bird.Tag     `json:"tags"`
	Metadata           map[string]any `json:"metadata"`
	InReplyToMessageID string         `json:"in_reply_to_message_id"`
	Error              *birdError     `json:"error"`
	LastError          *birdError     `json:"last_error"`
	Text               *struct {
		Body string `json:"body"`
	} `json:"text"`
	Button *struct {
		Text string `json:"text"`
	} `json:"button"`
	InteractiveReply *struct {
		Type   string `json:"type"`
		Button *struct {
			Text string `json:"text"`
		} `json:"button"`
		List *struct {
			Text string `json:"text"`
		} `json:"list"`
	} `json:"interactive_reply"`
}

type birdError struct {
	Code          string          `json:"code"`
	Description   string          `json:"description"`
	MetaErrorCode json.RawMessage `json:"meta_error_code"`
}

func (m messageData) messageID() string {
	if m.WhatsAppID != "" {
		return m.WhatsAppID
	}
	return m.ID
}

func (m messageData) recipientID() int64 {
	for _, t := range m.Tags {
		if t.Name == "recipient_id" {
			id, _ := strconv.ParseInt(t.Value, 10, 64)
			return id
		}
	}
	if v, ok := m.Metadata["recipient_id"].(float64); ok {
		return int64(v)
	}
	return 0
}

func (m messageData) text() string {
	switch {
	case m.Text != nil && m.Text.Body != "":
		return m.Text.Body
	case m.Button != nil:
		return m.Button.Text
	case m.InteractiveReply != nil && m.InteractiveReply.Button != nil:
		return m.InteractiveReply.Button.Text
	case m.InteractiveReply != nil && m.InteractiveReply.List != nil:
		return m.InteractiveReply.List.Text
	}
	return ""
}

func (w *Worker) processWebhooks(ctx context.Context) {
	rows, err := w.db.Query(ctx, `SELECT id, payload FROM webhook_events
		WHERE processed_at IS NULL AND attempts < 5 ORDER BY id LIMIT 500`)
	if err != nil {
		slog.Error("load webhook events failed", "err", err)
		return
	}
	type ev struct {
		id      int64
		payload []byte
	}
	var events []ev
	for rows.Next() {
		var e ev
		if rows.Scan(&e.id, &e.payload) == nil {
			events = append(events, e)
		}
	}
	rows.Close()

	for _, e := range events {
		if err := w.handleEvent(ctx, e.payload); err != nil {
			slog.Warn("webhook event failed", "id", e.id, "err", err)
			_, _ = w.db.Exec(ctx, `UPDATE webhook_events SET attempts=attempts+1, error=$2 WHERE id=$1`, e.id, err.Error())
			continue
		}
		_, _ = w.db.Exec(ctx, `UPDATE webhook_events SET processed_at=now(), attempts=attempts+1, error='' WHERE id=$1`, e.id)
	}
}

var statusRank = map[string]int{
	"pending": 0, "sending": 1, "unknown": 1, "accepted": 2, "sent": 3, "failed": 3, "delivered": 4, "read": 5,
}

func (w *Worker) handleEvent(ctx context.Context, payload []byte) error {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil // unparseable: nothing to retry
	}
	ts := time.Now()
	if env.Timestamp != nil {
		ts = *env.Timestamp
	} else if env.OccurredAt != nil {
		ts = *env.OccurredAt
	}

	switch env.Type {
	case "whatsapp.accepted", "whatsapp.sent", "whatsapp.delivered", "whatsapp.read", "whatsapp.failed", "whatsapp.rejected":
		var d messageData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil
		}
		return w.applyStatus(ctx, strings.TrimPrefix(env.Type, "whatsapp."), d, ts)
	case "whatsapp.received":
		var d messageData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return nil
		}
		return w.handleInbound(ctx, d, ts)
	case "preference.revoked", "preference.granted":
		var p struct {
			Channel string `json:"channel"`
			Handle  string `json:"handle"`
		}
		if json.Unmarshal(env.Data, &p) != nil || p.Channel != "whatsapp" || p.Handle == "" {
			return nil
		}
		if env.Type == "preference.revoked" {
			_, err := campaigns.OptOut(ctx, w.db, p.Handle, "bird")
			return err
		}
		_, err := w.db.Exec(ctx, `UPDATE contacts SET opted_out=false, opted_out_at=NULL, opt_out_source='', updated_at=now()
			WHERE phone=$1 AND opted_out`, p.Handle)
		return err
	case "whatsapp_suppression.created":
		if phone := findPhone(env.Data); phone != "" {
			_, err := campaigns.OptOut(ctx, w.db, phone, "bird")
			return err
		}
	}
	return nil // unknown event types are ignored (Bird may add new ones)
}

// findPhone pulls a phone number from a payload whose exact shape varies.
func findPhone(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"handle", "phone_number", "recipient", "to"} {
		switch v := m[k].(type) {
		case string:
			if strings.HasPrefix(v, "+") {
				return v
			}
		case map[string]any:
			if p, ok := v["phone_number"].(string); ok {
				return p
			}
		}
	}
	return ""
}

func (w *Worker) applyStatus(ctx context.Context, status string, d messageData, ts time.Time) error {
	mid := d.messageID()
	var id int64
	var current string
	var contactID *int64
	err := w.db.QueryRow(ctx, `SELECT id, status, contact_id FROM campaign_recipients
		WHERE id=$1 OR (bird_message_id IS NOT NULL AND bird_message_id=$2) ORDER BY (id=$1) DESC LIMIT 1`,
		d.recipientID(), mid).Scan(&id, &current, &contactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // a test send or a message sent outside the platform
	}
	if err != nil {
		return err
	}

	if status == "failed" || status == "rejected" {
		e := d.Error
		if e == nil {
			e = d.LastError
		}
		code, reason := status, "Message "+status+" by WhatsApp/Bird"
		if e != nil {
			code, reason = e.Code, e.Description
			if mc := strings.Trim(string(e.MetaErrorCode), `"`); mc != "" && mc != "null" {
				reason += " (Meta " + mc + ")"
			}
		}
		if status == "rejected" && isSuppression(code, reason) && contactID != nil {
			var phone string
			if w.db.QueryRow(ctx, `SELECT phone FROM contacts WHERE id=$1`, *contactID).Scan(&phone) == nil {
				_, _ = campaigns.OptOut(ctx, w.db, phone, "bird")
			}
		}
		// A late failure never overrides a confirmed delivery.
		_, err := w.db.Exec(ctx, `UPDATE campaign_recipients SET
			bird_message_id = coalesce(bird_message_id, nullif($2, '')),
			status = CASE WHEN status IN ('delivered','read') THEN status ELSE 'failed' END,
			failed_at = CASE WHEN status IN ('delivered','read') THEN failed_at ELSE coalesce(failed_at, $3) END,
			error_code=$4, error_reason=$5, locked_at=NULL, updated_at=now()
			WHERE id=$1`, id, mid, ts, code, reason)
		return err
	}

	newStatus := current
	if statusRank[status] > statusRank[current] {
		newStatus = status
	}
	// Later stages imply earlier ones (events can arrive out of order).
	_, err = w.db.Exec(ctx, `UPDATE campaign_recipients SET
		bird_message_id = coalesce(bird_message_id, nullif($2, '')),
		status = $3, locked_at = CASE WHEN $3 <> 'sending' THEN NULL ELSE locked_at END,
		accepted_at  = coalesce(accepted_at, $4),
		sent_at      = CASE WHEN $5 IN ('sent','delivered','read') THEN coalesce(sent_at, $4) ELSE sent_at END,
		delivered_at = CASE WHEN $5 IN ('delivered','read') THEN coalesce(delivered_at, $4) ELSE delivered_at END,
		read_at      = CASE WHEN $5 = 'read' THEN coalesce(read_at, $4) ELSE read_at END,
		updated_at = now()
		WHERE id=$1`, id, mid, newStatus, ts, status)
	return err
}

func isSuppression(code, reason string) bool {
	s := strings.ToLower(code + " " + reason)
	for _, k := range []string{"suppress", "opt_out", "opted out", "opt-out", "preference", "unsubscribed"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func (w *Worker) handleInbound(ctx context.Context, d messageData, ts time.Time) error {
	phone := d.From.PhoneNumber
	text := d.text()
	isStop := text != "" && w.Settings(ctx).IsStopMessage(text)

	var contactID *int64
	var cid int64
	if phone != "" && w.db.QueryRow(ctx, `SELECT id FROM contacts WHERE phone=$1`, phone).Scan(&cid) == nil {
		contactID = &cid
	}

	// Attribute the reply to the campaign message it answers, or else to the
	// most recent campaign message this contact received in the last 7 days.
	var recipientID, campaignID *int64
	var rid, cmp int64
	err := w.db.QueryRow(ctx, `SELECT id, campaign_id FROM campaign_recipients
		WHERE (bird_message_id = nullif($1,'')) OR (contact_id = $2 AND bird_message_id IS NOT NULL AND created_at > now() - interval '7 days')
		ORDER BY (bird_message_id = nullif($1,'')) DESC NULLS LAST, created_at DESC LIMIT 1`,
		d.InReplyToMessageID, contactID).Scan(&rid, &cmp)
	if err == nil {
		recipientID, campaignID = &rid, &cmp
	}

	var mid *string
	if m := d.messageID(); m != "" {
		mid = &m
	}
	tag, err := w.db.Exec(ctx, `INSERT INTO inbound_messages (bird_message_id, contact_id, campaign_id, phone, text, is_opt_out, received_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (bird_message_id) DO NOTHING`,
		mid, contactID, campaignID, phone, text, isStop, ts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already processed
	}
	if recipientID != nil {
		_, _ = w.db.Exec(ctx, `UPDATE campaign_recipients SET replied_at=coalesce(replied_at, $2), updated_at=now() WHERE id=$1`, *recipientID, ts)
	}
	if isStop && phone != "" {
		if _, err := campaigns.OptOut(ctx, w.db, phone, "keyword"); err != nil {
			return err
		}
		slog.Info("contact opted out by keyword", "phone", phone)
		// Keep Bird's preference store in sync so Bird blocks sends too.
		if w.bird.Configured() {
			if err := w.bird.RecordPreference(ctx, phone, "revoked", "STOP reply via campaign platform"); err != nil {
				slog.Warn("bird preference sync failed", "phone", phone, "err", err)
			}
		}
	}
	return nil
}
