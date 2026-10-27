package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds everything read from the environment. Secrets (Bird API key,
// webhook secret, JWT secret, SMTP password) live only here, never in the DB
// or the browser.
type Config struct {
	Env               string
	Port              string
	DatabaseURL       string
	JWTSecret         string
	PublicURL         string // e.g. https://campaigns.example.com (no trailing slash)
	StaticDir         string // built frontend; served by the API when set
	BirdAPIKey        string
	BirdAPIHost       string
	BirdWebhookSecret string
	SMTPHost          string
	SMTPPort          int
	SMTPUser          string
	SMTPPass          string
	SMTPFrom          string
}

func Load() (*Config, error) {
	c := &Config{
		Env:               env("APP_ENV", "production"),
		Port:              env("PORT", "8080"),
		DatabaseURL:       env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/wacampaigns?sslmode=disable"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		PublicURL:         strings.TrimRight(env("PUBLIC_URL", "http://localhost:5173"), "/"),
		StaticDir:         os.Getenv("STATIC_DIR"),
		BirdAPIKey:        os.Getenv("BIRD_API_KEY"),
		BirdAPIHost:       strings.TrimRight(env("BIRD_API_HOST", "https://eu1.platform.bird.com"), "/"),
		BirdWebhookSecret: os.Getenv("BIRD_WEBHOOK_SECRET"),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPass:          os.Getenv("SMTP_PASS"),
		SMTPFrom:          os.Getenv("SMTP_FROM"),
	}
	c.SMTPPort, _ = strconv.Atoi(env("SMTP_PORT", "587"))

	if c.JWTSecret == "" {
		if c.IsDev() {
			c.JWTSecret = "dev-only-insecure-secret"
		} else {
			return nil, fmt.Errorf("JWT_SECRET is required")
		}
	}
	if len(c.JWTSecret) < 16 && !c.IsDev() {
		return nil, fmt.Errorf("JWT_SECRET must be at least 16 characters")
	}
	return c, nil
}

func (c *Config) IsDev() bool { return c.Env == "development" }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
