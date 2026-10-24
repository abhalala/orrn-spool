package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/api/middleware"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/scale"
)

const nodeSecret = "integration-node-secret"

type readingJSON struct {
	Grams      *int64 `json:"grams"`
	Stable     bool   `json:"stable"`
	AgeMs      *int64 `json:"ageMs"`
	Frames     int    `json:"frames"`
	Reason     string `json:"reason"`
	Configured bool   `json:"configured"`
	Simulated  bool   `json:"simulated"`
}

var (
	dbOnce sync.Once
	dbErr  error
)

// router builds the real wiring from main.go: RequireAuth (JWT or ORRN
// HMAC) in front of the scale routes.
func router(t *testing.T, svc ScaleReader, verifierNow func() time.Time) *gin.Engine {
	t.Helper()
	dbOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spool-scale-test")
		if err != nil {
			dbErr = err
			return
		}
		dbErr = db.Init(db.Config{Path: filepath.Join(dir, "spool.db")})
	})
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	auth, err := middleware.NewAuthMiddleware(db.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	auth.SetORRNVerifier(middleware.NewORRNVerifier(nodeSecret).WithClock(verifierNow))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/")
	g.Use(auth.RequireAuth())
	NewScaleHandler(svc).RegisterRoutes(g)
	return r
}

type signer struct {
	mu   sync.Mutex
	base time.Time
	n    int
}

// get signs like ORRN's SpoolClient, one fresh millisecond per request.
func (s *signer) get(t *testing.T, r http.Handler, path string) (int, readingJSON) {
	t.Helper()
	s.mu.Lock()
	s.n++
	at := s.base.Add(time.Duration(s.n) * time.Millisecond)
	s.mu.Unlock()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+middleware.SignORRN(nodeSecret, "GET", path, nil, at))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out readingJSON
	if w.Code == http.StatusOK && path == "/scale/reading" {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, out
}

func TestScaleReadingRequiresAuth(t *testing.T) {
	r := router(t, (*scale.Service)(nil), time.Now)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/scale/reading", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without a token, got %d", w.Code)
	}
}

func TestScaleReadingWithoutScaleSaysNotConfigured(t *testing.T) {
	base := time.Now()
	r := router(t, (*scale.Service)(nil), func() time.Time { return base })
	s := &signer{base: base}
	code, got := s.get(t, r, "/scale/reading")
	if code != 200 || got.Stable || got.Configured || got.Reason != scale.ReasonNotConfigured || got.Grams != nil {
		t.Fatalf("%d %+v", code, got)
	}
}

// Simulator -> reader -> detector -> HTTP, in simulated time: the frames go
// through a real pipe and the line scanner, the clock is advanced per frame.
func TestSimulatorEndToEndInSimulatedTime(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 6, 30, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := t0
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }

	pr, pw := io.Pipe()
	framed := make(chan scale.Frame, 1)
	svc, err := scale.NewService(scale.Config{Device: "sim-pipe"},
		scale.WithClock(now),
		scale.WithOpener(func(context.Context) (io.ReadCloser, error) { return pr, nil }),
		scale.WithFrameHook(func(f scale.Frame) { framed <- f }),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	verifierBase := time.Now()
	r := router(t, svc, func() time.Time { return verifierBase })
	s := &signer{base: verifierBase}

	sim := &scale.Simulator{Seed: 11}
	target := sim.TargetGrams(0)
	step := 100 * time.Millisecond
	var stableSeen, zeroSeen, settlingSeen bool
	for el := time.Duration(0); el < scale.SimCycle; el += step {
		mu.Lock()
		clock = t0.Add(el)
		mu.Unlock()
		if _, err := io.WriteString(pw, sim.FrameAt(el)+"\r\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-framed:
		case <-time.After(2 * time.Second):
			t.Fatalf("reader did not take the frame at %s", el)
		}
		code, got := s.get(t, r, "/scale/reading")
		if code != 200 {
			t.Fatalf("%s: HTTP %d", el, code)
		}
		_, phase, inPhase := sim.PhaseAt(el)
		switch {
		case phase == scale.PhaseEmpty && got.Reason == scale.ReasonZero:
			zeroSeen = true
		case phase == scale.PhaseSettle:
			if got.Stable {
				t.Fatalf("%s settling reported stable: %+v", el, got)
			}
			settlingSeen = true
		case phase == scale.PhaseHold && inPhase >= 1100*time.Millisecond:
			if !got.Stable || got.Grams == nil || *got.Grams-target > 10 || target-*got.Grams > 10 || got.Frames < 5 {
				t.Fatalf("%s hold: want stable near %d, got %+v", el, target, got)
			}
			stableSeen = true
		}
	}
	if !stableSeen || !zeroSeen || !settlingSeen {
		t.Fatalf("stable=%v zero=%v settling=%v", stableSeen, zeroSeen, settlingSeen)
	}

	// No frames for more than 3 s: the reading goes stale with no grams.
	mu.Lock()
	clock = clock.Add(3*time.Second + 50*time.Millisecond)
	mu.Unlock()
	_, got := s.get(t, r, "/scale/reading")
	if got.Stable || got.Grams != nil || got.Reason != scale.ReasonStale {
		t.Fatalf("stale: %+v", got)
	}

	st := svc.Status()
	if !st.Configured || !st.Connected || st.LastFrameAt == nil || st.Frames == 0 {
		t.Fatalf("status: %+v", st)
	}
	_ = pw.Close()
}

// The real --scale-sim path (default opener, wall clock), sped up 2x.
func TestScaleSimModeServesAStableReading(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock test")
	}
	svc, err := scale.NewService(scale.Config{Simulate: true, SimulateSpeed: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	base := time.Now()
	r := router(t, svc, func() time.Time { return base })
	s := &signer{base: base}
	deadline := time.Now().Add(scale.SimCycle) // half a cycle at 2x, plus margin
	for time.Now().Before(deadline) {
		_, got := s.get(t, r, "/scale/reading")
		if got.Stable {
			if !got.Simulated || got.Grams == nil || *got.Grams < 25_000 || got.Frames < 5 {
				t.Fatalf("bad stable reading %+v", got)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("simulator never produced a stable reading")
}
