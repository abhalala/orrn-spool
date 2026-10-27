package core

var builtInLabelPage = LabelPage{WidthMM: 102.25, HeightMM: 76.2, GapMM: 2, DPI: 203, SpeedInSec: 5, Density: 8, Direction: 0}

func labelText(id string, x, y, size float64, variable string, content string, align string) LabelElement {
	return LabelElement{ID: id, Type: "text", Xmm: x, Ymm: y, Variable: variable, Content: content, Font: "Sans Serif", FontSizeMM: size, Align: align}
}

func labelQR(id string, x, y, size float64) LabelElement {
	return LabelElement{ID: id, Type: "qrcode", Xmm: x, Ymm: y, WidthMM: size, HeightMM: size, Variable: "qr", Model: 2, Mask: "auto", Mode: "auto", CellMM: 1, QuietZoneMM: 2}
}

func labelSerialBarcode(id string, x, y, height float64) LabelElement {
	return LabelElement{ID: id, Type: "barcode", Xmm: x, Ymm: y, HeightMM: height, Variable: "serialNumber", Symbology: "128", Narrow: 2, Wide: 2, HumanReadable: true, QuietZoneMM: 2}
}

var OperationsDense = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 7, "serialNumber", "", "left"),
		labelQR("qr", 76, 4, 22),
		labelText("quantity-label", 4, 13, 2.2, "", "Quantity", "left"),
		labelText("quantity", 4, 16.5, 4.2, "quantity", "", "left"),
		labelText("length-label", 4, 24, 2.2, "", "Cut Length", "left"),
		labelText("length", 4, 27.5, 4.2, "length", "", "left"),
		labelText("po-label", 4, 34, 2.2, "", "PO", "left"),
		labelText("po", 4, 37, 3, "poNumber", "", "left"),
		labelText("weight12-label", 58, 13, 2.2, "", "Weight / 12ft", "right"),
		labelText("weight12", 58, 16.5, 4.2, "weight12ft", "", "right"),
		labelText("weight-each-label", 58, 24, 2.2, "", "Weight / cut", "right"),
		labelText("weight-each", 58, 27.5, 4.2, "weight_each", "", "right"),
		labelText("series-label", 4, 46, 2.2, "", "Series Name", "left"),
		labelText("series", 4, 49, 3, "seriesName", "", "left"),
		labelText("cast", 4, 55, 2.4, "cast", "", "left"),
		labelText("sales-order", 4, 59, 2.4, "salesOrder", "", "left"),
		labelText("bis", 4, 63, 2.2, "standard", "", "left"),
		labelText("cml", 4, 66, 2.2, "cml", "", "left"),
		labelText("total-label", 58, 61, 2.2, "", "Total Weight", "right"),
		labelText("total", 58, 65, 4.5, "weight", "", "right"),
	},
}

var LegacyPolished = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 7, "serialNumber", "", "left"),
		labelQR("qr", 76, 4, 22),
		labelText("quantity-label", 4, 13, 2.2, "", "Quantity", "left"),
		labelText("quantity", 4, 16.5, 4.2, "quantity", "", "left"),
		labelText("length-label", 4, 24, 2.2, "", "Cut Length", "left"),
		labelText("length", 4, 27.5, 4.2, "length", "", "left"),
		labelText("po-label", 4, 34, 2.2, "", "PO", "left"),
		labelText("po", 4, 37, 3, "poNumber", "", "left"),
		labelText("weight12-label", 58, 13, 2.2, "", "Weight / 12ft", "right"),
		labelText("weight12", 58, 16.5, 4.2, "weight12ft", "", "right"),
		labelText("weight-each-label", 58, 24, 2.2, "", "Weight / cut", "right"),
		labelText("weight-each", 58, 27.5, 4.2, "weight_each", "", "right"),
		labelText("series-label", 4, 52, 2.2, "", "Series Name", "left"),
		labelText("series", 4, 55, 3, "seriesName", "", "left"),
		labelText("total-label", 58, 61, 2.2, "", "Total Weight", "right"),
		labelText("total", 58, 65, 4.5, "weight", "", "right"),
	},
}

var CompactCurrent = LabelDocument{
	Version: 1,
	Page:    builtInLabelPage,
	Elements: []LabelElement{
		labelText("serial", 4, 4, 5.5, "serialNumber", "", "left"),
		labelSerialBarcode("serial-barcode", 4, 14, 14),
		labelText("series", 4, 34, 2.8, "seriesName", "", "left"),
		labelText("weight", 4, 40, 3, "weight", "", "left"),
	},
}
