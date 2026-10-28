package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

type LabelPage struct {
	WidthMM    float64 `json:"widthMm"`
	HeightMM   float64 `json:"heightMm"`
	GapMM      float64 `json:"gapMm"`
	DPI        int     `json:"dpi"`
	SpeedInSec int     `json:"speedInSec"`
	Density    int     `json:"density"`
	Direction  int     `json:"direction"`
}

type LabelDocument struct {
	Version   int                    `json:"version"`
	Page      LabelPage              `json:"page"`
	Elements  []LabelElement         `json:"elements"`
	Variables map[string]VariableDef `json:"variables,omitempty"`
}

type LabelPrinterProfile struct {
	Name        string
	DPI         int
	MinWidthMM  float64
	MaxWidthMM  float64
	MinHeightMM float64
	MaxHeightMM float64
	SpeedsInSec []int
}

type LabelIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

var TE200Profile = LabelPrinterProfile{
	Name: "TE200", DPI: 203, MinWidthMM: 5, MaxWidthMM: 112, MinHeightMM: 5, MaxHeightMM: 2794, SpeedsInSec: []int{2, 3, 4, 5, 6},
}

var labelVariables = map[string]bool{
	"length": true, "poNumber": true, "quantity": true, "serialNumber": true, "seriesName": true,
	"weight": true, "weight12ft": true, "weight_each": true, "layout": true, "cast": true,
	"qr": true, "salesOrder": true, "standard": true, "cml": true, "partyNo": true, "showLogo": true,
}

var labelRotations = map[int]bool{0: true, 90: true, 180: true, 270: true}
var labelFonts = map[string]bool{"Sans Serif": true, "Font 3": true}
var labelBarcodeTypes = map[string]bool{"128": true, "39": true, "93": true, "CODABAR": true, "EAN13": true, "EAN8": true, "I2OF5": true, "MSI": true, "POSTNET": true, "UPCA": true, "UPCE": true}
var labelCompositeTypes = map[string]bool{"RSS14": true, "Truncated": true, "Stacked": true, "Omnidirectional": true, "Limited": true, "Expanded": true, "ExpandedStacked": true, "128_CC_AB": true, "128_CC_C": true}
var labelRSSTypes = map[string]bool{"RSS14": true, "Truncated": true, "Stacked": true, "Omnidirectional": true, "Limited": true, "Expanded": true, "ExpandedStacked": true}

