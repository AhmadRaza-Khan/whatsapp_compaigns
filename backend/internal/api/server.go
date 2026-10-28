package api

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wacampaigns/internal/auth"
	"wacampaigns/internal/bird"
	"wacampaigns/internal/config"
)

const sessionCookie = "wac_session"

// Qatar is the business time zone; scheduled campaigns are entered in it.
var Qatar = mustLoc("Asia/Qatar")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

type Server struct {
	db           *pgxpool.Pool
	cfg          *config.Config
	bird         *bird.Client
	mailer       auth.Mailer
	loginLimiter *auth.RateLimiter
	ipLimiter    *auth.RateLimiter
}

func New(db *pgxpool.Pool, cfg *config.Config, bc *bird.Client) *Server {
	return &Server{
		db:   db,
		cfg:  cfg,
		bird: bc,
		mailer: auth.Mailer{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Pass: cfg.SMTPPass, From: cfg.SMTPFrom},
		loginLimiter: auth.NewRateLimiter(5, 15*time.Minute),  // per email+IP
		ipLimiter:    auth.NewRateLimiter(30, 15*time.Minute), // per IP
	}
}

func (s *Server) Router() *gin.Engine {
	if !s.cfg.IsDev() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(), securityHeaders())
	r.MaxMultipartMemory = 12 << 20
	// Behind Caddy: trust the proxy on the docker network for client IPs.
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.POST("/webhooks/bird", s.birdWebhook)
	r.GET("/r/:campaign/:token", s.clickRedirect)
	r.GET("/media/:name", s.serveMedia)

	api := r.Group("/api")
	api.POST("/auth/login", s.login)
	api.POST("/auth/logout", s.logout)
	api.POST("/auth/forgot", s.forgotPassword)
	api.POST("/auth/reset", s.resetPassword)

	authed := api.Group("", s.requireAuth)
	authed.GET("/auth/me", s.me)
	authed.POST("/auth/change-password", s.changePassword)

	authed.GET("/dashboard", s.dashboard)
	authed.POST("/uploads/image", s.uploadImage)

	authed.GET("/contacts", s.listContacts)
	authed.POST("/contacts", s.createContact)
	authed.GET("/contacts/export.csv", s.exportContacts)
	authed.GET("/contacts/tags", s.listTags)
	authed.POST("/contacts/import", s.importContacts)
	authed.GET("/contacts/imports", s.listImports)
	authed.GET("/contacts/imports/:id", s.getImport)
	authed.POST("/contacts/bulk", s.bulkContacts)
	authed.PUT("/contacts/:id", s.updateContact)
	authed.DELETE("/contacts/:id", s.deleteContact)
	authed.POST("/contacts/:id/opt-out", s.optOutContact)

	authed.GET("/groups", s.listGroups)
	authed.POST("/groups", s.createGroup)
	authed.PUT("/groups/:id", s.updateGroup)
	authed.DELETE("/groups/:id", s.deleteGroup)

	authed.GET("/templates", s.listTemplates)
	authed.GET("/templates/:slug", s.getTemplate)
	authed.POST("/templates/sync", s.syncTemplates)

	authed.GET("/campaigns", s.listCampaigns)
	authed.POST("/campaigns", s.createCampaign)
	authed.POST("/campaigns/estimate", s.estimateAudience)
	authed.GET("/campaigns/:id", s.getCampaign)
	authed.PUT("/campaigns/:id", s.updateCampaign)
	authed.DELETE("/campaigns/:id", s.deleteCampaign)
	authed.POST("/campaigns/:id/test", s.testSend)
	authed.POST("/campaigns/:id/send", s.sendCampaign)
	authed.POST("/campaigns/:id/pause", s.pauseCampaign)
	authed.POST("/campaigns/:id/resume", s.resumeCampaign)
	authed.POST("/campaigns/:id/cancel", s.cancelCampaign)
	authed.POST("/campaigns/:id/unschedule", s.unscheduleCampaign)
	authed.POST("/campaigns/:id/duplicate", s.duplicateCampaign)
	authed.GET("/campaigns/:id/recipients", s.listRecipients)
	authed.GET("/campaigns/:id/replies", s.listReplies)
	authed.GET("/campaigns/:id/export.csv", s.exportRecipients)

	admin := authed.Group("", s.requireAdmin)
	admin.POST("/contacts/:id/opt-in", s.optInContact)
	admin.POST("/contacts/sync-opt-outs", s.syncOptOuts)
	admin.POST("/templates", s.createTemplate)
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.PUT("/users/:id", s.updateUser)
	admin.DELETE("/users/:id", s.deleteUser)
	admin.GET("/settings", s.getSettings)
	admin.PUT("/settings", s.putSettings)
	admin.GET("/settings/bird-status", s.birdStatus)

	if s.cfg.StaticDir != "" {
		s.serveSPA(r)
	}
	return r
}

// serveSPA serves the built React app; unknown paths fall back to index.html.
func (s *Server) serveSPA(r *gin.Engine) {
	dir := s.cfg.StaticDir
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		p := filepath.Join(dir, filepath.Clean("/"+c.Request.URL.Path))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			c.File(p)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.File(filepath.Join(dir, "index.html"))
	})
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/healthz" {
			return
		}
		slog.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "ms", time.Since(start).Milliseconds())
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// ---- auth middleware ----

type currentUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func (s *Server) requireAuth(c *gin.Context) {
	tok, err := c.Cookie(sessionCookie)
	if err != nil || tok == "" {
		c.AbortWithStatusJSON(401, gin.H{"error": "not logged in"})
		return
	}
	claims, err := auth.ParseToken(s.cfg.JWTSecret, tok)
	if err != nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "session expired"})
		return
	}
	// Re-check the user each request so deactivation and role changes apply
	// immediately; tokens issued before a password change are rejected.
	var u currentUser
	var active bool
	var pwChanged time.Time
	err = s.db.QueryRow(c, `SELECT id, email, name, role, active, password_changed_at FROM users WHERE id=$1`, claims.UserID).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &active, &pwChanged)
	if err != nil || !active {
		c.AbortWithStatusJSON(401, gin.H{"error": "account disabled"})
		return
	}
	if claims.IssuedAt != nil && claims.IssuedAt.Time.Add(time.Second).Before(pwChanged) {
		c.AbortWithStatusJSON(401, gin.H{"error": "session expired"})
		return
	}
	// CSRF defence in addition to SameSite=Strict: mutating requests must be
	// JSON or multipart sent by our own frontend, which always sets this header.
	if c.Request.Method != http.MethodGet && c.GetHeader("X-Requested-With") != "fetch" {
		c.AbortWithStatusJSON(403, gin.H{"error": "missing request header"})
		return
	}
	c.Set("user", u)
	c.Next()
}

func (s *Server) requireAdmin(c *gin.Context) {
	if user(c).Role != "admin" {
		c.AbortWithStatusJSON(403, gin.H{"error": "admin only"})
		return
	}
	c.Next()
}

func user(c *gin.Context) currentUser {
	u, _ := c.Get("user")
	cu, _ := u.(currentUser)
	return cu
}

// ---- helpers ----

func fail(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func serverError(c *gin.Context, err error) {
	slog.Error("request failed", "path", c.Request.URL.Path, "err", err)
	fail(c, 500, "Something went wrong. Please try again.")
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, 400, "invalid id")
		return 0, false
	}
	return id, true
}

func pageParams(c *gin.Context) (page, size, offset int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 50
	}
	return page, size, (page - 1) * size
}

func (s *Server) setSession(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, token, maxAge, "/", "", !s.cfg.IsDev(), true)
}
