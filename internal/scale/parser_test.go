package scale

import (
	"errors"
	"testing"
)

func TestDefaultPatternParsesCommonFrames(t *testing.T) {
	p, err := NewParser("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		line  string
		grams int64
		flag  string
	}{
		{"ST,GS,+  12.345kg", 12345, "ST"},
		{"US,GS,+  12.345kg", 12345, "US"},
		{"ST,NT,-   0.020kg", -20, "ST"},
		{"ST,GS,+ 143.200 kg", 143200, "ST"},
		{"ST,GS,+0000.000kg", 0, "ST"},
		{"+0012.345kg", 12345, ""},
		{"  ST , GS , + 7.5kg\r", 7500, "ST"},
		{"\x02ST,GS,+  52.40kg\x03", 52400, "ST"},
		{"ST,GS,+  1.2345kg", 1235, "ST"}, // rounded half up past grams
		{"ST,GS,+  1.2344kg", 1234, "ST"},
		{"9kg", 9000, ""},
	}
	for _, c := range cases {
		f, err := p.Parse(c.line)
		if err != nil {
			t.Errorf("%q: %v", c.line, err)
			continue
		}
		if f.Grams != c.grams || f.IndicatorFlag != c.flag {
			t.Errorf("%q: got %d g flag %q, want %d g flag %q", c.line, f.Grams, f.IndicatorFlag, c.grams, c.flag)
		}
	}
}

func TestDefaultPatternRejectsNonWeightLines(t *testing.T) {
	p, _ := NewParser("")
	for _, line := range []string{
		"", "OL,GS,+  -----kg", "ST,GS,+  12.345lb", "ST,GS,+  12.3.4kg", "INDICATOR V2.1",
		"ST,GS,+  kg", "ST,GS,12,345kg",
	} {
		if _, err := p.Parse(line); !errors.Is(err, ErrNoMatch) {
			t.Errorf("%q: want ErrNoMatch, got %v", line, err)
		}
	}
}

func TestCustomPatternWithGramsGroup(t *testing.T) {
	// e.g. an indicator that prints "W:0012345 g"
	p, err := NewParser(`^W:(?P<grams>\d+)\s*g$`)
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.Parse("W:0012345 g")
	if err != nil || f.Grams != 12345 {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestCustomPatternWithKgGroupAndOtherFields(t *testing.T) {
	p, err := NewParser(`^=\s*(?P<kg>[+-]?\d+\.\d+)\s*\(kg\)\s+#\d+$`)
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.Parse("= 52.405 (kg) #17")
	if err != nil || f.Grams != 52405 {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestPatternMustNameAWeightGroup(t *testing.T) {
	if _, err := NewParser(`^(\d+)kg$`); err == nil {
		t.Fatal("want an error for a pattern without grams/kg group")
	}
	if _, err := NewParser(`^(?P<kg>[`); err == nil {
		t.Fatal("want an error for an invalid regex")
	}
}

func TestKgToGrams(t *testing.T) {
	cases := map[string]int64{
		"0": 0, "+0.000": 0, "-0.005": -5, "12": 12000, ".5": 500, "+  12.345": 12345,
		"999.9995": 1000000, "0.0004": 0, "0.0005": 1,
	}
	for in, want := range cases {
		got, err := KgToGrams(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "+", "1e3", "12,5", "1234567890123.0"} {
		if _, err := KgToGrams(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	p, _ := NewParser("")
	for _, s := range []string{"ST,GS,+  12.345kg", "", "kg", "-.kg"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		_, _ = p.Parse(line)
	})
}
