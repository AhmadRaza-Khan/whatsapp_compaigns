package api

import (
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wacampaigns/internal/auth"
)

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Server) login(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "email and password are required")
		return
	}
	email := normEmail(in.Email)
	key := email + "|" + c.ClientIP()
	if s.loginLimiter.Blocked(key) || s.ipLimiter.Blocked(c.ClientIP()) {
		fail(c, 429, "Too many login attempts. Try again in 15 minutes.")
		return
	}

	var u currentUser
	var hash string
	var active bool
	err := s.db.QueryRow(c, `SELECT id, email, name, role, password_hash, active FROM users WHERE email=$1`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash, &active)
	if err != nil {
		auth.CheckDummy(in.Password)
	}
	if err != nil || !active || !auth.CheckPassword(hash, in.Password) {
		s.loginLimiter.Hit(key)
		s.ipLimiter.Hit(c.ClientIP())
		fail(c, 401, "Wrong email or password")
		return
	}
	s.loginLimiter.Reset(key)
	tok, err := auth.IssueToken(s.cfg.JWTSecret, u.ID, u.Role)
	if err != nil {
		serverError(c, err)
		return
	}
	_, _ = s.db.Exec(c, `UPDATE users SET last_login_at=now() WHERE id=$1`, u.ID)
	s.setSession(c, tok, int(auth.SessionTTL.Seconds()))
	c.JSON(200, u)
}

func (s *Server) logout(c *gin.Context) {
	s.setSession(c, "", -1)
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) me(c *gin.Context) { c.JSON(200, user(c)) }

func (s *Server) changePassword(c *gin.Context) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	u := user(c)
	var hash string
	if err := s.db.QueryRow(c, `SELECT password_hash FROM users WHERE id=$1`, u.ID).Scan(&hash); err != nil {
		serverError(c, err)
		return
	}
	if !auth.CheckPassword(hash, in.Current) {
		fail(c, 400, "Current password is wrong")
		return
	}
	newHash, err := auth.HashPassword(in.New)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	if _, err := s.db.Exec(c, `UPDATE users SET password_hash=$2, password_changed_at=now(), updated_at=now() WHERE id=$1`, u.ID, newHash); err != nil {
		serverError(c, err)
		return
	}
	// Old sessions are now invalid; issue a fresh one for this browser.
	tok, _ := auth.IssueToken(s.cfg.JWTSecret, u.ID, u.Role)
	s.setSession(c, tok, int(auth.SessionTTL.Seconds()))
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) forgotPassword(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&in)
	email := normEmail(in.Email)
	// Same response whether or not the account exists.
	resp := gin.H{"ok": true, "message": "If that email has an account, a reset link has been sent."}
	if s.ipLimiter.Blocked("forgot|" + c.ClientIP()) {
		c.JSON(200, resp)
		return
	}
	s.ipLimiter.Hit("forgot|" + c.ClientIP())

	var id int64
	if err := s.db.QueryRow(c, `SELECT id FROM users WHERE email=$1 AND active`, email).Scan(&id); err != nil {
		c.JSON(200, resp)
		return
	}
	token, hash := auth.RandomToken()
	if _, err := s.db.Exec(c, `INSERT INTO password_resets (user_id, token_hash, expires_at) VALUES ($1,$2, now() + interval '1 hour')`, id, hash); err != nil {
		serverError(c, err)
		return
	}
	link := s.cfg.PublicURL + "/reset-password?token=" + token
	body := "A password reset was requested for your ShopAldukan account.\n\n" +
		"Open this link within 1 hour to choose a new password:\n" + link +
		"\n\nIf you did not request this, you can ignore this email."
	if err := s.mailer.Send(email, "Reset your ShopAldukan password", body); err != nil {
		// Without SMTP the link is written to the server log for the admin.
		slog.Warn("password reset email not sent; link logged instead", "email", email, "link", link, "err", err)
	}
	c.JSON(200, resp)
}

func (s *Server) resetPassword(c *gin.Context) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Token == "" {
		fail(c, 400, "invalid request")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	tx, err := s.db.Begin(c)
	if err != nil {
		serverError(c, err)
		return
	}
	defer tx.Rollback(c)
	var resetID, userID int64
	err = tx.QueryRow(c, `SELECT id, user_id FROM password_resets
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at > now() FOR UPDATE`, auth.HashToken(in.Token)).Scan(&resetID, &userID)
	if err != nil {
		fail(c, 400, "This reset link is invalid or has expired.")
		return
	}
	if _, err := tx.Exec(c, `UPDATE password_resets SET used_at=now() WHERE id=$1`, resetID); err != nil {
		serverError(c, err)
		return
	}
	if _, err := tx.Exec(c, `UPDATE users SET password_hash=$2, password_changed_at=now(), updated_at=now() WHERE id=$1`, userID, hash); err != nil {
		serverError(c, err)
		return
	}
	if err := tx.Commit(c); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- users (admin) ----

type userRow struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Active      bool       `json:"active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) listUsers(c *gin.Context) {
	rows, err := s.db.Query(c, `SELECT id, email, name, role, active, last_login_at, created_at FROM users ORDER BY id`)
	if err != nil {
		serverError(c, err)
		return
	}
	defer rows.Close()
	out := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Active, &u.LastLoginAt, &u.CreatedAt); err != nil {
			serverError(c, err)
			return
		}
		out = append(out, u)
	}
	c.JSON(200, out)
}

type userInput struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Active   *bool  `json:"active"`
	Password string `json:"password"`
}

func (s *Server) createUser(c *gin.Context) {
	var in userInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	email := normEmail(in.Email)
	if _, err := mail.ParseAddress(email); err != nil {
		fail(c, 400, "Enter a valid email address")
		return
	}
	if in.Role != "admin" && in.Role != "staff" {
		fail(c, 400, "role must be admin or staff")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	var id int64
	err = s.db.QueryRow(c, `INSERT INTO users (email, name, role, password_hash) VALUES ($1,$2,$3,$4) RETURNING id`,
		email, strings.TrimSpace(in.Name), in.Role, hash).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			fail(c, 409, "A user with that email already exists")
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(201, gin.H{"id": id})
}

func (s *Server) updateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in userInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, 400, "invalid request")
		return
	}
	if in.Role != "admin" && in.Role != "staff" {
		fail(c, 400, "role must be admin or staff")
		return
	}
	active := in.Active == nil || *in.Active
	if id == user(c).ID && (in.Role != "admin" || !active) {
		fail(c, 400, "You cannot remove your own admin access")
		return
	}
	if _, err := s.db.Exec(c, `UPDATE users SET name=$2, role=$3, active=$4, updated_at=now() WHERE id=$1`,
		id, strings.TrimSpace(in.Name), in.Role, active); err != nil {
		serverError(c, err)
		return
	}
	if in.Password != "" {
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		_, _ = s.db.Exec(c, `UPDATE users SET password_hash=$2, password_changed_at=now() WHERE id=$1`, id, hash)
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if id == user(c).ID {
		fail(c, 400, "You cannot delete your own account")
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM users WHERE id=$1`, id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
