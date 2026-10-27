package core

import (
	"strings"
	"testing"
)

// TestPhysicalLabelFixtures verifies generated geometry and symbology only.
// Ruler measurements, QR/barcode scans, and physical copy counts remain manual
// evidence gates; this test does not claim they were observed.
func TestPhysicalLabelFixtures(t *testing.T) {
	variables := map[string]string{
		"serialNumber": "FC-B-0001", "qr": "https://orrn.in/b/FC-B-0001", "quantity": "12",
		"length": "6000 mm", "poNumber": "PO-42", "weight12ft": "18.40 kg", "weight_each": "1.53 kg",
		"seriesName": "6063-T6", "cast": "CAST-7", "salesOrder": "SO-9", "standard": "BIS", "cml": "CML-3", "weight": "18.40 kg",
	}
	for _, fixture := range []struct {
		name      string
		document  LabelDocument
		symbology string
	}{
		{name: "B operations dense", document: OperationsDense, symbology: "QRCODE"},
		{name: "A polished alternate", document: PolishedLabel, symbology: "QRCODE"},
		{name: "C compact alternate", document: CompactLabel, symbology: "BARCODE"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			payload, err := RenderLabelDocument(fixture.document, variables)
			if err != nil {
				t.Fatalf("RenderLabelDocument: %v", err)
			}
			for _, expected := range []string{"SIZE 102.25 mm, 76.20 mm\r\n", "GAP 2.00 mm, 0 mm\r\n", fixture.symbology, "FC-B-0001", "PRINT 1,1\r\n"} {
				if !strings.Contains(payload, expected) {
					t.Fatalf("payload missing %q:\n%s", expected, payload)
				}
			}
			if strings.Contains(strings.ReplaceAll(payload, "\r\n", ""), "\n") {
				t.Fatal("payload contains a bare LF instead of CRLF")
			}
		})
	}
}
