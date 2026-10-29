// Package bird is a minimal client for the Bird platform API
// (https://bird.com/docs/api/reference) covering what the campaign platform
// needs: template sends, message lookup, templates and preferences (opt-outs).
package bird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	host   string
	apiKey string
	http   *http.Client
}

func New(host, apiKey string) *Client {
	return &Client{host: host, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) Configured() bool { return c.apiKey != "" }

// APIError is a non-2xx response from Bird.
type APIError struct {
	Status  int
	Code    string
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("bird %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("bird %d: %s", e.Status, e.Message)
}

// Temporary reports whether the request may succeed if retried later.
func (e *APIError) Temporary() bool {
	if e.Status == 429 || e.Status >= 500 {
		return true
	}
	// A concurrent request holds the same idempotency key; it expires within 30s.
	return e.Status == 409 && (e.Code == "request_in_progress" || e.Code == "E01004")
}

// IsTemporary is true for network failures and retryable API errors.
func IsTemporary(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Temporary()
	}
	return err != nil // transport error (timeout, DNS, connection reset)
}

func (c *Client) do(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	if c.apiKey == "" {
		return &APIError{Status: 401, Code: "not_configured", Message: "BIRD_API_KEY is not set on the server"}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.host+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return parseError(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func parseError(status int, raw []byte) error {
	e := &APIError{Status: status, Body: string(raw)}
	var generic map[string]any
	if json.Unmarshal(raw, &generic) == nil {
		// Bird error bodies vary slightly by endpoint; look in the usual places.
		src := generic
		if inner, ok := generic["error"].(map[string]any); ok {
			src = inner
		}
		for _, k := range []string{"code", "type", "name"} {
			if v, ok := src[k].(string); ok && v != "" {
				e.Code = v
				break
			}
		}
		for _, k := range []string{"message", "description", "detail", "title"} {
			if v, ok := src[k].(string); ok && v != "" {
				e.Message = v
				break
			}
		}
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
		if len(raw) > 0 && len(raw) < 300 {
			e.Message = string(raw)
		}
	}
	return e
}

// ---- Messages ----

type Param struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	// Filename is the name shown on a document header; sent only for documents.
	Filename string `json:"filename,omitempty"`
}

type Component struct {
	Type       string  `json:"type"` // header | body | button
	Parameters []Param `json:"parameters"`
}

type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TemplateRef struct {
	Slug       string      `json:"slug"`
	Language   string      `json:"language,omitempty"`
	Components []Component `json:"components,omitempty"`
}

type SendRequest struct {
	To       string         `json:"to"`
	From     string         `json:"from,omitempty"`
	Template TemplateRef    `json:"template"`
	Tags     []Tag          `json:"tags,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Cost struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currency_code"`
}

type Message struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Cost      *Cost  `json:"cost"`
	LastError *struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	} `json:"last_error"`
}

// SendTemplate sends one template message (Bird has no batch endpoint).
// idempotencyKey makes a retried request return the original result instead
// of sending again (Bird retains keys for ~3 hours).
func (c *Client) SendTemplate(ctx context.Context, req SendRequest, idempotencyKey string) (*Message, error) {
	var out Message
	h := map[string]string{}
	if idempotencyKey != "" {
		h["Idempotency-Key"] = idempotencyKey
	}
	if err := c.do(ctx, http.MethodPost, "/v1/whatsapp/messages", req, h, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMessage(ctx context.Context, id string) (*Message, error) {
	var out Message
	if err := c.do(ctx, http.MethodGet, "/v1/whatsapp/messages/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Templates ----

type Template struct {
	ID                 string                     `json:"id"`
	Slug               string                     `json:"slug"`
	Name               string                     `json:"name"`
	Scope              string                     `json:"scope"`
	Category           string                     `json:"category"`
	Status             string                     `json:"status"`
	DefaultLanguage    string                     `json:"default_language"`
	AvailableLanguages []string                   `json:"available_languages"`
	Languages          map[string]json.RawMessage `json:"languages"`
	LiveVersionID      *string                    `json:"live_version_id"`
	DraftVersionID     *string                    `json:"draft_version_id"`
	PendingVersionID   *string                    `json:"pending_version_id"`
}

type TemplateLanguage struct {
	Language   string          `json:"language"`
	Status     string          `json:"status"`
	Category   string          `json:"category"`
	Components json.RawMessage `json:"components"`
}

// ListTemplates returns all workspace templates (paginated by cursor).
func (c *Client) ListTemplates(ctx context.Context) ([]json.RawMessage, error) {
	var all []json.RawMessage
	cursor := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"scope": {"workspace"}, "limit": {"100"}}
		if cursor != "" {
			q.Set("starting_after", cursor)
		}
		var out struct {
			Data       []json.RawMessage `json:"data"`
			NextCursor *string           `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/v1/whatsapp/templates?"+q.Encode(), nil, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.NextCursor == nil || *out.NextCursor == "" {
			break
		}
		cursor = *out.NextCursor
	}
	return all, nil
}

func (c *Client) GetTemplateLanguage(ctx context.Context, slug, versionID, lang string) (*TemplateLanguage, error) {
	var out TemplateLanguage
	path := fmt.Sprintf("/v1/whatsapp/templates/%s/versions/%s/languages/%s",
		url.PathEscape(slug), url.PathEscape(versionID), url.PathEscape(lang))
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type CreateTemplateRequest struct {
	WABA            string                    `json:"waba"`
	Slug            string                    `json:"slug"`
	Name            string                    `json:"name,omitempty"`
	Description     string                    `json:"description,omitempty"`
	Category        string                    `json:"category"`
	DefaultLanguage string                    `json:"default_language"`
	Languages       map[string]map[string]any `json:"languages"`
}

func (c *Client) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*Template, error) {
	var out Template
	if err := c.do(ctx, http.MethodPost, "/v1/whatsapp/templates", req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SubmitResult struct {
	Valid  bool `json:"valid"`
	Errors []struct {
		Language string `json:"language"`
		Field    string `json:"field"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func (c *Client) SubmitTemplateVersion(ctx context.Context, slug, versionID string) (*SubmitResult, error) {
	var out SubmitResult
	path := fmt.Sprintf("/v1/whatsapp/templates/%s/versions/%s/submit", url.PathEscape(slug), url.PathEscape(versionID))
	h := map[string]string{"Idempotency-Key": "template-submit/" + slug + "/" + versionID}
	if err := c.do(ctx, http.MethodPost, path, map[string]any{}, h, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Preferences (opt-outs) ----

type Preference struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Handle  string `json:"handle"`
	Status  string `json:"status"` // granted | revoked
	Origin  string `json:"origin"`
}

// RecordPreference writes an opt-out ("revoked") or re-consent ("granted").
func (c *Client) RecordPreference(ctx context.Context, phone, status, source string) error {
	body := map[string]any{
		"channel":  "whatsapp",
		"handle":   phone,
		"status":   status,
		"coverage": "non_transactional",
		"source":   source,
	}
	if status == "granted" {
		body["consented_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	return c.do(ctx, http.MethodPost, "/v1/preferences", body, nil, nil)
}

// ListRevokedPreferences returns WhatsApp handles that opted out in Bird.
func (c *Client) ListRevokedPreferences(ctx context.Context) ([]Preference, error) {
	var all []Preference
	cursor := ""
	for page := 0; page < 200; page++ {
		q := url.Values{"channel": {"whatsapp"}, "status": {"revoked"}, "limit": {"100"}}
		if cursor != "" {
			q.Set("starting_after", cursor)
		}
		var out struct {
			Data       []Preference `json:"data"`
			NextCursor *string      `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/v1/preferences?"+q.Encode(), nil, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.NextCursor == nil || *out.NextCursor == "" {
			break
		}
		cursor = *out.NextCursor
	}
	return all, nil
}
