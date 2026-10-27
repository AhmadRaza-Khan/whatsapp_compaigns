package worker

// End-to-end test of the acceptance criteria against a real Postgres and a
// fake Bird API. Run with:
//
//	make test-e2e   (or TEST_DATABASE_URL=postgres://…/wacampaigns_test go test ./internal/worker -run E2E -v)

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wacampaigns/internal/api"
	"wacampaigns/internal/auth"
	"wacampaigns/internal/bird"
	"wacampaigns/internal/config"
	"wacampaigns/internal/db"
)

// Built at runtime so secret scanners don't mistake this test value for a real key.
var webhookSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("e2e-test-signing-key"))

// fakeBird mimics the Bird endpoints the platform uses.
type fakeBird struct {
	mu        sync.Mutex
	byKey     map[string]string // idempotency key -> message id
	sends     map[string]int    // phone -> real (non-replayed) sends
	requests  []bird.SendRequest
	prefs     []string
	failPhone string // returns 500 for this number
}

func newFakeBird() *fakeBird {
	return &fakeBird{byKey: map[string]string{}, sends: map[string]int{}}
}

const launchComponents = `[
 {"type":"header","format":"image","example_parameters":[{"type":"image","url":"https://cdn.example.com/a.jpg"}]},
 {"type":"body","text":"Hi {{1}}, the {{2}} collection is here: {{3}}","example_parameters":[{"type":"text","text":"Sara"},{"type":"text","text":"Winter"},{"type":"text","text":"20% off"}]},
 {"type":"footer","text":"Reply STOP to unsubscribe"},
 {"type":"buttons","buttons":[{"type":"url","text":"Shop Now","url":"https://campaigns.example.com/r/{{1}}","example_parameters":[{"type":"text","text":"1/abc"}]}]}
]`

