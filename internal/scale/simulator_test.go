package scale

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestSimulatorFramesParseWithDefaultPattern(t *testing.T) {
	p, _ := NewParser("")
	sim := &Simulator{Seed: 7}
	for el := time.Duration(0); el < 2*SimCycle; el += 100 * time.Millisecond {
		line := sim.FrameAt(el)
		f, err := p.Parse(line)
		if err != nil {
			t.Fatalf("%s: %q does not parse: %v", el, line, err)
		}
		want, _ := sim.GramsAt(el)
		if f.Grams != want {
			t.Fatalf("%s: parsed %d, simulator says %d", el, f.Grams, want)
		}
	}
}

func TestSimulatorIsDeterministicAndRealistic(t *testing.T) {
	a, b := &Simulator{Seed: 42}, &Simulator{Seed: 42}
	for el := time.Duration(0); el < SimCycle; el += 50 * time.Millisecond {
		if a.FrameAt(el) != b.FrameAt(el) {
			t.Fatalf("same seed differs at %s", el)
		}
	}
	for c := int64(0); c < 50; c++ {
		g := a.TargetGrams(c)
		if g < 25_000 || g >= 120_000 || g%5 != 0 {
			t.Fatalf("cycle %d target %d g out of range or off the 5 g division", c, g)
		}
	}
	if (&Simulator{Seed: 1}).TargetGrams(0) == (&Simulator{Seed: 2}).TargetGrams(0) {
		t.Fatal("different seeds should place different bundles")
	}
}

// Drive the detector with the simulator in simulated time and check each
// phase gives the reading the spec asks for.
func TestSimulatorThroughDetectorPhases(t *testing.T) {
	sim := &Simulator{Seed: 3}
	d := NewDetector()
	step := 100 * time.Millisecond
	target := sim.TargetGrams(0)
	sawStableTarget, sawStableNudged, sawZero := false, false, false
	for el := time.Duration(0); el < SimCycle; el += step {
		g, _ := sim.GramsAt(el)
		now := t0.Add(el)
		d.Add(now, g)
		r := d.Reading(now)
		_, phase, inPhase := sim.PhaseAt(el)
		switch phase {
		case PhaseEmpty:
			if r.Stable {
				t.Fatalf("%s empty platform reported stable %+v", el, r)
			}
			if r.Reason == ReasonZero {
				sawZero = true
			}
		case PhaseRamp, PhaseLift:
			// The first frame of a phase still shows the previous weight.
			if r.Stable && inPhase > 0 {
				t.Fatalf("%s %s must not be stable: %+v", el, phase, r)
			}
		case PhaseSettle:
			if r.Stable {
				t.Fatalf("%s settling must not be stable: %+v", el, r)
			}
		case PhaseHold:
			if inPhase >= 1100*time.Millisecond && !r.Stable {
				t.Fatalf("%s hold for %s should be stable: %+v", el, inPhase, r)
			}
			if r.Stable {
				if diff := *r.Grams - target; diff < -10 || diff > 10 {
					t.Fatalf("stable weight %d far from target %d", *r.Grams, target)
				}
				sawStableTarget = true
			}
		case PhaseHold2:
			if r.Stable && *r.Grams-target >= 35 {
				sawStableNudged = true
			}
		}
	}
	if !sawZero || !sawStableTarget || !sawStableNudged {
		t.Fatalf("phases missing: zero=%v stable=%v nudged=%v", sawZero, sawStableTarget, sawStableNudged)
	}
}

func TestSimulatorRunWritesLines(t *testing.T) {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	sim := &Simulator{Seed: 1, Interval: 100 * time.Millisecond, Speed: 100}
	go func() { _ = pw.CloseWithError(sim.Run(ctx, pw)) }()
	buf := make([]byte, 64)
	n, err := io.ReadAtLeast(pr, buf, 19)
	cancel()
	_ = pr.Close()
	if err != nil || string(buf[:n])[:6] != "ST,GS," {
		t.Fatalf("got %q %v", buf[:n], err)
	}
}
