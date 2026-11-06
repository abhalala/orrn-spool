package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"

	apimw "github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/db"
)

// ---- Finding 4: first-boot setup and cookie/CSRF ----

func clearPassword(t *testing.T) {
	t.Helper()
	openTestDB(t)
	if err := db.Settings.DeleteSetting(context.Background(), "admin_password"); err != nil {
		t.Fatal(err)
	}
}

func TestFirstBootSetupNeedsLocalToken(t *testing.T) {
	clearPassword(t)
	defer ensurePassword(t)
	s := newTestSpool(t)

	// The JSON setup call from whoever reaches the listener first.
	body, _ := json.Marshal(map[string]string{"password": "attacker-pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if w := s.do(req); w.Code != http.StatusForbidden {
		t.Errorf("setup without the local token: %d %s, want 403", w.Code, w.Body.String())
	}
	// The login form's first-time path.
	form := url.Values{"password": {"attacker-pass"}, "confirm_password": {"attacker-pass"}}
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := s.do(req); w.Code == http.StatusSeeOther {
		t.Errorf("form setup without the local token signed in (303 -> %q)", w.Header().Get("Location"))
	}
	if got := setting(t, "admin_password"); got != "" {
		t.Fatal("an unauthenticated visitor set the first admin password")
	}
}

func TestSessionCookieIsSameSiteLax(t *testing.T) {
	s := newTestSpool(t)
	c := s.session(t)
	if c.SameSite != http.SameSiteLaxMode && c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie SameSite = %v, want Lax or Strict", c.SameSite)
	}
	if !c.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
}

func TestCrossSiteMutationRefused(t *testing.T) {
	s := newTestSpool(t)
	cookie := s.session(t)
	path := "/api/jobs/999999/pause"
	cases := []struct {
		name    string
		headers map[string]string
		refused bool
	}{
		{"fetch metadata cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://evil.example"}, true},
		{"fetch metadata same-site (other port on the plant host)", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://spool.test:3000"}, true},
		{"origin mismatch, no fetch metadata", map[string]string{"Origin": "http://evil.example"}, true},
		{"null origin", map[string]string{"Origin": "null"}, true},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://" + testHost}, false},
		{"origin matches, older browser", map[string]string{"Origin": "http://" + testHost}, false},
		{"user typed or bookmarked", map[string]string{"Sec-Fetch-Site": "none"}, false},
		{"no browser headers (curl)", nil, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		w := s.do(req)
		if tc.refused && w.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", tc.name, w.Code)
		}
		if !tc.refused && w.Code == http.StatusForbidden {
			t.Errorf("%s: refused with 403, want it to reach the handler", tc.name)
		}
	}

	// Login CSRF and logout CSRF go through the same check.
	form := url.Values{"password": {testAdminPassword}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "http://evil.example")
	if w := s.do(req); w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST /login: %d, want 403", w.Code)
	}
}

// The token the spool announced at start sets the first password exactly
// once, even when several requests race with it.
func TestFirstBootSetupWithTokenIsSingleUse(t *testing.T) {
	clearPassword(t)
	defer ensurePassword(t)
	s := newTestSpool(t)
	token := s.auth.SetupToken()
	if len(token) < 32 {
		t.Fatalf("setup token %q is too short", token)
	}
	var completed int32
	s.auth.OnSetupComplete(func() { atomic.AddInt32(&completed, 1) })

	setup := func(password, tok string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"password": password, "setup_token": tok})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return s.do(req)
	}
	if w := setup("attacker-pass", token[:len(token)-1]+"x"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d, want 403", w.Code)
	}

	const racers = 8
	codes := make([]int, racers)
	cookies := make([]bool, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := setup(fmt.Sprintf("racer-pass-%d", i), token)
			codes[i] = w.Code
			for _, c := range w.Result().Cookies() {
				if c.Name == "spool_auth" && c.Value != "" {
					cookies[i] = true
				}
			}
		}(i)
	}
	wg.Wait()
	winner := -1
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			if winner != -1 {
				t.Fatalf("two setup requests succeeded: %d and %d", winner, i)
			}
			winner = i
		case http.StatusBadRequest:
			if cookies[i] {
				t.Errorf("losing setup request %d got a session cookie", i)
			}
		default:
			t.Errorf("setup request %d: %d", i, code)
		}
	}
	if winner == -1 || !cookies[winner] {
		t.Fatalf("no setup request won with a session (codes %v)", codes)
	}
	hash := setting(t, "admin_password")
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(fmt.Sprintf("racer-pass-%d", winner))) != nil {
		t.Fatal("stored password is not the winner's")
	}
	if s.auth.SetupToken() != "" {
		t.Fatal("setup token still valid after setup")
	}
	if n := atomic.LoadInt32(&completed); n != 1 {
		t.Fatalf("OnSetupComplete ran %d times, want 1", n)
	}
	if w := setup("late-pass", token); w.Code != http.StatusBadRequest {
		t.Fatalf("reused token after setup: %d, want 400", w.Code)
	}
}

func TestFirstBootFormSetupWithToken(t *testing.T) {
	clearPassword(t)
	defer ensurePassword(t)
	s := newTestSpool(t)
	form := url.Values{"password": {"owner-pass"}, "confirm_password": {"owner-pass"}, "setup_token": {s.auth.SetupToken()}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://"+testHost)
	w := s.do(req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("form setup with the token: %d -> %q", w.Code, w.Header().Get("Location"))
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "spool_auth" {
			session = c
		}
	}
	if session == nil || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("form setup cookie: %+v, want SameSite=Lax", session)
	}
}

// main announces the token in a file only the spool's user can read and
// removes the file when setup completes.
func TestAnnounceSetupTokenFile(t *testing.T) {
	clearPassword(t)
	defer ensurePassword(t)
	s := newTestSpool(t)
	path := filepath.Join(t.TempDir(), apimw.SetupTokenFile)
	announceSetupToken(s.auth, s.auth.SetupToken(), path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != s.auth.SetupToken() {
		t.Fatal("token file does not hold the setup token")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("token file mode %o, want 600", perm)
		}
	}
	body, _ := json.Marshal(map[string]string{"password": testAdminPassword, "setup_token": strings.TrimSpace(string(raw))})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if w := s.do(req); w.Code != http.StatusOK {
		t.Fatalf("setup with the file's token: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still present after setup: %v", err)
	}
}
