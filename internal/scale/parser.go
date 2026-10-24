// Package scale reads a weighing indicator's continuous ASCII output over a
// serial (RS-232 / USB-serial) line, decides when the weight is stable, and
// serves the result to ORRN.
//
// The reader is receive-only. It never writes a byte to the indicator: no
// tare, no zero, no print command. See docs/scale-and-orrn-auth.md.
package scale

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DefaultFramePattern matches the common "continuous output" frame most
// indicators send several times a second, for example:
//
//	ST,GS,+  12.345kg
//	US,NT,-   0.020 kg
//	+0012.345kg
//
// The optional first field is the indicator's own stable/unstable flag (kept
// for diagnostics only; stability is decided by the Detector). OL (overload)
// frames carry no digits and never match.
const DefaultFramePattern = `^(?:(?P<status>ST|US)\s*,\s*)?(?:(?P<mode>GS|NT|GR|TR)\s*,\s*)?(?P<kg>[+-]?\s*\d+(?:\.\d+)?)\s*kg\s*$`

// ErrNoMatch means the line is not a weight frame (noise, an overload frame,
// a banner line). The reader counts and skips it.
var ErrNoMatch = errors.New("scale: line is not a weight frame")

// Frame is one parsed weight frame.
type Frame struct {
	Grams int64
	// IndicatorFlag is the indicator's own ST/US field when the pattern has a
	// "status" group. Informational only.
	IndicatorFlag string
}

// Parser turns raw lines into frames using a configurable regular expression.
// The expression must contain a named group "grams" (integer grams) or "kg"
// (decimal kilograms, converted exactly to grams).
type Parser struct {
	re       *regexp.Regexp
	gramsIdx int
	kgIdx    int
	flagIdx  int
}

// NewParser compiles pattern; an empty pattern uses DefaultFramePattern.
func NewParser(pattern string) (*Parser, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = DefaultFramePattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("scale.frame: %w", err)
	}
	p := &Parser{re: re, gramsIdx: -1, kgIdx: -1, flagIdx: -1}
	for i, name := range re.SubexpNames() {
		switch name {
		case "grams":
			p.gramsIdx = i
		case "kg":
			p.kgIdx = i
		case "status":
			p.flagIdx = i
		}
	}
	if p.gramsIdx < 0 && p.kgIdx < 0 {
		return nil, errors.New(`scale.frame: pattern needs a named group "grams" or "kg"`)
	}
	return p, nil
}

// Parse parses one line (without its line terminator).
func (p *Parser) Parse(line string) (Frame, error) {
	line = strings.Trim(line, "\x00\x02\x03\r\n\t ")
	m := p.re.FindStringSubmatch(line)
	if m == nil {
		return Frame{}, ErrNoMatch
	}
	var frame Frame
	if p.flagIdx >= 0 {
		frame.IndicatorFlag = m[p.flagIdx]
	}
	if p.gramsIdx >= 0 && m[p.gramsIdx] != "" {
		g, err := strconv.ParseInt(stripSpaces(m[p.gramsIdx]), 10, 64)
		if err != nil {
			return Frame{}, ErrNoMatch
		}
		frame.Grams = g
		return frame, nil
	}
	if p.kgIdx >= 0 && m[p.kgIdx] != "" {
		g, err := KgToGrams(m[p.kgIdx])
		if err != nil {
			return Frame{}, ErrNoMatch
		}
		frame.Grams = g
		return frame, nil
	}
	return Frame{}, ErrNoMatch
}

// KgToGrams converts a decimal kilogram string ("+  12.345", "-0.02", "7")
// to integer grams without floating point. Digits past the third decimal are
// rounded half away from zero.
func KgToGrams(s string) (int64, error) {
	s = stripSpaces(s)
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if s == "" {
		return 0, errors.New("empty weight")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if !allDigits(whole) || !allDigits(frac) {
		return 0, fmt.Errorf("bad weight %q", s)
	}
	if len(whole) > 12 {
		return 0, fmt.Errorf("weight out of range %q", s)
	}
	kg, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	padded := frac + "000"
	g, _ := strconv.ParseInt(padded[:3], 10, 64)
	grams := kg*1000 + g
	if len(frac) > 3 && frac[3] >= '5' {
		grams++
	}
	if neg {
		grams = -grams
	}
	return grams, nil
}

func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
