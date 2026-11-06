package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckCrossOrigin(t *testing.T) {
	const host = "20.0.0.126:8080"
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		refused bool
	}{
		{"GET from anywhere", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"HEAD", http.MethodHead, map[string]string{"Origin": "http://evil.example"}, false},
		{"cross-site POST", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"same-site POST from another port", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://20.0.0.126:3000"}, true},
		{"cross-site DELETE", http.MethodDelete, map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"cross-site PUT", http.MethodPut, map[string]string{"Origin": "http://evil.example"}, true},
		{"origin on another port", http.MethodPost, map[string]string{"Origin": "http://20.0.0.126:3000"}, true},
		{"null origin", http.MethodPost, map[string]string{"Origin": "null"}, true},
		{"garbage origin", http.MethodPost, map[string]string{"Origin": "::not a url"}, true},
		{"unknown fetch-site value", http.MethodPost, map[string]string{"Sec-Fetch-Site": "weird"}, true},
		{"same-origin fetch", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://" + host}, false},
		{"typed URL", http.MethodPost, map[string]string{"Sec-Fetch-Site": "none"}, false},
		{"matching origin only", http.MethodPost, map[string]string{"Origin": "http://" + host}, false},
		{"no browser headers", http.MethodPost, nil, false},
		{"bearer token from a script", http.MethodPost, map[string]string{"Authorization": "Bearer v1:x:y", "Sec-Fetch-Site": "cross-site"}, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "/api/jobs", nil)
		req.Host = host
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		err := checkCrossOrigin(req)
		if tc.refused && err == nil {
			t.Errorf("%s: allowed, want refused", tc.name)
		}
		if !tc.refused && err != nil {
			t.Errorf("%s: refused, want allowed", tc.name)
		}
	}
}

func TestWriteSetupTokenFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), SetupTokenFile)
	// A leftover file with a wide mode must not keep that mode.
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetupTokenFile(path, "abc123"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "abc123" {
		t.Fatalf("file holds %q", raw)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode %o, want 600", perm)
	}
}

// A middleware built without a database has no setup token, and each
// token is 128 random bits in hex.
func TestSetupTokenShape(t *testing.T) {
	a := &AuthMiddleware{}
	if got := a.SetupToken(); got != "" {
		t.Fatalf("zero middleware has token %q", got)
	}
	token, err := newSetupToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 32 || strings.Trim(token, "0123456789abcdef") != "" {
		t.Fatalf("token %q is not 32 hex characters", token)
	}
	other, _ := newSetupToken()
	if other == token {
		t.Fatal("two setup tokens are equal")
	}
}
