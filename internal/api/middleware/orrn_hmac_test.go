package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type vectorFile struct {
	MaxSkewMs int64 `json:"maxSkewMs"`
	Vectors   []struct {
		Name        string  `json:"name"`
		Secret      string  `json:"secret"`
		Method      string  `json:"method"`
		Path        string  `json:"path"`
		Body        *string `json:"body"`
		TimestampMs int64   `json:"timestampMs"`
		Token       string  `json:"token"`
		VerifyAtMs  int64   `json:"verifyAtMs"`
		Valid       bool    `json:"valid"`
		Why         string  `json:"why"`
	} `json:"vectors"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/orrn_hmac_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vf vectorFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		t.Fatal(err)
	}
	if len(vf.Vectors) < 10 {
		t.Fatalf("expected the shared vector set, got %d vectors", len(vf.Vectors))
	}
	return vf
}

func fixedClock(ms int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(ms) }
}

// ORRN signed these tokens (signSpoolRequest); Go must verify them.
func TestORRNVectorsVerify(t *testing.T) {
	vf := loadVectors(t)
	if time.Duration(vf.MaxSkewMs)*time.Millisecond != ORRNMaxSkew {
		t.Fatalf("vector skew %d ms != ORRNMaxSkew %s", vf.MaxSkewMs, ORRNMaxSkew)
	}
	for _, v := range vf.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			var body []byte
			if v.Body != nil {
				body = []byte(*v.Body)
			}
			err := NewORRNVerifier(v.Secret).WithClock(fixedClock(v.VerifyAtMs)).Verify(v.Method, v.Path, body, v.Token)
			if v.Valid && err != nil {
				t.Fatalf("%s: want valid, got %v", v.Why, err)
			}
			if !v.Valid && err == nil {
				t.Fatalf("%s: want refused, got valid", v.Why)
			}
		})
	}
}

// Go must produce exactly ORRN's token, so ORRN can verify what Go signs.
func TestORRNVectorsSign(t *testing.T) {
	for _, v := range loadVectors(t).Vectors {
		if !v.Valid {
			continue
		}
		var body []byte
		if v.Body != nil {
			body = []byte(*v.Body)
		}
		if got := SignORRN(v.Secret, v.Method, v.Path, body, time.UnixMilli(v.TimestampMs)); got != v.Token {
			t.Errorf("%s: Go signed %s, ORRN signed %s", v.Name, got, v.Token)
		}
	}
}

func TestORRNReplayRefused(t *testing.T) {
	now := time.UnixMilli(1792823400123)
	v := NewORRNVerifier("k").WithClock(func() time.Time { return now })
	tok := SignORRN("k", "GET", "/scale/reading", nil, now)
	if err := v.Verify("GET", "/scale/reading", nil, tok); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify("GET", "/scale/reading", nil, tok); !errors.Is(err, ErrORRNReplay) {
		t.Fatalf("second use: want replay error, got %v", err)
	}
	// A fresh token one millisecond later is fine.
	tok2 := SignORRN("k", "GET", "/scale/reading", nil, now.Add(time.Millisecond))
	if err := v.Verify("GET", "/scale/reading", nil, tok2); err != nil {
		t.Fatal(err)
	}
	// Remembered tokens are forgotten once they are outside the window anyway.
	now = now.Add(ORRNMaxSkew + time.Second)
	_ = v.Verify("GET", "/x", nil, SignORRN("k", "GET", "/x", nil, now))
	if len(v.seen) != 1 {
		t.Fatalf("expected expired entries swept, have %d", len(v.seen))
	}
}

func TestNewORRNVerifierEmptySecretIsOff(t *testing.T) {
	if NewORRNVerifier("  ") != nil {
		t.Fatal("empty secret must disable ORRN auth")
	}
}

func newTestRouter(a *AuthMiddleware) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/")
	g.Use(a.RequireAuth())
	g.GET("/scale/reading", func(c *gin.Context) {
		c.JSON(200, gin.H{"scheme": c.GetString("auth_scheme")})
	})
	g.POST("/api/jobs", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(200, string(body))
	})
	return r
}

func TestRequireAuthAcceptsORRNTokenAndJWT(t *testing.T) {
	a := &AuthMiddleware{secret: []byte("jwt-secret-for-tests")}
	a.SetORRNVerifier(NewORRNVerifier("node-secret"))
	r := newTestRouter(a)

	// ORRN machine token.
	req := httptest.NewRequest(http.MethodGet, "/scale/reading", nil)
	req.Header.Set("Authorization", "Bearer "+SignORRN("node-secret", "GET", "/scale/reading", nil, time.Now()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("orrn-hmac")) {
		t.Fatalf("hmac: %d %s", w.Code, w.Body.String())
	}

	// The body survives verification for the handler.
	body := []byte(`{"printer_id":1}`)
	req = httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+SignORRN("node-secret", "POST", "/api/jobs", body, time.Now()))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != string(body) {
		t.Fatalf("post: %d %q", w.Code, w.Body.String())
	}

	// The spool's own JWT still works.
	jwtToken, err := a.generateToken()
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/scale/reading", nil)
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("jwt: %d %s", w.Code, w.Body.String())
	}
}

func TestRequireAuthRefusesBadORRNTokens(t *testing.T) {
	a := &AuthMiddleware{secret: []byte("jwt-secret-for-tests")}
	a.SetORRNVerifier(NewORRNVerifier("node-secret"))
	r := newTestRouter(a)

	cases := map[string]string{
		"wrong secret": SignORRN("other", "GET", "/scale/reading", nil, time.Now()),
		"wrong path":   SignORRN("node-secret", "GET", "/api/jobs", nil, time.Now()),
		"old":          SignORRN("node-secret", "GET", "/scale/reading", nil, time.Now().Add(-6*time.Minute)),
		"garbage":      "v1:zz:nothex",
	}
	for name, tok := range cases {
		req := httptest.NewRequest(http.MethodGet, "/scale/reading", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", name, w.Code)
		}
	}

	// Replay of a good token over HTTP.
	tok := SignORRN("node-secret", "GET", "/scale/reading", nil, time.Now())
	for i, want := range []int{200, 401} {
		req := httptest.NewRequest(http.MethodGet, "/scale/reading", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("use %d: want %d, got %d", i+1, want, w.Code)
		}
	}
}

func TestRequireAuthWithoutSecretRefusesORRNTokens(t *testing.T) {
	a := &AuthMiddleware{secret: []byte("jwt-secret-for-tests")}
	r := newTestRouter(a)
	req := httptest.NewRequest(http.MethodGet, "/scale/reading", nil)
	req.Header.Set("Authorization", "Bearer "+SignORRN("node-secret", "GET", "/scale/reading", nil, time.Now()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when no shared secret is configured, got %d", w.Code)
	}
}
