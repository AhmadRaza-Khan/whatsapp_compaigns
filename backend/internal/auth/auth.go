package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const SessionTTL = 12 * time.Hour

func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func ValidatePassword(pw string) error {
	if len(pw) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(pw) > 72 {
		return errors.New("password must be at most 72 characters")
	}
	return nil
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash keeps login timing similar for unknown emails.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equaliser"), 12)

func CheckDummy(pw string) { _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw)) }

type Claims struct {
	UserID int64  `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func IssueToken(secret string, userID int64, role string) (string, error) {
	now := time.Now()
	c := Claims{UserID: userID, Role: role, RegisteredClaims: jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

func ParseToken(secret, token string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// RandomToken returns a URL-safe random token and its SHA-256 hash (stored).
func RandomToken() (token, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// RateLimiter allows `limit` failures per key within `window`.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (r *RateLimiter) prune(key string, now time.Time) []time.Time {
	kept := r.hits[key][:0]
	for _, t := range r.hits[key] {
		if now.Sub(t) < r.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(r.hits, key)
		return nil
	}
	r.hits[key] = kept
	return kept
}

// Blocked reports whether the key has used up its attempts.
func (r *RateLimiter) Blocked(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prune(key, time.Now())) >= r.limit
}

func (r *RateLimiter) Hit(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits[key] = append(r.prune(key, time.Now()), time.Now())
}

func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hits, key)
}

// Mailer sends plain-text email via SMTP. When unconfigured, Send returns
// ErrNoMailer and callers log the message instead.
type Mailer struct {
	Host, User, Pass, From string
	Port                   int
}

var ErrNoMailer = errors.New("smtp not configured")

func (m Mailer) Send(to, subject, body string) error {
	if m.Host == "" || m.From == "" {
		return ErrNoMailer
	}
	msg := strings.Join([]string{
		"From: " + m.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	var a smtp.Auth
	if m.User != "" {
		a = smtp.PlainAuth("", m.User, m.Pass, m.Host)
	}
	return smtp.SendMail(fmt.Sprintf("%s:%d", m.Host, m.Port), a, m.From, []string{to}, []byte(msg))
}
