package printer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Target is one printer from the node's desired configuration.
type Target struct {
	PrinterID  string `json:"printerId"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Paused     bool   `json:"paused"`
	MACAddress string `json:"macAddress,omitempty"`
	// Label stock, used only for the built-in test label.
	LabelWidthMM  float64 `json:"labelWidthMm,omitempty"`
	LabelHeightMM float64 `json:"labelHeightMm,omitempty"`
	GapMM         float64 `json:"gapMm,omitempty"`
	DPI           int     `json:"dpi,omitempty"`
	MediaType     string  `json:"mediaType,omitempty"`
	SpeedInSec    int     `json:"speedInSec,omitempty"`
	Density       int     `json:"density,omitempty"`
	CutterEnabled bool    `json:"cutterEnabled,omitempty"`
}

func (t Target) Config() Config {
	return Config{PrinterID: t.PrinterID, Host: t.Host, Port: strconv.Itoa(t.Port)}
}

// ParseTargets validates a complete desired printer list. One invalid entry
// rejects the whole list so a config version is applied all-or-nothing.
func ParseTargets(raw []json.RawMessage) ([]Target, error) {
	targets := make([]Target, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, item := range raw {
		var t Target
		if err := json.Unmarshal(item, &t); err != nil {
			return nil, fmt.Errorf("printer %d: %w", i, err)
		}
		t.PrinterID = strings.TrimSpace(t.PrinterID)
		t.Name = strings.TrimSpace(t.Name)
		t.Host = strings.TrimSpace(t.Host)
		if t.Port == 0 {
			t.Port = 9100
		}
		switch {
		case t.PrinterID == "":
			return nil, fmt.Errorf("printer %d: printerId is required", i)
		case t.Host == "":
			return nil, fmt.Errorf("printer %s: host is required", t.PrinterID)
		case t.Port < 1 || t.Port > 65535:
			return nil, fmt.Errorf("printer %s: port %d is out of range", t.PrinterID, t.Port)
		case seen[t.PrinterID]:
			return nil, fmt.Errorf("printer %s: duplicate printerId", t.PrinterID)
		}
		if t.Name == "" {
			t.Name = t.PrinterID
		}
		if t.LabelWidthMM <= 0 {
			t.LabelWidthMM = 100
		}
		if t.LabelHeightMM <= 0 {
			t.LabelHeightMM = 75
		}
		if t.GapMM <= 0 {
			t.GapMM = 3
		}
		if t.DPI <= 0 {
			t.DPI = 203
		}
		seen[t.PrinterID] = true
		targets = append(targets, t)
	}
	return targets, nil
}
