package core

import (
	"math"
	"strings"
	"testing"
)

func TestDotsUsesHalfUpMillimetreConversion(t *testing.T) {
	for _, test := range []struct {
		mm   float64
		want int
	}{
		{mm: 1, want: 8}, {mm: 0.1, want: 1}, {mm: 0.2, want: 2}, {mm: 0.3, want: 2}, {mm: 0.4, want: 3}, {mm: 0.5, want: 4},
	} {
		got, err := dots(test.mm, 203)
		if err != nil || got != test.want {
			t.Fatalf("dots(%v) = %d, %v; want %d", test.mm, got, err, test.want)
		}
	}
	if _, err := dots(math.NaN(), 203); err == nil {
		t.Fatal("NaN millimetres were accepted")
	}
}

func TestRenderLabelDocumentOperationsDense(t *testing.T) {
	got, err := RenderLabelDocument(OperationsDense, map[string]string{
		"serialNumber": "26J9094", "qr": "https://plant.orrn.in/b/26J9094?c=plant", "quantity": "24 Pcs",
		"length": "13.660ft", "poNumber": "247", "weight12ft": "1.78 kg", "weight_each": "2.03 kg",
		"seriesName": "(3/4)\" 2 Track Top", "cast": "K31", "salesOrder": "SO-12/1", "standard": "IS 1234", "cml": "CML-1", "weight": "48.700 kg",
	})
	if err != nil {
		t.Fatalf("RenderLabelDocument: %v", err)
	}
	commands := []string{
		"SIZE 102.25 mm, 76.20 mm", "GAP 2.00 mm, 0 mm", "DIRECTION 0", "CLS",
		`TEXT 32,32,"3",0,2,2,"26J9094"`,
		`QRCODE 591,24,M,6,A,0,"https://plant.orrn.in/b/26J9094?c=plant"`,
		`TEXT 32,104,"3",0,1,1,"Quantity"`,
		`TEXT 32,136,"3",0,2,2,"24 Pcs"`,
		`TEXT 304,104,"3",0,1,1,"Cut length"`,
		`TEXT 304,136,"3",0,2,2,"13.660ft"`,
		`TEXT 32,216,"3",0,1,1,"Weight / 12ft"`,
		`TEXT 32,248,"3",0,1,1,"1.78 kg"`,
		`TEXT 304,216,"3",0,1,1,"Weight / cut"`,
		`TEXT 304,248,"3",0,1,1,"2.03 kg"`,
		`TEXT 32,304,"3",0,1,1,"PO"`,
		`TEXT 32,336,"3",0,1,1,"247"`,
		`TEXT 304,304,"3",0,1,1,"Sales order"`,
		`TEXT 304,336,"3",0,1,1,"SO-12/1"`,
		`TEXT 591,304,"3",0,1,1,"Cast"`,
		`TEXT 591,336,"3",0,1,1,"K31"`,
		`TEXT 32,392,"3",0,1,1,"Series"`,
		`TEXT 32,424,"3",0,1,1,"(3/4)\" 2 Track Top"`,
		`TEXT 32,480,"3",0,1,1,"IS 1234"`,
		`TEXT 32,511,"3",0,1,1,"CML-1"`,
		`TEXT 448,464,"3",0,1,1,"Total weight"`,
		`TEXT 448,496,"3",0,2,2,"48.700 kg"`,
		"PRINT 1,1",
	}
	want := strings.Join(commands, "\r\n") + "\r\n"
	if got != want {
		t.Fatalf("operations-dense TSPL mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderLabelDocumentExpandedSymbologies(t *testing.T) {
	document := LabelDocument{
		Version: 1,
		Page:    LabelPage{WidthMM: 102.25, HeightMM: 76.2, GapMM: 2, DPI: 203, SpeedInSec: 5, Density: 8, Direction: 0},
		Elements: []LabelElement{
			{ID: "aztec", Type: "aztec", Xmm: 1, Ymm: 2, Rotation: 90, Content: "AZ-1", AztecType: "compact", ModuleMM: 0.5, ErrorCorrection: 25, WhiteOnBlack: true},
			{ID: "maxi", Type: "maxicode", Xmm: 3, Ymm: 4, Rotation: 0, Content: "MAX-1", MaxiCodeMode: 5},
			{ID: "pdf", Type: "pdf417", Xmm: 5, Ymm: 6, WidthMM: 40, HeightMM: 20, Rotation: 0, Content: "PDF-1", Columns: 4, Security: 3, ModuleMM: 0.5, Truncated: true},
			{ID: "composite", Type: "composite", Xmm: 7, Ymm: 8, Rotation: 0, Content: "CC-1", CompositeType: "128_CC_C", ModuleMM: 0.5},
			{ID: "rss", Type: "rss", Xmm: 9, Ymm: 10, Rotation: 0, Content: "RSS-1", RSSType: "Expanded", ModuleMM: 0.5, Columns: 12},
			{ID: "line", Type: "line", Xmm: 1, Ymm: 30, WidthMM: 20, HeightMM: 1, StrokeMM: 0.5},
			{ID: "box", Type: "box", Xmm: 1, Ymm: 40, WidthMM: 20, HeightMM: 10, StrokeMM: 0.5},
		},
	}
	got, err := RenderLabelDocument(document, map[string]string{})
	if err != nil {
		t.Fatalf("RenderLabelDocument: %v", err)
	}
	commands := []string{
		"SIZE 102.25 mm, 76.20 mm", "GAP 2.00 mm, 0 mm", "DIRECTION 0", "CLS",
		`AZTEC 8,16,90,4,25,0,0,0,1,"AZ-1"`,
		`MAXICODE 24,32,5,"MAX-1"`,
		`PDF417 40,48,320,160,0,E3,W4,C4,T1,"PDF-1"`,
		`RSS 56,64,"UCC128CCC",0,4,2,"CC-1"`,
		`RSS 72,80,"RSSEXP",0,4,2,12,"RSS-1"`,
		"BAR 8,240,168,248,4",
		"BOX 8,320,168,400,4",
		"PRINT 1,1",
	}
	want := strings.Join(commands, "\r\n") + "\r\n"
	if got != want {
		t.Fatalf("expanded TSPL mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderLabelDocumentEscapesVariablesAndCapsPayload(t *testing.T) {
	document := LabelDocument{
		Version:  1,
		Page:     LabelPage{WidthMM: 102.25, HeightMM: 76.2, GapMM: 2, DPI: 203, SpeedInSec: 5, Density: 8, Direction: 0},
		Elements: []LabelElement{{ID: "text", Type: "text", Xmm: 1, Ymm: 1, Variable: "serialNumber", Font: "Sans Serif", FontSizeMM: 3}},
	}
	got, err := RenderLabelDocument(document, map[string]string{"serialNumber": "A\"B\\C\nD"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `TEXT 8,8,"3",0,1,1,"A\"B\\C\nD"`) {
		t.Fatalf("variable was not escaped: %q", got)
	}
	document.Elements[0].Variable = ""
	document.Elements[0].Content = strings.Repeat("x", 64*1024)
	if _, err := RenderLabelDocument(document, map[string]string{}); err == nil {
		t.Fatal("payload over 64 KiB was accepted")
	}
}
