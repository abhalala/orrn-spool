package core

// Built-in bundle labels, laid out against what the TE200/TA220 actually prints: TSPL font "3" is a
// 16 x 24 dot cell (2 x 3 mm at 203 dpi) multiplied by a whole number, so text is 3 mm (x1) or 6 mm
// (x2) tall, and a QR is modules x cell dots. Keep identical to ORRN's packages/domain/src/builtin-labels.ts.
var builtInLabelPage = LabelPage{WidthMM: 102.25, HeightMM: 76.2, GapMM: 2, DPI: 203, SpeedInSec: 5, Density: 8, Direction: 0}

func labelText(id string, x, y, size float64, variable string, content string) LabelElement {
	return LabelElement{ID: id, Type: "text", Xmm: x, Ymm: y, Variable: variable, Content: content, Font: "Sans Serif", FontSizeMM: size, Align: "left"}
}

// labelQR uses 6-dot cells: a 33-module tenant QR address prints 24.8 mm square.
func labelQR(id string, x, y float64) LabelElement {
	return LabelElement{ID: id, Type: "qrcode", Xmm: x, Ymm: y, WidthMM: 25, HeightMM: 25, Variable: "qr", Level: "M", Model: 2, Mask: "auto", Mode: "auto", CellMM: 0.75, QuietZoneMM: 2}
}

func labelSerialBarcode(id string, x, y, height float64) LabelElement {
	return LabelElement{ID: id, Type: "barcode", Xmm: x, Ymm: y, HeightMM: height, Variable: "serialNumber", Symbology: "128", Narrow: 2, Wide: 2, HumanReadable: true, QuietZoneMM: 2}
}

// OperationsDense (B) is the default: every operational field in aligned columns, serial and QR on top.
var OperationsDense = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 6, "serialNumber", ""),
		labelQR("qr", 74, 3),
		labelText("quantity-label", 4, 13, 3, "", "Quantity"),
		labelText("quantity", 4, 17, 6, "quantity", ""),
		labelText("length-label", 38, 13, 3, "", "Cut length"),
		labelText("length", 38, 17, 6, "length", ""),
		labelText("weight12-label", 4, 27, 3, "", "Weight / 12ft"),
		labelText("weight12", 4, 31, 3, "weight12ft", ""),
		labelText("weight-each-label", 38, 27, 3, "", "Weight / cut"),
		labelText("weight-each", 38, 31, 3, "weight_each", ""),
		labelText("po-label", 4, 38, 3, "", "PO"),
		labelText("po", 4, 42, 3, "poNumber", ""),
		labelText("sales-order-label", 38, 38, 3, "", "Sales order"),
		labelText("sales-order", 38, 42, 3, "salesOrder", ""),
		labelText("cast-label", 74, 38, 3, "", "Cast"),
		labelText("cast", 74, 42, 3, "cast", ""),
		labelText("series-label", 4, 49, 3, "", "Series"),
		labelText("series", 4, 53, 3, "seriesName", ""),
		labelText("bis", 4, 60, 3, "standard", ""),
		labelText("cml", 4, 64, 3, "cml", ""),
		labelText("total-label", 56, 58, 3, "", "Total weight"),
		labelText("total", 56, 62, 6, "weight", ""),
	},
}

// PolishedLabel (A) is the classic hierarchy without order, cast and BIS rows.
var PolishedLabel = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 6, "serialNumber", ""),
		labelQR("qr", 74, 3),
		labelText("quantity-label", 4, 13, 3, "", "Quantity"),
		labelText("quantity", 4, 17, 6, "quantity", ""),
		labelText("length-label", 38, 13, 3, "", "Cut length"),
		labelText("length", 38, 17, 6, "length", ""),
		labelText("weight12-label", 4, 27, 3, "", "Weight / 12ft"),
		labelText("weight12", 4, 31, 3, "weight12ft", ""),
		labelText("weight-each-label", 38, 27, 3, "", "Weight / cut"),
		labelText("weight-each", 38, 31, 3, "weight_each", ""),
		labelText("po-label", 4, 38, 3, "", "PO"),
		labelText("po", 4, 42, 3, "poNumber", ""),
		labelText("series-label", 4, 49, 3, "", "Series"),
		labelText("series", 4, 53, 3, "seriesName", ""),
		labelText("total-label", 56, 58, 3, "", "Total weight"),
		labelText("total", 56, 62, 6, "weight", ""),
	},
}

// CompactLabel (C) is serial, scannable Code 128, series and weight.
var CompactLabel = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 6, "serialNumber", ""),
		labelSerialBarcode("serial-barcode", 4, 13, 12),
		labelText("series", 4, 33, 3, "seriesName", ""),
		labelText("weight", 4, 39, 6, "weight", ""),
	},
}
