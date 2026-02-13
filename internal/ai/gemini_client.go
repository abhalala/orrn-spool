package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/orrn/spool/internal/core"
)

const systemPrompt = `You are a label design expert for TSC thermal printers. You generate JSON schemas for labels that will be converted to TSPL2 commands.

OUTPUT FORMAT: Return ONLY valid JSON matching this schema:
{
  "name": "Label Name",
  "width_mm": 100,
  "height_mm": 50,
  "gap_mm": 2,
  "dpi": 203,
  "elements": [
    {"type": "text", "x": 10, "y": 5, "font": "3", "rotation": 0, "x_scale": 1, "y_scale": 1, "content": "{{product_name}}"},
    {"type": "barcode", "x": 10, "y": 20, "symbology": "128", "height": 80, "rotation": 0, "narrow": 2, "wide": 2, "content": "{{barcode}}"},
    {"type": "qrcode", "x": 80, "y": 10, "level": "M", "cell_width": 4, "rotation": 0, "content": "{{qr_data}}"},
    {"type": "box", "x": 5, "y": 5, "x_end": 95, "y_end": 45, "thickness": 2}
  ],
  "variables": {
    "product_name": {"type": "string", "required": true},
    "barcode": {"type": "string", "required": true},
    "qr_data": {"type": "string", "required": false, "default": ""}
  }
}

COORDINATES: x,y are in dots. For 203 DPI: 1mm = 8 dots. For 300 DPI: 1mm = 12 dots.
ELEMENT TYPES: text, barcode, qrcode, pdf417, datamatrix, box, line, circle, ellipse, block, image
BARCODE TYPES: 128, EAN13, EAN8, UPC, 39, CODABAR, etc.
FONTS: 1=8x12, 2=12x20, 3=16x24, 4=24x32, 5=32x48 dots

RULES:
1. Use {{variable_name}} for dynamic content in text/barcode/qrcode content
2. Define all variables in the variables object
3. Position elements within label bounds (x + element_width < width_mm * dpi/25.4)
4. Leave 2-3mm margin from edges
5. Use appropriate element types for the content
6. Include descriptive variable names
7. Add a border box for professional look when appropriate`

type GeminiClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string
}

type GenerateRequest struct {
	Description string  `json:"description"`
	Image       string  `json:"image,omitempty"`
	WidthMM     float64 `json:"width_mm,omitempty"`
	HeightMM    float64 `json:"height_mm,omitempty"`
	DPI         int     `json:"dpi,omitempty"`
}

type GenerateResponse struct {
	Schema      core.LabelSchema `json:"schema"`
	RawResponse string           `json:"raw_response,omitempty"`
}

type GeminiAPIRequest struct {
	Contents         []Content         `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig"`
}

