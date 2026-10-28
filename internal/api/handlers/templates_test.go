package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

func templateTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	f := newKeyFixture(t)
	RegisterTemplateRoutes(f.r.Group("/api"), NewTemplateHandler(db.GetDB(), core.NewTSPL2Generator(), f.queue))
	return f.r
}

func templateRequest(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeTemplate(t *testing.T, w *httptest.ResponseRecorder, status int) TemplateResponse {
	t.Helper()
	if w.Code != status {
		t.Fatalf("template response: %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var template TemplateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &template); err != nil {
		t.Fatal(err)
	}
	if template.SchemaJSON == "" {
		t.Fatalf("response omitted schema_json: %s", w.Body.String())
	}
	return template
}

func TestORRNTemplateSchemaRoundTrip(t *testing.T) {
	r := templateTestRouter(t)
	created := decodeTemplate(t, templateRequest(t, r, http.MethodPost, "/api/templates", map[string]any{
		"name": t.Name(), "description": "production wire format", "schema_json": keyTestSchema, "width_mm": 50, "height_mm": 30,
	}), http.StatusCreated)
	path := fmt.Sprintf("/api/templates/%d", created.ID)
	if created.SchemaJSON != keyTestSchema || created.WidthMM != 50 || created.HeightMM != 30 {
		t.Fatalf("create did not preserve schema and dimensions: %+v", created)
	}

	var schema core.LabelSchema
	if err := json.Unmarshal([]byte(keyTestSchema), &schema); err != nil {
		t.Fatal(err)
	}
	schema.WidthMM, schema.HeightMM = 60, 40
	schema.Elements[0].Content = "Bundle {{serial}}"
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	updated := decodeTemplate(t, templateRequest(t, r, http.MethodPut, path, map[string]any{
		"schema_json": string(raw), "width_mm": 60, "height_mm": 40,
	}), http.StatusOK)
	if updated.SchemaJSON != string(raw) || updated.WidthMM != 60 || updated.HeightMM != 40 {
		t.Fatalf("update did not persist intended schema and dimensions: %+v", updated)
	}
	stored, err := db.Templates.GetTemplateByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SchemaJSON != updated.SchemaJSON || stored.WidthMM != 60 || stored.HeightMM != 40 {
		t.Fatalf("stored template differs from response: %+v", stored)
	}
	loaded := decodeTemplate(t, templateRequest(t, r, http.MethodGet, path, nil), http.StatusOK)
	if loaded.SchemaJSON != updated.SchemaJSON {
		t.Fatalf("GET returned a different schema: %+v", loaded)
	}
	list := templateRequest(t, r, http.MethodGet, "/api/templates", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	var templates []TemplateResponse
	if err := json.Unmarshal(list.Body.Bytes(), &templates); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range templates {
		if item.ID == created.ID {
			found = true
			if item.SchemaJSON != updated.SchemaJSON {
				t.Fatalf("list omitted or changed schema_json: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("list omitted created template")
	}

	editor := toEditorTemplate(stored)
	if editor.LabelWidth != 60 || editor.LabelHeight != 40 || len(editor.Elements) != 1 || editor.Elements[0].Content != schema.Elements[0].Content || editor.Elements[0].Font != "3" {
		t.Fatalf("editor loaded unexpected dimensions or elements: %+v", editor)
	}
	if len(editor.Variables) != 1 || editor.Variables[0].Name != "serial" || !editor.Variables[0].Required {
		t.Fatalf("editor lost variable definitions: %+v", editor.Variables)
	}
	preview, err := core.NewTSPL2Generator().Generate(&schema, map[string]string{"serial": "B-123"})
	if err != nil || !strings.Contains(preview, "SIZE 60.00 mm, 40.00 mm") || !strings.Contains(preview, "Bundle B-123") {
		t.Fatalf("saved schema does not generate the intended label: %q, %v", preview, err)
	}
}

func TestTemplateMetadataAndSchemaOnlyUpdates(t *testing.T) {
	r := templateTestRouter(t)
	created := decodeTemplate(t, templateRequest(t, r, http.MethodPost, "/api/templates", map[string]any{
		"name": t.Name(), "description": "clear me", "schema_json": keyTestSchema, "width_mm": 50, "height_mm": 30,
	}), http.StatusCreated)
	path := fmt.Sprintf("/api/templates/%d", created.ID)
	metadata := decodeTemplate(t, templateRequest(t, r, http.MethodPut, path, map[string]any{
		"name": t.Name() + " renamed", "description": "",
	}), http.StatusOK)
	if metadata.Name != t.Name()+" renamed" || metadata.Description != "" || metadata.SchemaJSON != keyTestSchema || metadata.WidthMM != 50 || metadata.HeightMM != 30 {
		t.Fatalf("metadata update changed schema or failed to apply metadata: %+v", metadata)
	}
	changedSchema := strings.Replace(keyTestSchema, "{{serial}}", "Updated {{serial}}", 1)
	updated := decodeTemplate(t, templateRequest(t, r, http.MethodPut, path, map[string]any{
		"schema_json": changedSchema,
	}), http.StatusOK)
	if updated.SchemaJSON != changedSchema || updated.WidthMM != 50 || updated.HeightMM != 30 {
		t.Fatalf("schema-only update was ignored: %+v", updated)
	}
}

func TestInvalidTemplateWritesDoNotReplaceStoredSchema(t *testing.T) {
	r := templateTestRouter(t)
	created := decodeTemplate(t, templateRequest(t, r, http.MethodPost, "/api/templates", map[string]any{
		"name": t.Name(), "description": "unchanged", "schema_json": keyTestSchema, "width_mm": 50, "height_mm": 30,
	}), http.StatusCreated)
	path := fmt.Sprintf("/api/templates/%d", created.ID)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"malformed JSON", map[string]any{"schema_json": "{"}},
		{"empty schema string", map[string]any{"schema_json": ""}},
		{"null schema", map[string]any{"schema_json": "null"}},
		{"empty schema", map[string]any{"schema_json": "{}"}},
		{"no elements", map[string]any{"schema_json": `{"width_mm":50,"height_mm":30,"elements":[]}`}},
		{"unsupported element", map[string]any{"schema_json": strings.Replace(keyTestSchema, `"text"`, `"unknown"`, 1)}},
		{"missing element coordinate", map[string]any{"schema_json": strings.Replace(keyTestSchema, `"x":10,`, "", 1)}},
		{"wrong coordinate type", map[string]any{"schema_json": strings.Replace(keyTestSchema, `"x":10`, `"x":"ten"`, 1)}},
		{"wrong font type", map[string]any{"schema_json": strings.Replace(keyTestSchema, `"font":"3"`, `"font":3`, 1)}},
		{"zero schema width", map[string]any{"schema_json": strings.Replace(keyTestSchema, `"width_mm":50`, `"width_mm":0`, 1)}},
		{"dimension disagreement", map[string]any{"schema_json": keyTestSchema, "width_mm": 60}},
		{"dimension-only disagreement", map[string]any{"height_mm": 40}},
		{"zero width", map[string]any{"width_mm": 0}},
		{"negative height", map[string]any{"height_mm": -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			update := map[string]any{"name": t.Name(), "description": "must not persist"}
			create := map[string]any{"name": t.Name(), "schema_json": keyTestSchema, "width_mm": 50, "height_mm": 30}
			for key, value := range tc.body {
				update[key], create[key] = value, value
			}
			for _, request := range []struct {
				method, path string
				body         map[string]any
			}{
				{http.MethodPost, "/api/templates", create},
				{http.MethodPut, path, update},
			} {
				w := templateRequest(t, r, request.method, request.path, request.body)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid template: %d, want 400: %s", request.method, w.Code, w.Body.String())
				}
			}
			stored, err := db.Templates.GetTemplateByID(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Name != created.Name || stored.Description != created.Description || stored.SchemaJSON != keyTestSchema || stored.WidthMM != 50 || stored.HeightMM != 30 {
				t.Fatalf("invalid update changed stored template: %+v", stored)
			}
			if _, err := db.Templates.GetTemplateByName(context.Background(), t.Name()); err == nil {
				t.Fatal("invalid create persisted a template")
			}
		})
	}
}
