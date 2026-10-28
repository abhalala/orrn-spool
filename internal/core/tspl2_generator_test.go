package core

import (
	"strings"
	"testing"
)

func TestGeneratePreservesDecimalMMAndValidBarcodeSyntax(t *testing.T) {
	gen := NewTSPL2Generator()
	schema := &LabelSchema{
		Name:     "fourcubes-102x76",
		WidthMM:  102.25,
		HeightMM: 76.2,
		GapMM:    2,
		DPI:      203,
		Elements: []LabelElement{
			{
				Type:      "barcode",
				X:         60,
				Y:         220,
				Symbology: "128",
				Height:    60,
				Rotation:  0,
				Narrow:    2,
				Wide:      2,
				Content:   "{{bundleSerial}}",
			},
		},
		Variables: map[string]VariableDef{
			"bundleSerial": {Type: "string", Required: true},
		},
	}

	tspl, err := gen.Generate(schema, map[string]string{"bundleSerial": "26I4086"})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if !strings.Contains(tspl, "SIZE 102.25 mm, 76.20 mm\r\n") {
		t.Fatalf("expected decimal SIZE in mm with CRLF, got:\n%s", tspl)
	}
	if !strings.Contains(tspl, "GAP 2.00 mm, 0 mm\r\n") {
		t.Fatalf("expected decimal GAP in mm with CRLF, got:\n%s", tspl)
	}
	// TSPL2 BARCODE syntax: BARCODE X,Y,"type",height,human_readable,rotation,narrow,wide,"content"
	wantBarcode := `BARCODE 60,220,"128",60,1,0,2,2,"26I4086"`
	if !strings.Contains(tspl, wantBarcode) {
		t.Fatalf("expected %q in TSPL output, got:\n%s", wantBarcode, tspl)
	}
}

func TestGenerateUsesTSCQRAndDataMatrixParameterOrder(t *testing.T) {
	gen := NewTSPL2Generator()
	schema := &LabelSchema{
		Name: "2d-codes", WidthMM: 102.25, HeightMM: 76.2, DPI: 203,
		Elements: []LabelElement{
			{Type: "qrcode", X: 20, Y: 30, Level: "H", CellWidth: 5, Rotation: 90, Content: "QR-123"},
			{Type: "datamatrix", X: 40, Y: 300, Width: 144, Height: 160, ModuleSize: 6, Rotation: 270, Content: "DM-123"},
		},
	}
	tspl, err := gen.Generate(schema, map[string]string{})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	for _, want := range []string{
		`QRCODE 20,30,H,5,A,90,"QR-123"`,
		`DMATRIX 40,300,144,160,x6,r270,"DM-123"`,
	} {
		if !strings.Contains(tspl, want) {
			t.Errorf("expected calibrated command %q in output:\n%s", want, tspl)
		}
	}
}