type Content struct {
	Parts []Part `json:"parts"`
	Role  string `json:"role,omitempty"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GenerationConfig struct {
	Temperature      float64 `json:"temperature"`
	ResponseMimeType string  `json:"responseMimeType"`
}

type GeminiAPIResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type GeminiError struct {
	Code    int
	Message string
	Status  string
}

func (e *GeminiError) Error() string {
	return fmt.Sprintf("gemini api error: %s (status: %s, code: %d)", e.Message, e.Status, e.Code)
}

func NewGeminiClient() *GeminiClient {
	return &GeminiClient{
		model:  "gemini-2.0-flash",
		baseURL: "https://generativelanguage.googleapis.com/v1beta",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *GeminiClient) SetAPIKey(key string) {
	c.apiKey = key
}

func (c *GeminiClient) SetModel(model string) {
	if model != "" {
		c.model = model
	}
}

func (c *GeminiClient) GenerateLabel(ctx context.Context, req *GenerateRequest) (*core.LabelSchema, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("gemini api key not configured")
	}

	apiReq, err := c.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var geminiResp GeminiAPIResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if geminiResp.Error != nil {
		return nil, &GeminiError{
			Code:    geminiResp.Error.Code,
			Message: geminiResp.Error.Message,
			Status:  geminiResp.Error.Status,
		}
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from gemini")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text
	schema, err := c.parseResponse([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("failed to parse label schema: %w", err)
	}

	return schema, nil
}

func (c *GeminiClient) TestConnection(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("api key not configured")
	}

	req := &GenerateRequest{
		Description: "Create a simple test label",
		WidthMM:     50,
		HeightMM:    25,
		DPI:         203,
	}

	_, err := c.GenerateLabel(ctx, req)
	if err != nil {
		if apiErr, ok := err.(*GeminiError); ok {
			if apiErr.Status == "INVALID_ARGUMENT" && strings.Contains(apiErr.Message, "API key") {
				return fmt.Errorf("invalid api key")
			}
		}
		return err
	}

	return nil
}

func (c *GeminiClient) buildRequest(req *GenerateRequest) (*GeminiAPIRequest, error) {
	var parts []Part

	promptBuilder := &strings.Builder{}
	promptBuilder.WriteString(systemPrompt)
	promptBuilder.WriteString("\n\n")

	if req.WidthMM > 0 && req.HeightMM > 0 {
		promptBuilder.WriteString(fmt.Sprintf("LABEL SIZE: %.1fmm x %.1fmm\n", req.WidthMM, req.HeightMM))
	}
	if req.DPI > 0 {
		promptBuilder.WriteString(fmt.Sprintf("DPI: %d\n", req.DPI))
	}

	promptBuilder.WriteString(fmt.Sprintf("\nUSER REQUEST: %s\n", req.Description))
	promptBuilder.WriteString("\nGenerate the label schema JSON now. Return ONLY valid JSON, no markdown formatting.")

	parts = append(parts, Part{Text: promptBuilder.String()})

	if req.Image != "" {
		mimeType := "image/png"
		if len(req.Image) > 10 {
			header := strings.ToLower(req.Image[:10])
			if strings.Contains(header, "/9j/") {
				mimeType = "image/jpeg"
			} else if strings.Contains(header, "ivbor") {
				mimeType = "image/png"
			} else if strings.Contains(header, "r0lgod") {
				mimeType = "image/gif"
			}
		}
		imageData := req.Image
		if strings.Contains(req.Image, ",") {
			parts := strings.SplitN(req.Image, ",", 2)
			if len(parts) == 2 {
				imageData = parts[1]
			}
		}

		imagePart := Part{
			InlineData: &InlineData{
				MimeType: mimeType,
				Data:     imageData,
			},
		}
		parts = append(parts, imagePart)
		parts = append(parts, Part{Text: "Analyze the image above and create a similar label schema based on the user's description. Maintain the layout and style from the image."})
	}

	return &GeminiAPIRequest{
		Contents: []Content{
			{
				Parts: parts,
			},
		},
		GenerationConfig: GenerationConfig{
			Temperature:      0.7,
			ResponseMimeType: "application/json",
		},
	}, nil
}

func (c *GeminiClient) parseResponse(body []byte) (*core.LabelSchema, error) {
	text := string(body)

	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("no valid json object found in response")
	}
	jsonStr := text[start : end+1]

	var schema core.LabelSchema
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		return nil, fmt.Errorf("failed to parse json: %w", err)
	}

	if schema.DPI == 0 {
		schema.DPI = 203
	}
	if schema.GapMM == 0 {
		schema.GapMM = 2
	}
	if schema.Variables == nil {
		schema.Variables = make(map[string]core.VariableDef)
	}

	if err := c.validateSchema(&schema); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}

	return &schema, nil
}

func (c *GeminiClient) validateSchema(schema *core.LabelSchema) error {
	if schema.WidthMM <= 0 {
		return fmt.Errorf("width_mm must be greater than 0")
	}
	if schema.HeightMM <= 0 {
		return fmt.Errorf("height_mm must be greater than 0")
	}
	if len(schema.Elements) == 0 {
		return fmt.Errorf("schema must have at least one element")
	}

	validTypes := map[string]bool{
		"text":      true,
		"barcode":   true,
		"qrcode":    true,
		"pdf417":    true,
		"datamatrix": true,
		"box":       true,
		"line":      true,
		"circle":    true,
		"ellipse":   true,
		"block":     true,
		"image":     true,
	}

	for i, elem := range schema.Elements {
		if !validTypes[elem.Type] {
			return fmt.Errorf("element[%d]: invalid type '%s'", i, elem.Type)
		}
	}

	return nil
}

func (c *GeminiClient) GetModel() string {
	return c.model
}

func (c *GeminiClient) IsConfigured() bool {
	return c.apiKey != ""
}
