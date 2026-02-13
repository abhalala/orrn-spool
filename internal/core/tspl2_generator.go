package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type LabelSchema struct {
	Name      string                 `json:"name"`
	WidthMM   float64                `json:"width_mm"`
	HeightMM  float64                `json:"height_mm"`
	GapMM     float64                `json:"gap_mm"`
	DPI       int                    `json:"dpi"`
	Elements  []LabelElement         `json:"elements"`
	Variables map[string]VariableDef `json:"variables"`
}

type LabelElement struct {
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`

	Content   string `json:"content,omitempty"`
	Font      string `json:"font,omitempty"`
	Rotation  int    `json:"rotation,omitempty"`
	XScale    int    `json:"x_scale,omitempty"`
	YScale    int    `json:"y_scale,omitempty"`

	Symbology string `json:"symbology,omitempty"`
	Height    int    `json:"height,omitempty"`
	Narrow    int    `json:"narrow,omitempty"`
	Wide      int    `json:"wide,omitempty"`

	Level     string `json:"level,omitempty"`
	CellWidth int    `json:"cell_width,omitempty"`

	XEnd      int `json:"x_end,omitempty"`
	YEnd      int `json:"y_end,omitempty"`
	Thickness int `json:"thickness,omitempty"`

	X1 int `json:"x1,omitempty"`
	Y1 int `json:"y1,omitempty"`
	X2 int `json:"x2,omitempty"`
	Y2 int `json:"y2,omitempty"`

	Radius int `json:"radius,omitempty"`

	XRadius int `json:"x_radius,omitempty"`
	YRadius int `json:"y_radius,omitempty"`

	Columns    int `json:"columns,omitempty"`
	Rows       int `json:"rows,omitempty"`
	Security   int `json:"security,omitempty"`
	ModuleSize int `json:"module_size,omitempty"`

	Encoding string `json:"encoding,omitempty"`

	ImagePath string `json:"image_path,omitempty"`

	Width  int `json:"width,omitempty"`
	Spacing int `json:"spacing,omitempty"`
}

type VariableDef struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
}

type TSPL2Generator struct{}

func NewTSPL2Generator() *TSPL2Generator {
	return &TSPL2Generator{}
}

func (g *TSPL2Generator) ParseSchema(jsonStr string) (*LabelSchema, error) {
	var schema LabelSchema
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema JSON: %w", err)
	}
	if schema.DPI == 0 {
		schema.DPI = 203
	}
	return &schema, nil
}

func (g *TSPL2Generator) ValidateVariables(schema *LabelSchema, variables map[string]string) error {
	for name, def := range schema.Variables {
		value, provided := variables[name]
		if !provided || value == "" {
			if def.Required && def.Default == "" {
				return fmt.Errorf("required variable '%s' is missing", name)
			}
		}
	}
	return nil
}

func (g *TSPL2Generator) substituteVariables(content string, variables map[string]string, schema *LabelSchema) string {
	result := content
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)

	matches := re.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		varName := match[1]
		value, provided := variables[varName]
		if !provided || value == "" {
			if def, exists := schema.Variables[varName]; exists {
				value = def.Default
			}
		}
		result = strings.ReplaceAll(result, match[0], value)
	}
	return result
}

func (g *TSPL2Generator) Generate(schema *LabelSchema, variables map[string]string) (string, error) {
	if err := g.ValidateVariables(schema, variables); err != nil {
		return "", err
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("SIZE %.0f mm, %.0f mm\n", schema.WidthMM, schema.HeightMM))
	sb.WriteString(fmt.Sprintf("GAP %.0f mm, 0 mm\n", schema.GapMM))
	sb.WriteString("DIRECTION 0\n")
	sb.WriteString("CLS\n")

	for _, elem := range schema.Elements {
		cmd, err := g.generateElement(&elem, variables, schema)
		if err != nil {
			return "", fmt.Errorf("error generating %s element: %w", elem.Type, err)
		}
		if cmd != "" {
			sb.WriteString(cmd)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("PRINT 1\n")
	return sb.String(), nil
}

func (g *TSPL2Generator) generateElement(elem *LabelElement, variables map[string]string, schema *LabelSchema) (string, error) {
	switch elem.Type {
	case "text":
		return g.generateText(elem, variables, schema), nil
	case "barcode":
		return g.generateBarcode(elem, variables, schema), nil
	case "qrcode":
		return g.generateQRCode(elem, variables, schema), nil
	case "pdf417":
		return g.generatePDF417(elem, variables, schema), nil
	case "datamatrix":
		return g.generateDataMatrix(elem, variables, schema), nil
	case "box":
		return g.generateBox(elem), nil
	case "line":
		return g.generateLine(elem), nil
	case "circle":
		return g.generateCircle(elem), nil
	case "ellipse":
		return g.generateEllipse(elem), nil
	case "block":
		return g.generateBlock(elem, variables, schema), nil
	case "image":
		return g.generateImage(elem), nil
	default:
		return "", fmt.Errorf("unsupported element type: %s", elem.Type)
	}
}

func (g *TSPL2Generator) generateText(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	font := elem.Font
	if font == "" {
		font = "3"
	}
	xScale := elem.XScale
	if xScale == 0 {
		xScale = 1
	}
	yScale := elem.YScale
	if yScale == 0 {
		yScale = 1
	}
	return fmt.Sprintf(`TEXT %d,%d,"%s",%d,%d,%d,"%s"`, elem.X, elem.Y, font, elem.Rotation, xScale, yScale, content)
}

func (g *TSPL2Generator) generateBarcode(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	symbology := elem.Symbology
	if symbology == "" {
		symbology = "128"
	}
	height := elem.Height
	if height == 0 {
		height = 80
	}
	narrow := elem.Narrow
	if narrow == 0 {
		narrow = 2
	}
	wide := elem.Wide
	if wide == 0 {
		wide = 2
	}
	return fmt.Sprintf(`BARCODE %d,%d,"%s",%d,%d,%d,%d,%d,"%s"`,
		elem.X, elem.Y, symbology, height, elem.Rotation, narrow, wide, narrow, content)
}

func (g *TSPL2Generator) generateQRCode(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	level := elem.Level
	if level == "" {
		level = "M"
	}
	cellWidth := elem.CellWidth
	if cellWidth == 0 {
		cellWidth = 4
	}
	return fmt.Sprintf(`QRCODE %d,%d,%s,%d,%d,A,"%s"`, elem.X, elem.Y, level, cellWidth, elem.Rotation, content)
}

func (g *TSPL2Generator) generatePDF417(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	columns := elem.Columns
	if columns == 0 {
		columns = 3
	}
	rows := elem.Rows
	if rows == 0 {
		rows = 0
	}
	security := elem.Security
	if security == 0 {
		security = 0
	}
	moduleSize := elem.ModuleSize
	if moduleSize == 0 {
		moduleSize = 2
	}
	return fmt.Sprintf(`PDF417 %d,%d,%d,%d,%d,%d,%d,"%s"`,
		elem.X, elem.Y, columns, rows, security, moduleSize, elem.Rotation, content)
}

func (g *TSPL2Generator) generateDataMatrix(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	moduleSize := elem.ModuleSize
	if moduleSize == 0 {
		moduleSize = 2
	}
	encoding := elem.Encoding
	if encoding == "" {
		encoding = "A"
	}
	return fmt.Sprintf(`DMATRIX %d,%d,%d,%d,%s,"%s"`, elem.X, elem.Y, moduleSize, elem.Rotation, encoding, content)
}

func (g *TSPL2Generator) generateBox(elem *LabelElement) string {
	thickness := elem.Thickness
	if thickness == 0 {
		thickness = 1
	}
	return fmt.Sprintf("BOX %d,%d,%d,%d,%d", elem.X, elem.Y, elem.XEnd, elem.YEnd, thickness)
}

func (g *TSPL2Generator) generateLine(elem *LabelElement) string {
	thickness := elem.Thickness
	if thickness == 0 {
		thickness = 1
	}
	return fmt.Sprintf("BAR %d,%d,%d,%d,%d", elem.X1, elem.Y1, elem.X2, elem.Y2, thickness)
}

func (g *TSPL2Generator) generateCircle(elem *LabelElement) string {
	thickness := elem.Thickness
	if thickness == 0 {
		thickness = 1
	}
	return fmt.Sprintf("CIRCLE %d,%d,%d,%d", elem.X, elem.Y, elem.Radius, thickness)
}

func (g *TSPL2Generator) generateEllipse(elem *LabelElement) string {
	thickness := elem.Thickness
	if thickness == 0 {
		thickness = 1
	}
	return fmt.Sprintf("ELLIPSE %d,%d,%d,%d,%d", elem.X, elem.Y, elem.XRadius, elem.YRadius, thickness)
}

func (g *TSPL2Generator) generateBlock(elem *LabelElement, variables map[string]string, schema *LabelSchema) string {
	content := g.substituteVariables(elem.Content, variables, schema)
	content = escapeTSPLString(content)
	font := elem.Font
	if font == "" {
		font = "3"
	}
	xScale := elem.XScale
	if xScale == 0 {
		xScale = 1
	}
	yScale := elem.YScale
	if yScale == 0 {
		yScale = 1
	}
	return fmt.Sprintf(`BLOCK %d,%d,%d,%d,"%s",%d,%d,%d,"%s"`,
		elem.X, elem.Y, elem.Width, elem.Height, font, elem.Rotation, xScale, yScale, content)
}

func (g *TSPL2Generator) generateImage(elem *LabelElement) string {
	return fmt.Sprintf(`PUTBMP %d,%d,"%s"`, elem.X, elem.Y, elem.ImagePath)
}

func (g *TSPL2Generator) GeneratePreview(schema *LabelSchema) (string, error) {
	previewVars := make(map[string]string)
	for name, def := range schema.Variables {
		if def.Default != "" {
			previewVars[name] = def.Default
		} else {
			switch def.Type {
			case "string":
				previewVars[name] = "SAMPLE"
			case "number":
				previewVars[name] = "123"
			case "barcode":
				previewVars[name] = "12345678"
			default:
				previewVars[name] = "SAMPLE"
			}
		}
	}
	return g.Generate(schema, previewVars)
}

func mmToDots(mm float64, dpi int) int {
	dotsPerMM := float64(dpi) / 25.4
	return int(mm * dotsPerMM)
}

func escapeTSPLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

func (g *TSPL2Generator) GenerateWithDotCoordinates(schema *LabelSchema, variables map[string]string, useDotCoords bool) (string, error) {
	if !useDotCoords {
		return g.Generate(schema, variables)
	}

	if err := g.ValidateVariables(schema, variables); err != nil {
		return "", err
	}

	var sb strings.Builder
	dpi := schema.DPI
	if dpi == 0 {
		dpi = 203
	}

	widthDots := mmToDots(schema.WidthMM, dpi)
	heightDots := mmToDots(schema.HeightMM, dpi)
	gapDots := mmToDots(schema.GapMM, dpi)

	sb.WriteString(fmt.Sprintf("SIZE %d dot,%d dot\n", widthDots, heightDots))
	sb.WriteString(fmt.Sprintf("GAP %d dot,0 dot\n", gapDots))
	sb.WriteString("DIRECTION 0\n")
	sb.WriteString("CLS\n")

	for _, elem := range schema.Elements {
		cmd, err := g.generateElement(&elem, variables, schema)
		if err != nil {
			return "", fmt.Errorf("error generating %s element: %w", elem.Type, err)
		}
		if cmd != "" {
			sb.WriteString(cmd)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("PRINT 1\n")
	return sb.String(), nil
}

func (g *TSPL2Generator) GenerateMultiLabel(schema *LabelSchema, labelDataList []map[string]string, copies int) (string, error) {
	if copies <= 0 {
		copies = 1
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("SIZE %.0f mm, %.0f mm\n", schema.WidthMM, schema.HeightMM))
	sb.WriteString(fmt.Sprintf("GAP %.0f mm, 0 mm\n", schema.GapMM))
	sb.WriteString("DIRECTION 0\n")

	for _, variables := range labelDataList {
		if err := g.ValidateVariables(schema, variables); err != nil {
			return "", err
		}

		sb.WriteString("CLS\n")
		for _, elem := range schema.Elements {
			cmd, err := g.generateElement(&elem, variables, schema)
			if err != nil {
				return "", fmt.Errorf("error generating %s element: %w", elem.Type, err)
			}
			if cmd != "" {
				sb.WriteString(cmd)
				sb.WriteString("\n")
			}
		}
		sb.WriteString(fmt.Sprintf("PRINT %d\n", copies))
	}

	return sb.String(), nil
}

func (g *TSPL2Generator) GetVariables(schema *LabelSchema) map[string]VariableDef {
	return schema.Variables
}

func (g *TSPL2Generator) GetRequiredVariables(schema *LabelSchema) []string {
	var required []string
	for name, def := range schema.Variables {
		if def.Required {
			required = append(required, name)
		}
	}
	return required
}

func (g *TSPL2Generator) MergeVariablesWithDefaults(schema *LabelSchema, variables map[string]string) map[string]string {
	result := make(map[string]string)
	for name, def := range schema.Variables {
		if def.Default != "" {
			result[name] = def.Default
		}
	}
	for name, value := range variables {
		result[name] = value
	}
	return result
}

func ParseDPIStr(dpiStr string) (int, error) {
	dpi, err := strconv.Atoi(dpiStr)
	if err != nil {
		return 0, fmt.Errorf("invalid DPI value: %s", dpiStr)
	}
	if dpi != 203 && dpi != 300 && dpi != 600 {
		return 0, fmt.Errorf("unsupported DPI: %d (supported: 203, 300, 600)", dpi)
	}
	return dpi, nil
}

func GetDotsPerMM(dpi int) float64 {
	switch dpi {
	case 203:
		return 8.0
	case 300:
		return 12.0
	case 600:
		return 24.0
	default:
		return float64(dpi) / 25.4
	}
}
