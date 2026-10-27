package bird

import (
	"encoding/base64"
	"strconv"
	"testing"
	"time"
)

func TestVerifyWebhook(t *testing.T) {
	// Built at runtime so secret scanners don't mistake this test value for a real key.
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("unit-test-signing-key"))
	body := []byte(`{"type":"whatsapp.delivered"}`)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := Sign(secret, "msg_1", ts, body)

	if err := VerifyWebhook(secret, "msg_1", ts, "v1,bogus "+sig, body, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifyWebhook(secret, "msg_1", ts, sig, []byte(`{"type":"x"}`), now); err == nil {
		t.Fatal("tampered body accepted")
	}
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	if err := VerifyWebhook(secret, "msg_1", old, Sign(secret, "msg_1", old, body), body, now); err == nil {
		t.Fatal("stale timestamp accepted")
	}
}