func (f *fakeBird) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/whatsapp/messages":
		var req bird.SendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.To == f.failPhone {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"code":"internal","message":"temporary"}`))
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if id, ok := f.byKey[key]; ok && key != "" {
			w.Header().Set("Idempotency-Replay", "true")
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"id":%q,"status":"accepted"}`, id)
			return
		}
		id := fmt.Sprintf("wam_%d", len(f.requests)+1)
		if key != "" {
			f.byKey[key] = id
		}
		f.sends[req.To]++
		f.requests = append(f.requests, req)
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"id":%q,"status":"accepted"}`, id)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/whatsapp/messages/"):
		fmt.Fprint(w, `{"id":"x","status":"delivered","cost":{"amount":"0.03910","currency_code":"USD"}}`)
	case r.Method == "GET" && r.URL.Path == "/v1/whatsapp/templates":
		fmt.Fprint(w, `{"data":[{"id":"wat_1","slug":"collection_launch","name":"Collection launch","scope":"workspace",
			"category":"marketing","status":"active","default_language":"en","available_languages":["en"],
			"languages":{"en":{"status":"approved"}},"live_version_id":"wav_1"}],"next_cursor":null}`)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/whatsapp/templates/"):
		fmt.Fprintf(w, `{"language":"en","status":"approved","components":%s}`, launchComponents)
	case r.Method == "POST" && r.URL.Path == "/v1/preferences":
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.prefs = append(f.prefs, fmt.Sprint(p["handle"], ":", p["status"]))
		w.WriteHeader(201)
		fmt.Fprint(w, `{"applied":true}`)
	default:
		w.WriteHeader(404)
	}
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "fetch")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode >= 400 {
		c.t.Logf("%s %s -> %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode
}

func (c *client) upload(path, filename, content string, fields map[string]string, out any) int {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write([]byte(content))
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Requested-With", "fetch")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, out)
	if resp.StatusCode >= 400 {
		c.t.Logf("upload -> %d %s", resp.StatusCode, raw)
	}
	return resp.StatusCode
}

var webhookSeq int

func (c *client) webhook(eventType string, data map[string]any) {
	c.t.Helper()
	webhookSeq++
	body, _ := json.Marshal(map[string]any{"type": eventType, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data})
	id := fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), webhookSeq)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest("POST", c.base+"/webhooks/bird", bytes.NewReader(body))
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", bird.Sign(webhookSecret, id, ts, body))
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		c.t.Fatalf("webhook %s failed: %v %v", eventType, err, resp)
	}
	resp.Body.Close()
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestE2E(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if !strings.Contains(dbURL, "test") {
		t.Fatal("refusing to run: this test wipes the database; use a database whose name contains \"test\"")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, _ = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	fb := newFakeBird()
	birdSrv := httptest.NewServer(fb)
	defer birdSrv.Close()

	cfg := &config.Config{Env: "development", JWTSecret: "test-secret-test-secret", PublicURL: "https://campaigns.example.com",
		BirdAPIKey: "bk_eu1_test", BirdAPIHost: birdSrv.URL, BirdWebhookSecret: webhookSecret}
	bc := bird.New(cfg.BirdAPIHost, cfg.BirdAPIKey)
	apiSrv := httptest.NewServer(api.New(pool, cfg, bc).Router())
	defer apiSrv.Close()

	hash, _ := auth.HashPassword("correct-horse-battery")
	_, _ = pool.Exec(ctx, `INSERT INTO users (email, name, role, password_hash) VALUES ('admin@example.com','Admin','admin',$1)`, hash)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: apiSrv.URL, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	// Login rate limiting.
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "wrong-password"}, nil)
	}
	if code := c.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "correct-horse-battery"}, nil); code != 429 {
		t.Fatalf("expected login to be rate limited, got %d", code)
	}
	// Fresh server instance resets the in-memory limiter.
	apiSrv.Config.Handler = api.New(pool, cfg, bc).Router()
	if code := c.do("POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "correct-horse-battery"}, nil); code != 200 {
		t.Fatalf("login failed: %d", code)
	}
	_ = c.do("PUT", "/api/settings", map[string]any{"marketing_rate": 0.0391, "currency": "USD", "send_rate_per_minute": 1000,
		"default_region": "QA", "bird_from": "+97440000000", "stop_keywords": []string{"STOP"}, "name_fallback": "Customer"}, nil)

	// --- Acceptance: import a 2,500-row file; duplicates and invalid flagged.
	var sb strings.Builder
	sb.WriteString("Name,Mobile,Tags\n")
	for i := 0; i < 2450; i++ {
		fmt.Fprintf(&sb, "Customer %d,%d,vip\n", i, 50000000+i) // valid Qatar mobiles 5xxxxxxx
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "Dup %d,+974 %d,\n", i, 50000000+i) // same numbers in international format
	}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "Bad %d,12%d,\n", i, i)
	}
	var imp struct {
		Total, Created, Duplicates, Invalid int
	}
	start := time.Now()
	if code := c.upload("/api/contacts/import", "customers.csv", sb.String(), nil, &imp); code != 200 {
		t.Fatalf("import failed: %d", code)
	}
	if imp.Total != 2500 || imp.Created != 2450 || imp.Duplicates != 30 || imp.Invalid != 20 {
		t.Fatalf("import counts wrong: %+v", imp)
	}
	t.Logf("imported 2500 rows in %v: %+v", time.Since(start), imp)

	// Test group of 10.
	var grp10 struct{ Created int }
	var testCSV strings.Builder
	testCSV.WriteString("phone,name\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&testCSV, "+9746600000%d,Tester %d\n", i, i)
	}
	c.upload("/api/contacts/import", "testers.csv", testCSV.String(), map[string]string{"new_group_name": "Test group"}, &grp10)
	var groups []struct {
		ID       int64
		Name     string
		Contacts int
	}
	c.do("GET", "/api/groups", nil, &groups)
	if len(groups) != 1 || groups[0].Contacts != 10 {
		t.Fatalf("groups wrong: %+v", groups)
	}
	groupID := groups[0].ID

	// Templates synced from Bird.
	var sync struct{ Synced int }
	if c.do("POST", "/api/templates/sync", nil, &sync); sync.Synced != 1 {
		t.Fatalf("template sync: %+v", sync)
	}

	newCampaign := func(name string) int64 {
		var res struct{ ID int64 }
		code := c.do("POST", "/api/campaigns", map[string]any{
			"name": name, "template_slug": "collection_launch",
			"variables": map[string]any{"header_media_url": "https://cdn.example.com/w.jpg", "body": []string{"{first_name}", "Winter", "20% off"}, "shop_url": "https://shop.example.com/winter"},
			"audience":  map[string]any{"group_ids": []int64{groupID}},
		}, &res)
		if code != 201 {
			t.Fatalf("create campaign: %d", code)
		}
		return res.ID
	}

	camp1 := newCampaign("Winter launch")
	var est struct {
		Eligible      int
		EstimatedCost float64 `json:"estimated_cost"`
	}
	c.do("POST", "/api/campaigns/estimate", map[string]any{"audience": map[string]any{"group_ids": []int64{groupID}}}, &est)
	if est.Eligible != 10 {
		t.Fatalf("estimate: %+v", est)
	}

	// Test send to own number.
	if code := c.do("POST", fmt.Sprintf("/api/campaigns/%d/test", camp1), map[string]string{"phone": "55123456"}, nil); code != 200 {
		t.Fatal("test send failed")
	}

	// --- Acceptance: stopping the worker mid-campaign causes no double sends.
	// Simulate a crash after Bird accepted a message but before we stored the
	// result: launch, then mark one recipient as stuck in "sending" with its
	// idempotency key already used at Bird.
	if code := c.do("POST", fmt.Sprintf("/api/campaigns/%d/send", camp1), map[string]string{}, nil); code != 200 {
		t.Fatal("send failed")
	}
	w := New(pool, bc, cfg)
	w.schedulerTick(ctx) // launches the campaign (materialises recipients)
	var stuckID int64
	var stuckPhone string
	_ = pool.QueryRow(ctx, `SELECT id, phone FROM campaign_recipients WHERE campaign_id=$1 ORDER BY id LIMIT 1`, camp1).Scan(&stuckID, &stuckPhone)
	fb.mu.Lock()
	fb.byKey[IdempotencyKey(camp1, stuckID)] = "wam_crashed"
	fb.sends[stuckPhone]++
	fb.mu.Unlock()
	_, _ = pool.Exec(ctx, `UPDATE campaign_recipients SET status='sending', locked_at=now(), attempts=1 WHERE id=$1`, stuckID)

	// Run the worker, stop it part-way, start it again.
	runWorker := func() (context.CancelFunc, chan struct{}) {
		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = New(pool, bc, cfg).Run(wctx); close(done) }()
		return cancel, done
	}
	_, _ = pool.Exec(ctx, `UPDATE settings SET value = jsonb_set(value, '{send_rate_per_minute}', '120')`) // 1 per 500ms
	cancel, done := runWorker()
	waitFor(t, "a few sends", 10*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=$1 AND bird_message_id IS NOT NULL`, camp1).Scan(&n)
		return n >= 3
	})
	cancel()
	<-done
	_, _ = pool.Exec(ctx, `UPDATE settings SET value = jsonb_set(value, '{send_rate_per_minute}', '1000')`)
	cancel, done = runWorker()
	waitFor(t, "campaign completed", 20*time.Second, func() bool {
		var st string
		_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, camp1).Scan(&st)
		return st == "completed"
	})
	fb.mu.Lock()
	for p, n := range fb.sends {
		if strings.HasPrefix(p, "+974660") && n != 1 {
			t.Errorf("%s was sent %d times", p, n)
		}
	}
	lastReq := fb.requests[len(fb.requests)-1]
	fb.mu.Unlock()
	if lastReq.From != "+97440000000" || len(lastReq.Template.Components) != 3 || len(lastReq.Tags) != 3 {
		t.Errorf("unexpected send payload: %+v", lastReq)
	}
	var stuckMID string
	_ = pool.QueryRow(ctx, `SELECT bird_message_id FROM campaign_recipients WHERE id=$1`, stuckID).Scan(&stuckMID)
	if stuckMID != "wam_crashed" {
		t.Errorf("crashed send not recovered via idempotency replay, got %q", stuckMID)
	}

	// --- Acceptance: delivered, read and clicks appear in the report.
	type rcpt struct {
		ID        int64
		Phone     string
		MessageID string
		Token     string
	}
	var rs []rcpt
	rows, _ := pool.Query(ctx, `SELECT id, phone, bird_message_id, token FROM campaign_recipients WHERE campaign_id=$1 ORDER BY id`, camp1)
	for rows.Next() {
		var r rcpt
		_ = rows.Scan(&r.ID, &r.Phone, &r.MessageID, &r.Token)
		rs = append(rs, r)
	}
	rows.Close()
	tags := func(r rcpt) []map[string]string {
		return []map[string]string{{"name": "campaign_id", "value": strconv.FormatInt(camp1, 10)}, {"name": "recipient_id", "value": strconv.FormatInt(r.ID, 10)}}
	}
	for i, r := range rs {
		data := map[string]any{"whatsapp_id": r.MessageID, "direction": "outbound", "to": map[string]string{"phone_number": r.Phone}, "tags": tags(r)}
		switch {
		case i < 6: // read arrives before delivered for some: order must not matter
			c.webhook("whatsapp.read", data)
			c.webhook("whatsapp.delivered", data)
		case i < 9:
			c.webhook("whatsapp.sent", data)
			c.webhook("whatsapp.delivered", data)
		default:
			data["error"] = map[string]any{"code": "undeliverable", "description": "Recipient is not on WhatsApp", "meta_error_code": "131026"}
			c.webhook("whatsapp.failed", data)
		}
	}
	// Clicks: two customer taps on one link, one crawler, one unknown token.
	for _, ua := range []string{"Mozilla/5.0", "Mozilla/5.0", "WhatsApp/2.24 A"} {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/r/%d/%s", apiSrv.URL, camp1, rs[0].Token), nil)
		req.Header.Set("User-Agent", ua)
		resp, _ := c.http.Do(req)
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://shop.example.com/winter" {
			t.Fatalf("redirect wrong: %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/r/%d/%s", apiSrv.URL, camp1, rs[1].Token), nil)
	resp, _ := c.http.Do(req)
	resp.Body.Close()

	// --- Acceptance: a contact who replies STOP is excluded from the next campaign.
	c.webhook("whatsapp.received", map[string]any{"whatsapp_id": "wam_in_1", "direction": "inbound",
		"from": map[string]string{"phone_number": rs[2].Phone}, "text": map[string]string{"body": "stop"}})
	c.webhook("whatsapp.received", map[string]any{"whatsapp_id": "wam_in_2", "direction": "inbound",
		"from": map[string]string{"phone_number": rs[3].Phone}, "text": map[string]string{"body": "Love it! Do you have size M?"}})

	w.processWebhooks(ctx)
	_, _ = pool.Exec(ctx, `UPDATE campaign_recipients SET updated_at = now() - interval '2 minutes'`)
	w.fetchCosts(ctx)

	var report struct {
		Stats struct {
			Recipients, Sent, Delivered, Read, Failed, Clicked, Clicks, Replied int
			ActualCost                                                          float64 `json:"actual_cost"`
		}
		EstimatedCost  float64 `json:"estimated_cost"`
		FailureReasons []struct {
			Reason string
			Count  int
		} `json:"failure_reasons"`
	}
	c.do("GET", fmt.Sprintf("/api/campaigns/%d", camp1), nil, &report)
	st := report.Stats
	if st.Recipients != 10 || st.Delivered != 9 || st.Read != 6 || st.Failed != 1 || st.Clicked != 2 || st.Clicks != 3 || st.Replied != 2 {
		t.Fatalf("report wrong: %+v", st)
	}
	if len(report.FailureReasons) != 1 || !strings.Contains(report.FailureReasons[0].Reason, "131026") {
		t.Fatalf("failure reason missing: %+v", report.FailureReasons)
	}
	// --- Acceptance: cost estimate matches actual cost within a few percent.
	if diff := (report.EstimatedCost - st.ActualCost) / report.EstimatedCost; diff > 0.15 || diff < -0.05 {
		t.Fatalf("estimate %.4f vs actual %.4f", report.EstimatedCost, st.ActualCost)
	}
	t.Logf("report: %+v estimated=%.4f", st, report.EstimatedCost)

	var optedOut bool
	_ = pool.QueryRow(ctx, `SELECT opted_out FROM contacts WHERE phone=$1`, rs[2].Phone).Scan(&optedOut)
	if !optedOut {
		t.Fatal("STOP reply did not opt the contact out")
	}
	fb.mu.Lock()
	if len(fb.prefs) != 1 || fb.prefs[0] != rs[2].Phone+":revoked" {
		t.Errorf("opt-out not synced to Bird: %v", fb.prefs)
	}
	fb.mu.Unlock()

	// --- Acceptance: a scheduled campaign starts on time (Asia/Qatar input).
	camp2 := newCampaign("Follow-up")
	in := time.Now().In(api.Qatar).Add(time.Minute).Format("2006-01-02T15:04")
	if code := c.do("POST", fmt.Sprintf("/api/campaigns/%d/send", camp2), map[string]string{"scheduled_at": in}, nil); code != 200 {
		t.Fatal("schedule failed")
	}
	var sched time.Time
	_ = pool.QueryRow(ctx, `SELECT scheduled_at FROM campaigns WHERE id=$1`, camp2).Scan(&sched)
	if got := sched.In(api.Qatar).Format("2006-01-02T15:04"); got != in {
		t.Fatalf("scheduled time stored wrong: %s vs %s", got, in)
	}
	// Fast-forward: make it due in 1 second, and check it is not started early.
	_, _ = pool.Exec(ctx, `UPDATE campaigns SET scheduled_at = now() + interval '1 second' WHERE id=$1`, camp2)
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, camp2).Scan(&status)
	if status != "scheduled" {
		t.Fatalf("started early: %s", status)
	}
	waitFor(t, "scheduled campaign to complete", 20*time.Second, func() bool {
		_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, camp2).Scan(&status)
		return status == "completed"
	})
	var n2 int
	var stoppedIncluded bool
	_ = pool.QueryRow(ctx, `SELECT count(*), bool_or(phone=$2) FROM campaign_recipients WHERE campaign_id=$1`, camp2, rs[2].Phone).Scan(&n2, &stoppedIncluded)
	if n2 != 9 || stoppedIncluded {
		t.Fatalf("opted-out contact not excluded: recipients=%d included=%v", n2, stoppedIncluded)
	}

	// Pause / resume / cancel and temporary-error retry.
	fb.mu.Lock()
	fb.failPhone = "+97450000000"
	fb.mu.Unlock()
	var res struct{ ID int64 }
	c.do("POST", "/api/campaigns", map[string]any{"name": "Everyone", "template_slug": "collection_launch",
		"variables": map[string]any{"header_media_url": "https://cdn.example.com/w.jpg", "body": []string{"{name}", "Winter", "20% off"}, "shop_url": "https://shop.example.com"},
		"audience":  map[string]any{"tags": []string{"vip"}}}, &res)
	c.do("POST", fmt.Sprintf("/api/campaigns/%d/send", res.ID), map[string]string{}, nil)
	waitFor(t, "big campaign sending", 10*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=$1 AND bird_message_id IS NOT NULL`, res.ID).Scan(&n)
		return n > 5
	})
	if code := c.do("POST", fmt.Sprintf("/api/campaigns/%d/pause", res.ID), nil, nil); code != 200 {
		t.Fatal("pause failed")
	}
	time.Sleep(300 * time.Millisecond)
	var before, after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=$1 AND bird_message_id IS NOT NULL`, res.ID).Scan(&before)
	time.Sleep(700 * time.Millisecond)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=$1 AND bird_message_id IS NOT NULL`, res.ID).Scan(&after)
	if before != after {
		t.Fatalf("still sending while paused: %d -> %d", before, after)
	}
	var retry struct {
		Status   string
		Attempts int
	}
	_ = pool.QueryRow(ctx, `SELECT status, attempts FROM campaign_recipients WHERE campaign_id=$1 AND phone=$2`, res.ID, "+97450000000").Scan(&retry.Status, &retry.Attempts)
	if retry.Status != "pending" || retry.Attempts != 1 {
		t.Fatalf("temporary error not queued for retry: %+v", retry)
	}
	c.do("POST", fmt.Sprintf("/api/campaigns/%d/cancel", res.ID), nil, nil)
	var pendingLeft int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM campaign_recipients WHERE campaign_id=$1 AND status='pending'`, res.ID).Scan(&pendingLeft)
	if pendingLeft != 0 {
		t.Fatalf("cancel left %d pending", pendingLeft)
	}

	// Staff cannot reach settings; invalid webhook signatures are refused.
	staffHash, _ := auth.HashPassword("staff-password-1")
	_, _ = pool.Exec(ctx, `INSERT INTO users (email, name, role, password_hash) VALUES ('staff@example.com','Staff','staff',$1)`, staffHash)
	jar2, _ := cookiejar.New(nil)
	staff := &client{t: t, base: apiSrv.URL, http: &http.Client{Jar: jar2}}
	staff.do("POST", "/api/auth/login", map[string]string{"email": "staff@example.com", "password": "staff-password-1"}, nil)
	if code := staff.do("GET", "/api/settings", nil, nil); code != 403 {
		t.Fatalf("staff reached settings: %d", code)
	}
	if code := staff.do("GET", "/api/contacts?page_size=5", nil, nil); code != 200 {
		t.Fatalf("staff cannot list contacts: %d", code)
	}
	bad, _ := http.NewRequest("POST", apiSrv.URL+"/webhooks/bird", strings.NewReader(`{"type":"whatsapp.read"}`))
	bad.Header.Set("webhook-id", "x")
	bad.Header.Set("webhook-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	bad.Header.Set("webhook-signature", "v1,AAAA")
	if resp, _ := http.DefaultClient.Do(bad); resp.StatusCode != 401 {
		t.Fatalf("bad signature accepted: %d", resp.StatusCode)
	}

	cancel()
	<-done
}
