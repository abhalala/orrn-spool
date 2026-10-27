package edgeclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/printer"
)

type printJobPayload struct {
	JobKey         string          `json:"jobKey"`
	PayloadHash    string          `json:"payloadHash"`
	PrinterID      string          `json:"printerId"`
	TemplateSchema json.RawMessage `json:"templateSchema"`
	Variables      map[string]any  `json:"variables"`
	Copies         int             `json:"copies"`
}

// renderPrintJob freezes the label as TSPL when the job is accepted, so later
// template edits never change a queued or reprinted label.
func renderPrintJob(p printJobPayload) (string, error) {
	if len(p.TemplateSchema) == 0 || string(p.TemplateSchema) == "null" {
		return "", errors.New("templateSchema is required")
	}
	document, err := core.ParseLabelDocument([]byte(p.TemplateSchema))
	if err != nil {
		return "", fmt.Errorf("invalid templateSchema: %w", err)
	}
	if document.Page.WidthMM <= 0 || document.Page.HeightMM <= 0 {
		return "", errors.New("templateSchema must set width_mm and height_mm")
	}
	vars := make(map[string]string, len(p.Variables))
	for k, v := range p.Variables {
		switch val := v.(type) {
		case nil:
			vars[k] = ""
		case string:
			vars[k] = val
		default:
			vars[k] = fmt.Sprint(val)
		}
	}
	tspl, err := core.RenderLabelDocument(document, vars)
	if err != nil {
		return "", err
	}
	return withCopies(tspl, p.Copies), nil
}

func withCopies(tspl string, copies int) string {
	if copies <= 1 {
		return tspl
	}
	const single = "PRINT 1,1\r\n"
	if strings.HasSuffix(tspl, single) {
		return strings.TrimSuffix(tspl, single) + fmt.Sprintf("PRINT 1,%d\r\n", copies)
	}
	return tspl
}

func renderTestLabel(t printer.Target, now time.Time) string {
	dotsPerMM := float64(t.DPI) / 25.4
	centerX := int(t.LabelWidthMM*dotsPerMM) / 2
	centerY := int(t.LabelHeightMM*dotsPerMM) / 2
	return fmt.Sprintf(
		"SIZE %.2f mm, %.2f mm\r\nGAP %.2f mm, 0 mm\r\nDIRECTION 0\r\nCLS\r\n"+
			"TEXT %d,%d,\"3\",0,2,2,\"TEST LABEL\"\r\n"+
			"TEXT %d,%d,\"3\",0,1,1,\"Printer: %s\"\r\n"+
			"TEXT %d,%d,\"2\",0,1,1,\"%s\"\r\n"+
			"BARCODE %d,%d,\"128\",60,1,0,2,2,\"%s\"\r\nPRINT 1,1\r\n",
		t.LabelWidthMM, t.LabelHeightMM, t.GapMM,
		centerX-80, centerY-60,
		centerX-100, centerY,
		escapeQuotes(t.Name),
		centerX-100, centerY+30,
		now.UTC().Format("2006-01-02 15:04 UTC"),
		centerX-100, centerY+60,
		escapeQuotes(t.Host),
	)
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `'`)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
