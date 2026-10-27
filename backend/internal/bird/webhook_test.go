package bird

import (
	"strconv"
	"testing"
	"time"
)

func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
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
