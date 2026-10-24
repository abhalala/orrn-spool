package scale

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"
)

// Simulator produces the frame stream of a generic ASCII indicator for tests
// and demos: an empty platform near zero, a bundle placed (overshoot and a
// damped swing while it settles), a steady hold with small noise, a nudge that
// creeps the weight by ~45 g and settles again, then the bundle lifted off.
// The output is deterministic for a given Seed.
//
// Frames look like "ST,GS,+  12.345kg" (indicator flag US while moving), the
// format DefaultFramePattern parses.
type Simulator struct {
	// Seed picks the bundle weights; the same seed replays the same stream.
	Seed int64
	// Interval between frames in simulated time. Default 100 ms (10 Hz).
	Interval time.Duration
	// Speed multiplies simulated time against wall time in Run. Default 1.
	Speed float64
	// DivisionGrams is the indicator's display step. Default 5 g.
	DivisionGrams int64
}

// Phase lengths of one cycle (simulated time).
const (
	simEmpty  = 3 * time.Second
	simRamp   = 600 * time.Millisecond
	simSettle = 1800 * time.Millisecond
	simHold   = 4 * time.Second
	simNudge  = 800 * time.Millisecond
	simHold2  = 3 * time.Second
	simLift   = 500 * time.Millisecond
	// SimCycle is the length of one load/unload cycle.
	SimCycle = simEmpty + simRamp + simSettle + simHold + simNudge + simHold2 + simLift
)

// SimPhase names the part of the cycle a moment falls in (for tests).
type SimPhase string

const (
	PhaseEmpty  SimPhase = "empty"
	PhaseRamp   SimPhase = "ramp"
	PhaseSettle SimPhase = "settle"
	PhaseHold   SimPhase = "hold"
	PhaseNudge  SimPhase = "nudge"
	PhaseHold2  SimPhase = "hold2"
	PhaseLift   SimPhase = "lift"
)

func (s *Simulator) interval() time.Duration {
	if s.Interval <= 0 {
		return 100 * time.Millisecond
	}
	return s.Interval
}

func (s *Simulator) division() int64 {
	if s.DivisionGrams <= 0 {
		return 5
	}
	return s.DivisionGrams
}

// TargetGrams is the bundle weight placed in the given cycle (25-120 kg).
func (s *Simulator) TargetGrams(cycle int64) int64 {
	r := splitmix(uint64(s.Seed)*0x9E3779B97F4A7C15 + uint64(cycle))
	g := int64(25_000 + r%95_000)
	return g - g%s.division()
}

// PhaseAt reports the cycle number and phase at simulated time elapsed.
func (s *Simulator) PhaseAt(elapsed time.Duration) (int64, SimPhase, time.Duration) {
	cycle := int64(elapsed / SimCycle)
	t := elapsed % SimCycle
	bounds := []struct {
		d time.Duration
		p SimPhase
	}{
		{simEmpty, PhaseEmpty}, {simRamp, PhaseRamp}, {simSettle, PhaseSettle}, {simHold, PhaseHold},
		{simNudge, PhaseNudge}, {simHold2, PhaseHold2}, {simLift, PhaseLift},
	}
	for _, b := range bounds {
		if t < b.d {
			return cycle, b.p, t
		}
		t -= b.d
	}
	return cycle, PhaseLift, simLift
}

// GramsAt is the indicator's displayed weight at simulated time elapsed and
// whether the indicator itself would flag it steady.
func (s *Simulator) GramsAt(elapsed time.Duration) (int64, bool) {
	cycle, phase, t := s.PhaseAt(elapsed)
	target := float64(s.TargetGrams(cycle))
	frame := int64(elapsed / (10 * time.Millisecond))
	noise := float64(int64(splitmix(uint64(s.Seed)^uint64(frame)*0xBF58476D1CE4E5B9)%9) - 4) // -4..+4 g
	const nudge = 45.0
	var g float64
	steady := false
	switch phase {
	case PhaseEmpty:
		// Bare platform: mostly 0, sometimes one division below zero.
		g = 0
		if noise >= 4 {
			g = -float64(s.division())
		}
		steady = true
	case PhaseRamp:
		f := float64(t) / float64(simRamp)
		g = target * f * 1.08 // overshoots as the bundle lands
	case PhaseSettle:
		f := float64(t) / float64(simSettle)
		amp := 0.08 * target * math.Exp(-4.5*f)
		g = target + amp*math.Cos(2*math.Pi*3*f) + noise
		steady = f > 0.85
	case PhaseHold:
		g = target + noise
		steady = true
	case PhaseNudge:
		f := float64(t) / float64(simNudge)
		g = target + nudge*f + noise
	case PhaseHold2:
		g = target + nudge + noise
		steady = true
	case PhaseLift:
		f := float64(t) / float64(simLift)
		g = (target + nudge) * (1 - f)
	}
	div := s.division()
	rounded := int64(math.Round(g/float64(div))) * div
	return rounded, steady
}

// FrameAt is the ASCII frame (without line ending) at simulated time elapsed.
func (s *Simulator) FrameAt(elapsed time.Duration) string {
	g, steady := s.GramsAt(elapsed)
	return FormatFrame(g, steady)
}

// FormatFrame renders grams the way a generic indicator does:
// "ST,GS,+  12.345kg" / "US,GS,-   0.020kg".
func FormatFrame(grams int64, steady bool) string {
	flag := "US"
	if steady {
		flag = "ST"
	}
	sign := "+"
	if grams < 0 {
		sign = "-"
		grams = -grams
	}
	return fmt.Sprintf("%s,GS,%s%4d.%03dkg", flag, sign, grams/1000, grams%1000)
}

// Run writes frames to w, CR LF terminated, until ctx ends or a write fails.
func (s *Simulator) Run(ctx context.Context, w io.Writer) error {
	speed := s.Speed
	if speed <= 0 {
		speed = 1
	}
	step := s.interval()
	wall := time.Duration(float64(step) / speed)
	if wall < time.Millisecond {
		wall = time.Millisecond
	}
	ticker := time.NewTicker(wall)
	defer ticker.Stop()
	var elapsed time.Duration
	for {
		if _, err := io.WriteString(w, s.FrameAt(elapsed)+"\r\n"); err != nil {
			return err
		}
		elapsed += step
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func splitmix(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
