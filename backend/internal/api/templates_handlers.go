package api

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wacampaigns/internal/bird"
	"wacampaigns/internal/settings"
	"wacampaigns/internal/templates"
)

type templateRow struct {
	Slug               string          `json:"slug"`
	Name               string          `json:"name"`
	Category           string          `json:"category"`
	Status             string          `json:"status"`
	DefaultLanguage    string          `json:"default_language"`
	AvailableLanguages []string        `json:"available_languages"`
	Languages          json.RawMessage `json:"languages"`
	Spec               templates.Spec  `json:"spec"`
	Sendable           bool            `json:"sendable"`
	SyncedAt           time.Time       `json:"synced_at"`
}

const templateCols = `slug, name, category, status, default_language, available_languages, languages, components, synced_at`

func scanTemplate(row interface{ Scan(...any) error }) (templateRow, error) {
	var t templateRow
	var comps []byte
	err := row.Scan(&t.Slug, &t.Name, &t.Category, &t.Status, &t.DefaultLanguage, &t.AvailableLanguages, &t.Languages, &comps, &t.SyncedAt)
	if err != nil {
		return t, err
	}
	t.Spec, _ = templates.Parse(comps)
	t.Sendable = t.Status == "active" && len(t.AvailableLanguages) > 0
	return t, nil
}

func (s *Server) listTemplates(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT `+templateCols+` FROM templates ORDER BY (status='active') DESC, name`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	out := []templateRow{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			serverError(c, err)
			return
		}
		out = append(out, t)
	}
	c.JSON(200, out)
}

func (s *Server) getTemplate(c *gin.Context) {
	t, err := scanTemplate(s.db.QueryRow(c, `SELECT `+templateCols+` FROM templates WHERE slug=$1`, c.Param("slug")))
	if err != nil {
		if isNoRows(err) {
			fail(c, 404, "template not found")
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(200, t)
}

func (s *Server) syncTemplates(c *gin.Context) {
	n, err := templates.Sync(c, s.db, s.bird)
	if err != nil {
		fail(c, 502, "Could not sync templates from Bird: "+err.Error())
		return
	}
	c.JSON(200, gin.H{"synced": n})
}

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,511}$`)
var positional = regexp.MustCompile(`\{\{(\d+)\}\}`)

// createTemplate builds a marketing/utility template, creates it in Bird and
// submits it to Meta for approval.
func (s *Server) createTemplate(c *gin.Context) {
	var in struct {
		Slug           string   `json:"slug"`
		Name           string   `json:"name"`
		Category       string   `json:"category"`
		Language       string   `json:"language"`
		HeaderImageURL string   `json:"header_image_url"`
		Body           string   `json:"body"`
		BodyExamples   []string `json:"body_examples"`
		Footer         string   `json:"footer"`
		ShopButtonText string   `json:"shop_button_text"` // empty = no tracked button
		StopButton     bool     `json:"stop_button"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	st, _ := settings.Load(c, s.db)
	if st.BirdWABAID == "" {
		fail(c, 400, "Set the WhatsApp Business Account (WABA) ID in Settings first")
		return
	}
	in.Slug = strings.TrimSpace(in.Slug)
	if !slugRe.MatchString(in.Slug) || strings.HasPrefix(in.Slug, "bird_") {
		fail(c, 400, "Template ID: lowercase letters, numbers and underscores only, starting with a letter")
		return
	}
	if in.Category != "marketing" && in.Category != "utility" {
		fail(c, 400, "category must be marketing or utility")
		return
	}
	if in.Language == "" {
		in.Language = "en"
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len(body) > 1024 {
		fail(c, 400, "Message body is required (max 1024 characters)")
		return
	}
	nVars := 0
	for _, m := range positional.FindAllStringSubmatch(body, -1) {
		n := 0
		for _, r := range m[1] {
			n = n*10 + int(r-'0')
		}
		nVars = max(nVars, n)
	}
	if len(in.BodyExamples) < nVars {
		fail(c, 400, "Give an example value for every {{n}} variable in the body")
		return
	}

	var comps []map[string]any
	if in.HeaderImageURL != "" {
		if !strings.HasPrefix(in.HeaderImageURL, "https://") {
			fail(c, 400, "Header image must be a public https:// URL")
			return
		}
		comps = append(comps, map[string]any{"type": "header", "format": "image",
			"example_parameters": []map[string]any{{"type": "image", "url": in.HeaderImageURL}}})
	}
	bodyComp := map[string]any{"type": "body", "text": body}
	if nVars > 0 {
		ex := make([]map[string]any, nVars)
		for i := 0; i < nVars; i++ {
			ex[i] = map[string]any{"type": "text", "text": strings.TrimSpace(in.BodyExamples[i])}
		}
		bodyComp["example_parameters"] = ex
	}
	comps = append(comps, bodyComp)
	if f := strings.TrimSpace(in.Footer); f != "" {
		comps = append(comps, map[string]any{"type": "footer", "text": f})
	}
	var buttons []map[string]any
	if t := strings.TrimSpace(in.ShopButtonText); t != "" {
		buttons = append(buttons, map[string]any{"type": "url", "text": t, "url": s.cfg.PublicURL + "/r/{{1}}",
			"example_parameters": []map[string]any{{"type": "text", "text": "1/example"}}})
	}
	if in.StopButton {
		buttons = append(buttons, map[string]any{"type": "quick_reply", "text": "Stop promotions"})
	}
	if len(buttons) > 0 {
		comps = append(comps, map[string]any{"type": "buttons", "buttons": buttons})
	}

	tpl, err := s.bird.CreateTemplate(c, bird.CreateTemplateRequest{
		WABA: st.BirdWABAID, Slug: in.Slug, Name: strings.TrimSpace(in.Name), Category: in.Category,
		DefaultLanguage: in.Language,
		Languages:       map[string]map[string]any{in.Language: {"components": comps}},
	})
	if err != nil {
		fail(c, 502, "Bird rejected the template: "+err.Error())
		return
	}
	if tpl.DraftVersionID == nil || *tpl.DraftVersionID == "" {
		fail(c, 502, "Bird created the template but returned no draft to submit; submit it in Bird")
		return
	}
	res, err := s.bird.SubmitTemplateVersion(c, in.Slug, *tpl.DraftVersionID)
	if err != nil {
		fail(c, 502, "Template saved in Bird as a draft, but submitting to Meta failed: "+err.Error())
		return
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, e := range res.Errors {
			msgs[i] = e.Field + ": " + e.Message
		}
		fail(c, 422, "Template saved as a draft but has problems: "+strings.Join(msgs, "; "))
		return
	}
	_, _ = templates.Sync(c, s.db, s.bird)
	c.JSON(201, gin.H{"ok": true, "slug": in.Slug})
}
