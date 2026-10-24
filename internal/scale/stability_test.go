package scale

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 24, 6, 30, 0, 0, time.UTC)

// feed adds frames every step starting at start, returning the time of the last one.
func feed(d *Detector, start time.Time, step time.Duration, grams ...int64) time.Time {
	at := start
	for i, g := range grams {
		at = start.Add(time.Duration(i) * step)
		d.Add(at, g)
	}
	return at
}

func TestNoFramesIsNotStable(t *testing.T) {
	r := NewDetector().Reading(t0)
	if r.Stable || r.Grams != nil || r.Reason != ReasonNoFrames {
		t.Fatalf("%+v", r)
	}
}

func TestFiveFramesWithinBandOverOneSecondIsStable(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 250*time.Millisecond, 52400, 52410, 52395, 52405, 52415) // spread 20 g, span 1 s
	r := d.Reading(last)
	if !r.Stable || *r.Grams != 52415 || r.Frames != 5 {
		t.Fatalf("want stable 52415 over 5 frames, got %+v", r)
	}
}

func TestFourFramesAreNotEnoughEvenOverOneSecond(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 400*time.Millisecond, 52400, 52400, 52400, 52400) // 1.2 s, 4 frames
	r := d.Reading(last)
	if r.Stable || r.Reason != ReasonSettling || r.Frames != 4 {
		t.Fatalf("%+v", r)
	}
	d.Add(last.Add(400*time.Millisecond), 52400)
	if r := d.Reading(last.Add(400 * time.Millisecond)); !r.Stable {
		t.Fatalf("fifth frame should make it stable: %+v", r)
	}
}

func TestFiveFastFramesUnderOneSecondAreNotStable(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 100*time.Millisecond, 52400, 52400, 52400, 52400, 52400, 52400, 52400, 52400, 52400, 52400) // 0.9 s
	if r := d.Reading(last); r.Stable || r.Frames != 10 {
		t.Fatalf("0.9 s of frames must not be stable: %+v", r)
	}
	d.Add(last.Add(100*time.Millisecond), 52400) // now 1.0 s
	if r := d.Reading(last.Add(100 * time.Millisecond)); !r.Stable {
		t.Fatalf("1.0 s hold should be stable: %+v", r)
	}
}

func TestSpreadOver20GramsBreaksTheRun(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 250*time.Millisecond, 52400, 52410, 52421, 52410, 52405) // 52400 vs 52421 = 21 g
	r := d.Reading(last)
	if r.Stable {
		t.Fatalf("21 g spread must not be stable: %+v", r)
	}
	// The run that holds is the last four frames (52421..52405), not five.
	if r.Frames != 4 {
		t.Fatalf("run length %d, want 4", r.Frames)
	}
}

func TestMovementRestartsTheOneSecondHold(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 250*time.Millisecond, 50000, 50000, 50000, 50000, 50000, 50000)
	if !d.Reading(last).Stable {
		t.Fatal("should be stable before the nudge")
	}
	nudge := last.Add(250 * time.Millisecond)
	d.Add(nudge, 50060) // someone leans on the bundle
	if r := d.Reading(nudge); r.Stable || r.Frames != 1 {
		t.Fatalf("a 60 g move must restart the run: %+v", r)
	}
	at := nudge
	for i := 0; i < 4; i++ {
		at = at.Add(250 * time.Millisecond)
		d.Add(at, 50060)
	}
	if r := d.Reading(at); !r.Stable || *r.Grams != 50060 {
		t.Fatalf("five frames over 1 s after the move should be stable at 50060: %+v", r)
	}
}

func TestStaleAfterThreeSeconds(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 250*time.Millisecond, 52400, 52400, 52400, 52400, 52400)
	if r := d.Reading(last.Add(3 * time.Second)); !r.Stable {
		t.Fatalf("exactly 3 s old is still fresh: %+v", r)
	}
	r := d.Reading(last.Add(3*time.Second + time.Millisecond))
	if r.Stable || r.Grams != nil || r.Reason != ReasonStale || r.AgeMs == nil || *r.AgeMs != 3001 {
		t.Fatalf("older than 3 s must be {stable:false} with no grams: %+v", r)
	}
}

func TestStableZeroIsNotAWeight(t *testing.T) {
	d := NewDetector()
	last := feed(d, t0, 250*time.Millisecond, 0, 0, 0, 0, 0, 0)
	r := d.Reading(last)
	if r.Stable || r.Reason != ReasonZero || r.Grams == nil || *r.Grams != 0 {
		t.Fatalf("a steady 0 must report stable false, reason zero: %+v", r)
	}
	// Negative zero drift is not a weight either.
	d.Reset()
	last = feed(d, t0, 250*time.Millisecond, -5, -5, 0, -5, -5)
	if r := d.Reading(last); r.Stable || r.Reason != ReasonZero {
		t.Fatalf("negative steady reading must not be stable: %+v", r)
	}
}

func TestClockStepBackwardsStartsOver(t *testing.T) {
	d := NewDetector()
	feed(d, t0, 250*time.Millisecond, 1000, 1000, 1000, 1000, 1000)
	d.Add(t0.Add(-time.Minute), 1000)
	if r := d.Reading(t0.Add(-time.Minute)); r.Frames != 1 {
		t.Fatalf("history should restart on a backwards clock: %+v", r)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	d := NewDetector()
	feed(d, t0, 10*time.Millisecond, make([]int64, historyLimit*3)...)
	if len(d.history) != historyLimit {
		t.Fatalf("history %d > limit %d", len(d.history), historyLimit)
	}
}
