package api

import (
	"github.com/gin-gonic/gin"

	"wacampaigns/internal/campaigns"
	"wacampaigns/internal/settings"
)

func (s *Server) dashboard(c *gin.Context) {
	type contactsSummary struct {
		Total    int `json:"total"`
		OptedOut int `json:"opted_out"`
		Groups   int `json:"groups"`
	}
	var cs contactsSummary
	if err := s.db.QueryRow(c, `SELECT count(*), count(*) FILTER (WHERE opted_out), (SELECT count(*) FROM groups) FROM contacts`).
		Scan(&cs.Total, &cs.OptedOut, &cs.Groups); err != nil {
		serverError(c, err)
		return
	}
	all, err := campaigns.ScanStats(s.db.QueryRow(c, `SELECT `+campaigns.StatsSQL+` FROM campaign_recipients r`))
	if err != nil {
		serverError(c, err)
		return
	}
	last30, err := campaigns.ScanStats(s.db.QueryRow(c, `SELECT `+campaigns.StatsSQL+` FROM campaign_recipients r
		WHERE r.created_at > now() - interval '30 days'`))
	if err != nil {
		serverError(c, err)
		return
	}
	statusCounts := map[string]int{}
	rows, err := s.db.Query(c, `SELECT status, count(*) FROM campaigns GROUP BY status`)
	if err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				statusCounts[st] = n
			}
		}
		rows.Close()
	}

	recent := []campaignWithStats{}
	rows, err = s.db.Query(c, `SELECT `+prefixCols("c")+`, st.* FROM campaigns c
		CROSS JOIN LATERAL (SELECT `+campaigns.StatsSQL+` FROM campaign_recipients r WHERE r.campaign_id=c.id) st
		WHERE c.status <> 'draft' ORDER BY coalesce(c.started_at, c.scheduled_at) DESC NULLS LAST LIMIT 6`)
	if err == nil {
		for rows.Next() {
			if cw, err := scanCampaignWithStats(rows); err == nil {
				recent = append(recent, cw)
			}
		}
		rows.Close()
	}
	var replies30 int
	_ = s.db.QueryRow(c, `SELECT count(*) FROM inbound_messages WHERE received_at > now() - interval '30 days'`).Scan(&replies30)

	st, _ := settings.Load(c, s.db)
	c.JSON(200, gin.H{
		"contacts":            cs,
		"all_time":            all,
		"last_30_days":        last30,
		"replies_30_days":     replies30,
		"campaigns_by_status": statusCounts,
		"recent_campaigns":    recent,
		"currency":            st.Currency,
		"bird_configured":     s.bird.Configured(),
	})
}

func (s *Server) settingsInfo() gin.H {
	return gin.H{
		"webhook_url":               s.cfg.PublicURL + "/webhooks/bird",
		"click_url_pattern":         s.cfg.PublicURL + "/r/{{1}}",
		"bird_api_host":             s.cfg.BirdAPIHost,
		"bird_configured":           s.bird.Configured(),
		"webhook_secret_configured": s.cfg.BirdWebhookSecret != "",
		"smtp_configured":           s.cfg.SMTPHost != "",
		"webhook_events": []string{"whatsapp.accepted", "whatsapp.sent", "whatsapp.delivered", "whatsapp.read",
			"whatsapp.failed", "whatsapp.rejected", "whatsapp.received", "preference.revoked", "preference.granted",
			"whatsapp_suppression.created"},
	}
}

func (s *Server) getSettings(c *gin.Context) {
	st, err := settings.Load(c, s.db)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"settings": st, "info": s.settingsInfo()})
}

func (s *Server) putSettings(c *gin.Context) {
	st := settings.Defaults()
	if err := c.ShouldBindJSON(&st); err != nil {
		fail(c, 400, "invalid settings")
		return
	}
	if err := st.Validate(); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if err := settings.Save(c, s.db, st); err != nil {
		serverError(c, err)
		return
	}
	saved, _ := settings.Load(c, s.db)
	c.JSON(200, gin.H{"settings": saved, "info": s.settingsInfo()})
}

func (s *Server) birdStatus(c *gin.Context) {
	if !s.bird.Configured() {
		c.JSON(200, gin.H{"ok": false, "message": "BIRD_API_KEY is not set on the server"})
		return
	}
	tpls, err := s.bird.ListTemplates(c)
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "message": "Connected to Bird", "templates": len(tpls)})
}
