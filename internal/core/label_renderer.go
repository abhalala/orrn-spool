package core

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

const maxRenderedLabelBytes = 64 * 1024

func dots(mm float64, dpi int) (int, error) {
	if dpi <= 0 {
		return 0, fmt.Errorf("dpi must be positive")
	}
	if math.IsNaN(mm) || math.IsInf(mm, 0) {
		return 0, fmt.Errorf("millimetre value must be finite")
	}
	return int(math.Round(mm * float64(dpi) / 25.4)), nil
}

func fontScale(sizeMM float64) int {
	scale := int(math.Floor(sizeMM/3 + 0.5))
	if scale < 1 {
		return 1
	}
	return scale
}

func RenderLabelDocument(document LabelDocument, variables map[string]string) (string, error) {
	for name, definition := range document.Variables {
		if definition.Required && definition.Default == "" && variables[name] == "" {
			return "", fmt.Errorf("required variable %q is missing", name)
		}
	}
	if issues := ValidateLabelDocument(document, TE200Profile); len(issues) != 0 {
		return "", fmt.Errorf("invalid label document: %+v", issues)
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("SIZE %.2f mm, %.2f mm\r\n", document.Page.WidthMM, document.Page.HeightMM))
	output.WriteString(fmt.Sprintf("GAP %.2f mm, 0 mm\r\n", document.Page.GapMM))
	output.WriteString(fmt.Sprintf("DIRECTION %d\r\n", document.Page.Direction))
	output.WriteString("CLS\r\n")
	for _, element := range document.Elements {
		command, err := renderLabelElement(element, document.Page.DPI, variables)
		if err != nil {
			return "", fmt.Errorf("render %s %s: %w", element.ID, element.Type, err)
		}
		output.WriteString(command)
		output.WriteString("\r\n")
	}
	output.WriteString("PRINT 1,1\r\n")
	if output.Len() > maxRenderedLabelBytes {
		return "", fmt.Errorf("rendered label exceeds %d bytes", maxRenderedLabelBytes)
	}
	return output.String(), nil
}

func renderLabelContent(element LabelElement, variables map[string]string) string {
	if element.Variable != "" {
		return variables[element.Variable]
	}
	content := element.Content
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	return re.ReplaceAllStringFunc(content, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
		return variables[name]
	})
}

func renderLabelElement(element LabelElement, dpi int, variables map[string]string) (string, error) {
	x, err := dots(element.Xmm, dpi)
	if err != nil {
		return "", err
	}
	y, err := dots(element.Ymm, dpi)
	if err != nil {
		return "", err
	}
	content := escapeTSPLString(renderLabelContent(element, variables))
	width, err := dots(element.WidthMM, dpi)
	if err != nil {
		return "", err
	}
	height, err := dots(element.HeightMM, dpi)
	if err != nil {
		return "", err
	}

	switch element.Type {
	case "text":
		font := "3"
		return fmt.Sprintf(`TEXT %d,%d,"%s",%d,%d,%d,"%s"`, x, y, font, element.Rotation, fontScale(element.FontSizeMM), fontScale(element.FontSizeMM), content), nil
	case "barcode":
		return fmt.Sprintf(`BARCODE %d,%d,"%s",%d,%d,%d,%d,%d,"%s"`, x, y, element.Symbology, height, boolToInt(element.HumanReadable), element.Rotation, element.Narrow, element.Wide, content), nil
	case "qrcode":
		level := element.Level
		if level == "" {
			level = "M"
		}
		cell, err := dots(element.CellMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`QRCODE %d,%d,%s,%d,A,%d,"%s"`, x, y, level, cell, element.Rotation, content), nil
	case "datamatrix":
		module, err := dots(element.ModuleMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`DMATRIX %d,%d,%d,%d,x%d,r%d,"%s"`, x, y, width, height, module, element.Rotation, content), nil
	case "aztec":
		size, err := dots(element.ModuleMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`AZTEC %d,%d,%d,%d,%d,0,0,0,%d,"%s"`, x, y, element.Rotation, size, element.ErrorCorrection, boolToInt(element.WhiteOnBlack), content), nil
	case "maxicode":
		return fmt.Sprintf(`MAXICODE %d,%d,%d,"%s"`, x, y, element.MaxiCodeMode, content), nil
	case "pdf417":
		module, err := dots(element.ModuleMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`PDF417 %d,%d,%d,%d,%d,E%d,W%d,C%d,T%d,"%s"`, x, y, width, height, element.Rotation, element.Security, module, element.Columns, boolToInt(element.Truncated), content), nil
	case "composite":
		sym := map[string]string{"RSS14": "RSS14", "Truncated": "RSS14T", "Stacked": "RSS14S", "Omnidirectional": "RSS14SO", "Limited": "RSSLIM", "Expanded": "RSSEXP", "ExpandedStacked": "RSSEXP", "128_CC_AB": "UCC128CCA", "128_CC_C": "UCC128CCC"}[element.CompositeType]
		pix, err := dots(element.ModuleMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`RSS %d,%d,"%s",%d,%d,2,"%s"`, x, y, sym, element.Rotation, pix, content), nil
	case "rss":
		sym := map[string]string{"RSS14": "RSS14", "Truncated": "RSS14T", "Stacked": "RSS14S", "Omnidirectional": "RSS14SO", "Limited": "RSSLIM", "Expanded": "RSSEXP", "ExpandedStacked": "RSSEXP"}[element.RSSType]
		pix, err := dots(element.ModuleMM, dpi)
		if err != nil {
			return "", err
		}
		if element.Columns > 0 {
			return fmt.Sprintf(`RSS %d,%d,"%s",%d,%d,2,%d,"%s"`, x, y, sym, element.Rotation, pix, element.Columns, content), nil
		}
		return fmt.Sprintf(`RSS %d,%d,"%s",%d,%d,2,"%s"`, x, y, sym, element.Rotation, pix, content), nil
	case "line":
		stroke, err := dots(element.StrokeMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("BAR %d,%d,%d,%d,%d", x, y, x+width, y+height, stroke), nil
	case "box":
		stroke, err := dots(element.StrokeMM, dpi)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("BOX %d,%d,%d,%d,%d", x, y, x+width, y+height, stroke), nil
	default:
		return "", fmt.Errorf("unsupported element type %q", element.Type)
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
