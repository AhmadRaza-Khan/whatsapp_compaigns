package bird

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// VerifyWebhook checks a Standard Webhooks signature as used by Bird:
// HMAC-SHA256 over "{webhook-id}.{webhook-timestamp}.{raw body}", keyed with
// the base64-decoded secret after the "whsec_" prefix. The header may carry
// several space-separated "v1,<sig>" values (during secret rotation).
func VerifyWebhook(secret, id, timestamp, signatures string, body []byte, now time.Time) error {
	if id == "" || timestamp == "" || signatures == "" {
		return errors.New("missing webhook signature headers")
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid webhook timestamp")
	}
	if d := now.Sub(time.Unix(ts, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("webhook timestamp outside tolerance")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return errors.New("invalid webhook secret format")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	expected := mac.Sum(nil)

	for _, s := range strings.Fields(signatures) {
		version, sig, ok := strings.Cut(s, ",")
		if !ok || version != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(sig)
		if err == nil && hmac.Equal(got, expected) {
			return nil
		}
	}
	return errors.New("webhook signature mismatch")
}

// Sign produces a signature header value; used by tests and the local
// webhook simulator.
func Sign(secret, id, timestamp string, body []byte) string {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