func ParseLabelDocument(raw []byte) (LabelDocument, error) {
	var document LabelDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return LabelDocument{}, fmt.Errorf("parse label document: %w", err)
	}
	var legacy struct {
		WidthMM  float64 `json:"width_mm"`
		HeightMM float64 `json:"height_mm"`
		GapMM    float64 `json:"gap_mm"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return LabelDocument{}, fmt.Errorf("parse legacy page settings: %w", err)
	}
	if document.Page.WidthMM == 0 {
		document.Page.WidthMM = legacy.WidthMM
	}
	if document.Page.HeightMM == 0 {
		document.Page.HeightMM = legacy.HeightMM
	}
	if document.Page.GapMM == 0 {
		document.Page.GapMM = legacy.GapMM
	}
	if document.Version == 0 {
		document.Version = 1
	}
	if document.Page.DPI == 0 {
		document.Page.DPI = 203
	}
	if document.Page.SpeedInSec == 0 {
		document.Page.SpeedInSec = 5
	}
	for index, element := range document.Elements {
		document.Elements[index] = normalizeLegacyLabelElement(element, document.Page.DPI, index)
	}
	return document, nil
}

func normalizeLegacyLabelElement(element LabelElement, dpi, index int) LabelElement {
	if element.ID == "" {
		element.ID = fmt.Sprintf("element-%d", index)
	}
	if element.Xmm == 0 && element.X != 0 {
		element.Xmm = float64(element.X) * 25.4 / float64(dpi)
	}
	if element.Ymm == 0 && element.Y != 0 {
		element.Ymm = float64(element.Y) * 25.4 / float64(dpi)
	}
	if element.Type == "text" {
		if element.Font == "" || element.Font == "3" {
			element.Font = "Sans Serif"
		}
		if element.FontSizeMM == 0 {
			element.FontSizeMM = 3
		}
	}
	if element.Type == "barcode" {
		if element.HeightMM == 0 && element.Height > 0 {
			element.HeightMM = float64(element.Height) * 25.4 / float64(dpi)
		}
		if element.QuietZoneMM == 0 {
			element.QuietZoneMM = 2
		}
		if !element.HumanReadable {
			element.HumanReadable = true
		}
	}
	if element.Type == "qrcode" {
		if element.CellMM == 0 && element.CellWidth > 0 {
			element.CellMM = float64(element.CellWidth) * 25.4 / float64(dpi)
		}
		if element.Model == 0 {
			element.Model = 2
		}
		if element.Mask == "" {
			element.Mask = "auto"
		}
		if element.Mode == "" {
			element.Mode = "auto"
		}
		if element.QuietZoneMM == 0 {
			element.QuietZoneMM = 2
		}
	}
	if element.Type == "datamatrix" {
		if element.WidthMM == 0 && element.Width > 0 {
			element.WidthMM = float64(element.Width) * 25.4 / float64(dpi)
		}
		if element.HeightMM == 0 && element.Height > 0 {
			element.HeightMM = float64(element.Height) * 25.4 / float64(dpi)
		}
		if element.ModuleMM == 0 && element.ModuleSize > 0 {
			element.ModuleMM = float64(element.ModuleSize) * 25.4 / float64(dpi)
		}
		if element.ECC == 0 {
			element.ECC = 200
		}
		if element.QuietZoneMM == 0 {
			element.QuietZoneMM = 2
		}
	}
	return element
}

func ValidateLabelDocument(document LabelDocument, profile LabelPrinterProfile) []LabelIssue {
	var issues []LabelIssue
	add := func(path, message string) { issues = append(issues, LabelIssue{Path: path, Message: message}) }
	if document.Version != 1 {
		add("version", "Label document version must be 1")
	}
	page := document.Page
	if page.WidthMM < profile.MinWidthMM || page.WidthMM > profile.MaxWidthMM {
		add("page.widthMm", "Width is outside printer limits")
	}
	if page.HeightMM < profile.MinHeightMM || page.HeightMM > profile.MaxHeightMM {
		add("page.heightMm", "Height is outside printer limits")
	}
	if page.GapMM < 0 || page.GapMM > 50 {
		add("page.gapMm", "Gap must be between 0 and 50 mm")
	}
	if page.DPI != profile.DPI {
		add("page.dpi", fmt.Sprintf("DPI must be %d", profile.DPI))
	}
	speedOK := false
	for _, speed := range profile.SpeedsInSec {
		speedOK = speedOK || page.SpeedInSec == speed
	}
	if !speedOK {
		add("page.speedInSec", "Unsupported print speed")
	}
	if page.Density < 0 || page.Density > 15 {
		add("page.density", "Density must be between 0 and 15")
	}
	if page.Direction != 0 && page.Direction != 1 {
		add("page.direction", "Direction must be 0 or 1")
	}
	if len(document.Elements) == 0 {
		add("elements", "At least one element is required")
	}
	if len(document.Elements) > 64 {
		add("elements", "At most 64 elements are allowed")
	}
	ids := map[string]bool{}
	for index, element := range document.Elements {
		path := fmt.Sprintf("elements.%d", index)
		if element.ID == "" || ids[element.ID] {
			add(path+".id", "Element id must be unique and non-empty")
		}
		ids[element.ID] = true
		if !labelRotations[element.Rotation] {
			add(path+".rotation", "Rotation must be 0, 90, 180, or 270")
		}
		if element.Xmm < 0 || element.Ymm < 0 || element.Xmm > page.WidthMM || element.Ymm > page.HeightMM ||
			(element.WidthMM > 0 && element.Xmm+element.WidthMM > page.WidthMM) || (element.HeightMM > 0 && element.Ymm+element.HeightMM > page.HeightMM) {
			add(path, "Element exceeds printable area")
		}
		if element.Variable != "" && !labelVariables[element.Variable] {
			add(path+".variable", "Forbidden label variable: "+element.Variable)
		}
		if strings.ContainsAny(element.Content, "\x00\r\n") {
			add(path+".content", "Control characters are not allowed")
		}
		validateLabelElement(element, path, add)
	}
	return issues
}

func validateLabelElement(element LabelElement, path string, add func(string, string)) {
	switch element.Type {
	case "text":
		if !labelFonts[element.Font] {
			add(path+".font", "Unsupported font")
		}
		if element.FontSizeMM < 1 || element.FontSizeMM > 50 {
			add(path+".fontSizeMm", "Font size must be between 1 and 50 mm")
		}
		if element.Variable == "" && element.Content == "" {
			add(path+".content", "Text content or variable is required")
		}
	case "barcode":
		if !labelBarcodeTypes[element.Symbology] {
			add(path+".symbology", "Unsupported barcode symbology")
		}
		if element.HeightMM <= 0 {
			add(path+".heightMm", "Barcode height is required")
		}
		if element.Narrow <= 0 || element.Wide < element.Narrow || element.Wide > 10 {
			add(path+".narrow", "Invalid narrow/wide barcode ratio")
		}
		if element.QuietZoneMM < 1 {
			add(path+".quietZoneMm", "Quiet zone must be at least 1 mm")
		}
	case "qrcode":
		if element.Model != 1 && element.Model != 2 {
			add(path+".model", "QR model must be 1 or 2")
		}
		if element.Mask != "auto" && (len(element.Mask) != 1 || element.Mask[0] < '0' || element.Mask[0] > '7') {
			add(path+".mask", "Invalid QR mask")
		}
		if element.Mode != "auto" && element.Mode != "manual" {
			add(path+".mode", "QR mode must be auto or manual")
		}
		if element.CellMM <= 0 {
			add(path+".cellMm", "QR cell size is required")
		}
		if element.QuietZoneMM < 2 {
			add(path+".quietZoneMm", "Quiet zone must be at least 2 mm")
		}
	case "datamatrix":
		if element.ECC != 200 {
			add(path+".ecc", "Data Matrix requires ECC200")
		}
		if element.ModuleMM <= 0 {
			add(path+".moduleMm", "Data Matrix module size is required")
		}
		if element.QuietZoneMM < 2 {
			add(path+".quietZoneMm", "Quiet zone must be at least 2 mm")
		}
	case "aztec":
		switch element.AztecType {
		case "auto", "compact", "full", "rune":
		default:
			add(path+".aztecType", "Invalid Aztec type")
		}
		if element.ErrorCorrection < 0 || element.ErrorCorrection > 99 {
			add(path+".errorCorrection", "Aztec error correction must be between 0 and 99")
		}
		if element.Layers < 0 || element.Layers > 32 {
			add(path+".layers", "Aztec layers must be between 1 and 32")
		}
	case "maxicode":
		if element.MaxiCodeMode < 2 || element.MaxiCodeMode > 5 {
			add(path+".maxicodeMode", "MaxiCode mode must be between 2 and 5")
		}
	case "pdf417":
		if element.Columns <= 0 {
			add(path+".columns", "PDF417 columns are required")
		}
		if element.Security < 0 || element.Security > 8 {
			add(path+".security", "PDF417 security must be between 0 and 8")
		}
	case "composite":
		if !labelCompositeTypes[element.CompositeType] {
			add(path+".compositeType", "Unsupported composite type")
		}
	case "rss":
		if !labelRSSTypes[element.RSSType] {
			add(path+".rssType", "Unsupported RSS type")
		}
	case "line", "box":
		if element.StrokeMM <= 0 {
			add(path+".strokeMm", "Stroke width is required")
		}
	default:
		add(path+".type", "Unsupported element type")
	}
}
