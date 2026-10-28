package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings are the admin-editable, non-secret options. Secrets stay in env.
type Settings struct {
	MarketingRate     float64  `json:"marketing_rate"`       // cost per marketing message
	Currency          string   `json:"currency"`             // display currency for the rate
	SendRatePerMinute int      `json:"send_rate_per_minute"` // Bird plan limit: Startup 30, Growth 100
	DefaultRegion     string   `json:"default_region"`       // ISO country for numbers without +, e.g. QA
	BirdFrom          string   `json:"bird_from"`            // connected sender number, E.164 (Bird rejects anything else)
	BirdWABAID        string   `json:"bird_waba_id"`         // needed to create templates from the platform
	BirdDashboardURL  string   `json:"bird_dashboard_url"`   // link to Bird's template screen
	StopKeywords      []string `json:"stop_keywords"`
	NameFallback      string   `json:"name_fallback"` // used for {name} when a contact has no name
}

func Defaults() Settings {
	return Settings{
		MarketingRate:     0.0391,
		Currency:          "USD",
		SendRatePerMinute: 30,
		DefaultRegion:     "QA",
		BirdDashboardURL:  "https://app.bird.com",
		StopKeywords:      []string{"STOP", "STOP PROMOTIONS", "STOP ALL", "UNSUBSCRIBE", "CANCEL", "END", "QUIT", "OPT OUT", "OPTOUT", "إيقاف", "ايقاف", "توقف", "الغاء", "إلغاء"},
		NameFallback:      "Customer",
	}
}

const key = "app"

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// CleanFrom strips spaces, dashes and brackets so "+974 5512-3456" is accepted.
func CleanFrom(v string) string {
	return strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(v))
}

func Load(ctx context.Context, db *pgxpool.Pool) (Settings, error) {
	s := Defaults()
	var raw []byte
	err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return s, nil
		}
		return s, err
	}
	// Unmarshal over defaults so newly added fields keep their defaults.
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), err
	}
	return s, nil
}

func (s Settings) Validate() error {
	if s.MarketingRate < 0 || s.MarketingRate > 10 {
		return fmt.Errorf("marketing rate must be between 0 and 10")
	}
	if s.SendRatePerMinute < 1 || s.SendRatePerMinute > 1000 {
		return fmt.Errorf("send rate must be between 1 and 1000 per minute")
	}
	if s.BirdFrom != "" && !e164.MatchString(s.BirdFrom) {
		return fmt.Errorf("sender number must include the country code, e.g. +97455123456")
	}
	if len(s.DefaultRegion) != 2 {
		return fmt.Errorf("default region must be a 2-letter country code, e.g. QA")
	}
	return nil
}

func Save(ctx context.Context, db *pgxpool.Pool, s Settings) error {
	s.DefaultRegion = strings.ToUpper(s.DefaultRegion)
	s.BirdFrom = CleanFrom(s.BirdFrom)
	s.BirdWABAID = strings.TrimSpace(s.BirdWABAID)
	kw := s.StopKeywords[:0]
	for _, k := range s.StopKeywords {
		if k = strings.TrimSpace(k); k != "" {
			kw = append(kw, k)
		}
	}
	s.StopKeywords = kw
	raw, _ := json.Marshal(s)
	_, err := db.Exec(ctx, `INSERT INTO settings(key, value) VALUES($1,$2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, raw)
	return err
}

// IsStopMessage reports whether an inbound text is an opt-out keyword.
func (s Settings) IsStopMessage(text string) bool {
	t := strings.ToUpper(strings.TrimSpace(strings.Trim(text, ".!؟? ")))
	for _, k := range s.StopKeywords {
		if t == strings.ToUpper(strings.TrimSpace(k)) {
			return true
		}
	}
	return false
}
