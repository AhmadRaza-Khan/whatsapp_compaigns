package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wacampaigns/internal/bird"
)

// birdWebhook verifies the signature, stores the raw event and returns
// immediately; the worker processes stored events in the background.
func (s *Server) birdWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.Status(400)
		return
	}
	id := c.GetHeader("webhook-id")
	if s.cfg.BirdWebhookSecret != "" {
		if err := bird.VerifyWebhook(s.cfg.BirdWebhookSecret, id, c.GetHeader("webhook-timestamp"),
			c.GetHeader("webhook-signature"), body, time.Now()); err != nil {
			slog.Warn("webhook rejected", "err", err, "ip", c.ClientIP())
			c.Status(401)
			return
		}
	} else if !s.cfg.IsDev() {
		slog.Error("webhook received but BIRD_WEBHOOK_SECRET is not set; rejecting")
		c.Status(503)
		return
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		c.Status(400)
		return
	}
	if id == "" {
		id = "local-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	// webhook-id is unique per event; retries are deduplicated here.
	_, err = s.db.Exec(c, `INSERT INTO webhook_events (webhook_id, event_type, payload) VALUES ($1,$2,$3)
		ON CONFLICT (webhook_id) DO NOTHING`, id, env.Type, body)
	if err != nil {
		slog.Error("store webhook failed", "err", err)
		c.Status(500) // Bird will retry
		return
	}
	c.Status(200)
}

// clickRedirect records a Shop Now click and forwards to the campaign's shop
// link. Only links stored on campaigns are used, so this is not an open redirect.
func (s *Server) clickRedirect(c *gin.Context) {
	campaignID, _ := strconv.ParseInt(c.Param("campaign"), 10, 64)
	token := c.Param("token")
	var shopURL string
	if err := s.db.QueryRow(c, `SELECT variables->>'shop_url' FROM campaigns WHERE id=$1`, campaignID).Scan(&shopURL); err != nil || shopURL == "" {
		c.String(404, "This link is no longer available.")
		return
	}
	ua := c.GetHeader("User-Agent")
	// Link-preview crawlers (WhatsApp, Facebook) are not customer clicks.
	isBot := strings.HasPrefix(ua, "WhatsApp/") || strings.Contains(ua, "facebookexternalhit") || strings.Contains(strings.ToLower(ua), "bot")
	if token != "test" && !isBot {
		var rid int64
		err := s.db.QueryRow(c, `UPDATE campaign_recipients SET clicked_at=coalesce(clicked_at, now()), click_count=click_count+1, updated_at=now()
			WHERE campaign_id=$1 AND token=$2 RETURNING id`, campaignID, token).Scan(&rid)
		if err == nil {
			_, _ = s.db.Exec(c, `INSERT INTO clicks (recipient_id, campaign_id, ip, user_agent) VALUES ($1,$2,$3,$4)`,
				rid, campaignID, c.ClientIP(), truncate(ua, 300))
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, shopURL)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
