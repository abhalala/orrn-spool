package core

import (
	"strings"
	"testing"
)

func validLabelDocument() LabelDocument {
	return LabelDocument{
		Version: 1,
		Page:    LabelPage{WidthMM: 102.25, HeightMM: 76.2, GapMM: 2, DPI: 203, SpeedInSec: 5, Density: 8, Direction: 0},
		Elements: []LabelElement{
			{ID: "serial", Type: "text", Xmm: 4, Ymm: 4, Rotation: 0, Variable: "serialNumber", Font: "Sans Serif", FontSizeMM: 7},
			{ID: "qr", Type: "qrcode", Xmm: 76, Ymm: 4, WidthMM: 22, HeightMM: 22, Rotation: 0, Variable: "qr", Model: 2, Mask: "auto", Mode: "auto", CellMM: 1, QuietZoneMM: 2},
			{ID: "weight", Type: "text", Xmm: 58, Ymm: 67, Rotation: 0, Variable: "weight", Font: "Sans Serif", FontSizeMM: 4.5},
		},
	}
}

func TestValidateLabelDocumentAcceptsOperationsDense(t *testing.T) {
	if issues := ValidateLabelDocument(validLabelDocument(), TE200Profile); len(issues) != 0 {
		t.Fatalf("valid document rejected: %+v", issues)
	}
}

func TestValidateLabelDocumentRejectsForbiddenContent(t *testing.T) {
	document := validLabelDocument()
	document.Elements[0].Variable = "uuid"
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "Forbidden label variable: uuid" {
		t.Fatalf("uuid was accepted: %+v", issues)
	}
	document.Elements[0].Variable = "serialNumber"
	document.Elements[0].Content = "Hello\r\nCLS"
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "Control characters are not allowed" {
		t.Fatalf("TSPL injection was accepted: %+v", issues)
	}
}

func TestValidateLabelDocumentRejectsBoundsAndElementCount(t *testing.T) {
	document := validLabelDocument()
	document.Page.WidthMM = 200
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "Width is outside printer limits" {
		t.Fatalf("invalid width was accepted: %+v", issues)
	}
	document = validLabelDocument()
	document.Elements[1].Xmm = 90
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "Element exceeds printable area" {
		t.Fatalf("out-of-bounds QR was accepted: %+v", issues)
	}
	document = validLabelDocument()
	document.Elements = make([]LabelElement, 65)
	for i := range document.Elements {
		document.Elements[i] = LabelElement{ID: "text", Type: "text", Variable: "seriesName", Font: "Sans Serif", FontSizeMM: 3}
	}
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "At most 64 elements are allowed" {
		t.Fatalf("65 elements were accepted: %+v", issues)
	}
}

func TestValidateLabelDocumentRejectsInvalidSymbologySettings(t *testing.T) {
	document := validLabelDocument()
	document.Elements[0].Font = "Comic Sans"
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) == 0 || issues[0].Message != "Unsupported font" {
		t.Fatalf("unsupported font was accepted: %+v", issues)
	}
	document = validLabelDocument()
	document.Elements[1].Model = 3
	document.Elements[1].Mask = "8"
	document.Elements[1].QuietZoneMM = 1
	issues := ValidateLabelDocument(document, TE200Profile)
	messages := make([]string, len(issues))
	for i := range issues {
		messages[i] = issues[i].Message
	}
	joined := strings.Join(messages, ",")
	for _, want := range []string{"QR model must be 1 or 2", "Invalid QR mask", "Quiet zone must be at least 2 mm"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %+v", want, issues)
		}
	}
}

func TestParseLabelDocumentKeepsLegacyJSONReadable(t *testing.T) {
	document, err := ParseLabelDocument([]byte(`{"name":"legacy","width_mm":100,"height_mm":50,"elements":[]}`))
	if err != nil || document.Page.WidthMM != 100 || document.Page.HeightMM != 50 || document.Page.DPI != 203 {
		t.Fatalf("legacy parse = %+v, %v", document, err)
	}
}
