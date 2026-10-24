package scale

import (
	"sync"
	"time"
)

// The stability rule (ORRN spec, pack module, Q-PK-3). Do not tune these
// without changing the spec.
const (
	// StableFrames is the minimum number of consecutive frames in the band.
	StableFrames = 5
	// StableBandGrams is the widest spread (max - min) the run may have.
	StableBandGrams = 20
	// StableHold is how long the run must have held without leaving the band.
	StableHold = time.Second
	// StaleAfter: when the newest frame is older than this, there is no reading.
	StaleAfter = 3 * time.Second
	// historyLimit bounds memory; at 50 frames/s it still covers the hold.
	historyLimit = 256
)

// Reason codes explain why a reading is not stable. They are stable strings
// ORRN may show or log.
const (
	ReasonNotConfigured = "not_configured"
	ReasonNoFrames      = "no_frames"
	ReasonStale         = "stale"
	ReasonSettling      = "settling"
	ReasonZero          = "zero"
)

// Reading is the JSON shape served at GET /scale/reading.
//
// A stale or empty reading is {"stable": false, ...} with no grams. A fresh
// but moving reading carries grams so a screen can show the live number, with
// stable false. A stable zero (or negative) weight is never a stable weight.
type Reading struct {
	Grams      *int64 `json:"grams,omitempty"`
	Stable     bool   `json:"stable"`
	AgeMs      *int64 `json:"ageMs,omitempty"`
	Frames     int    `json:"frames"`
	Reason     string `json:"reason,omitempty"`
	Configured bool   `json:"configured"`
	Simulated  bool   `json:"simulated,omitempty"`
}

type sample struct {
	at    time.Time
	grams int64
}

// Detector keeps a short frame history and applies the stability rule.
// It is safe for concurrent use.
type Detector struct {
	mu      sync.Mutex
	history []sample
}

// NewDetector returns an empty detector.
func NewDetector() *Detector {
	return &Detector{history: make([]sample, 0, historyLimit)}
}

// Add records a frame received at time at.
func (d *Detector) Add(at time.Time, grams int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := len(d.history); n > 0 && at.Before(d.history[n-1].at) {
		// A clock step backwards: start again rather than mix timelines.
		d.history = d.history[:0]
	}
	if len(d.history) == historyLimit {
		copy(d.history, d.history[1:])
		d.history = d.history[:historyLimit-1]
	}
	d.history = append(d.history, sample{at: at, grams: grams})
}

// LastFrameAt is the time of the newest frame, or the zero time.
func (d *Detector) LastFrameAt() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.history) == 0 {
		return time.Time{}
	}
	return d.history[len(d.history)-1].at
}

// Reset forgets every frame (used when the device disconnects).
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history = d.history[:0]
}

// Reading evaluates the rule at time now:
//
//  1. No frames, or the newest frame older than StaleAfter: {stable:false}.
//  2. Walk back from the newest frame while every frame stays within
//     StableBandGrams of the others (max - min <= 20 g). That is the run.
//  3. Stable when the run has at least StableFrames frames AND spans at least
//     StableHold (newest frame time - oldest frame time in the run).
//  4. A stable reading of 0 g or less is reported with stable false.
//
// The weight reported is the newest frame, which is what the indicator shows.
func (d *Detector) Reading(now time.Time) Reading {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := len(d.history)
	if n == 0 {
		return Reading{Stable: false, Reason: ReasonNoFrames, Configured: true}
	}
	newest := d.history[n-1]
	age := now.Sub(newest.at)
	if age < 0 {
		age = 0
	}
	ageMs := age.Milliseconds()
	if age > StaleAfter {
		return Reading{Stable: false, AgeMs: &ageMs, Reason: ReasonStale, Configured: true}
	}

	lo, hi := newest.grams, newest.grams
	runStart := newest.at
	count := 1
	for i := n - 2; i >= 0; i-- {
		g := d.history[i].grams
		nlo, nhi := min64(lo, g), max64(hi, g)
		if nhi-nlo > StableBandGrams {
			break
		}
		lo, hi = nlo, nhi
		runStart = d.history[i].at
		count++
	}

	grams := newest.grams
	r := Reading{Grams: &grams, AgeMs: &ageMs, Frames: count, Configured: true}
	stable := count >= StableFrames && newest.at.Sub(runStart) >= StableHold
	switch {
	case !stable:
		r.Reason = ReasonSettling
	case grams <= 0:
		r.Reason = ReasonZero
	default:
		r.Stable = true
	}
	return r
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
