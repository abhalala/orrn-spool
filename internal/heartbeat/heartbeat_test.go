package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orrn/spool/internal/api/middleware"
)

func TestBeatIsSignedAndCarriesScalePresence(t *testing.T) {
	lastFrame := time.Date(2026, 10, 24, 6, 29, 59, 900_000_000, time.UTC)
	var got Payload
	var verifyErr error
	var subdomain string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		verifyErr = middleware.NewORRNVerifier("node-secret").Verify(r.Method, r.URL.RequestURI(), body, token)
		subdomain = r.Header.Get("X-Spool-Subdomain")
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(Config{
		ServerURL:    srv.URL + "/",
		InstanceID:   "8b0c7a52-4c9e-4e7e-9a55-1d2f3e4a5b6c",
		Subdomain:    "fourcubes",
		SharedSecret: "node-secret",
		Version:      "1.4.0",
		Scale: func() ScalePresence {
			return ScalePresence{Configured: true, Connected: true, LastFrameAt: &lastFrame}
		},
	})
	if err := s.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if verifyErr != nil {
		t.Fatalf("ORRN-side verification failed: %v", verifyErr)
	}
	if subdomain != "fourcubes" || got.InstanceID != "8b0c7a52-4c9e-4e7e-9a55-1d2f3e4a5b6c" || got.Version != "1.4.0" {
		t.Fatalf("payload %+v subdomain %q", got, subdomain)
	}
	if !got.Scale.Configured || got.Scale.LastFrameAt == nil || !got.Scale.LastFrameAt.Equal(lastFrame) {
		t.Fatalf("scale presence %+v", got.Scale)
	}
}

func TestBeatWithoutScaleSaysNotConfigured(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
	}))
	defer srv.Close()
	if err := New(Config{ServerURL: srv.URL, InstanceID: "i", SharedSecret: "k"}).Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	sc, ok := raw["scale"].(map[string]any)
	if !ok || sc["configured"] != false || sc["lastFrameAt"] != nil {
		t.Fatalf("scale block %v", raw["scale"])
	}
}

func TestBeatReportsORRNRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if err := New(Config{ServerURL: srv.URL, InstanceID: "i", SharedSecret: "k"}).Beat(context.Background()); err == nil {
		t.Fatal("a 403 must be an error")
	}
}

func TestEnabledNeedsURLInstanceAndSecret(t *testing.T) {
	if (Config{ServerURL: "https://x", InstanceID: "i"}).Enabled() {
		t.Fatal("no secret: must be off")
	}
	if !(Config{ServerURL: "https://x", InstanceID: "i", SharedSecret: "k"}).Enabled() {
		t.Fatal("should be on")
	}
}
