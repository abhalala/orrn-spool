package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/orrn/spool/internal/db"
)

// ORRN verifies the signature over the exact HTTP body, not only its data field.
func TestORRNWebhookAuthenticatesWholeEnvelope(t *testing.T) {
	const secret = "isolated-contract-test-secret"
	var body []byte
	var signature string
	var subdomain string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		signature = r.Header.Get("X-Webhook-Signature")
		subdomain = r.Header.Get("X-Spool-Subdomain")
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	sender := NewWebhookSender(nil, WebhookConfig{Subdomain: "fourcubes"})
	payload := &WebhookPayload{Event: "job_completed", Timestamp: time.Unix(1700000000, 0).UTC(), Data: &JobEventData{JobID: 42, Status: "completed"}}
	if err := sender.sendRequest(&db.Webhook{URL: receiver.URL, Secret: secret}, payload); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if signature != expected {
		t.Fatalf("ORRN rejects callback: header signs a different body; got %s, want %s", signature, expected)
	}
	if subdomain != "fourcubes" {
		t.Fatalf("ORRN node identity = %q, want fourcubes", subdomain)
	}
	firstBody, firstSignature := string(body), signature
	if err := sender.sendRequest(&db.Webhook{URL: receiver.URL, Secret: secret}, payload); err != nil {
		t.Fatal(err)
	}
	if string(body) != firstBody || signature != firstSignature {
		t.Fatal("repeated delivery mutated the payload or signature")
	}
	sender = NewWebhookSender(nil, WebhookConfig{})
	if err := sender.sendRequest(&db.Webhook{URL: receiver.URL}, payload); err != nil {
		t.Fatal(err)
	}
	if signature != "" || subdomain != "" {
		t.Fatal("generic unsigned callback acquired a signature or node identity")
	}
}
