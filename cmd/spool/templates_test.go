package main

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
)

// webTemplateGlob matches the pattern main.go passes to LoadHTMLGlob
// ("web/templates/**/*"), relative to this package directory.
const webTemplateGlob = "../../web/templates/*/*"

func parseWebTemplates(t *testing.T) *template.Template {
	t.Helper()
	files, err := filepath.Glob(webTemplateGlob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates matched %s (err=%v)", webTemplateGlob, err)
	}
	// Parsing every file together (as gin.LoadHTMLGlob does) fails on a literal
	// "{{" inside JS/HTML text, which would otherwise panic at server startup.
	for _, f := range files {
		if _, err := template.New("").Funcs(templateFuncs()).ParseFiles(f); err != nil {
			t.Errorf("parse %s: %v", f, err)
		}
	}
	tpl, err := template.New("").Funcs(templateFuncs()).ParseGlob(webTemplateGlob)
	if err != nil {
		t.Fatalf("parse %s: %v", webTemplateGlob, err)
	}
	return tpl
}

func TestWebTemplatesParse(t *testing.T) {
	parseWebTemplates(t)
}

// The label editor must still show literal {{name}} placeholder syntax.
func TestTemplateFormShowsPlaceholderBraces(t *testing.T) {
	// Parse the file alone: several files define "modal-content", so the
	// combined glob keeps only whichever file sorts last.
	tpl, err := template.New("").Funcs(templateFuncs()).ParseFiles("../../web/templates/modals/template_form.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	data := map[string]any{"Template": map[string]any{}, "TSPLPreview": ""}
	if err := tpl.ExecuteTemplate(&buf, "modal-content", data); err != nil {
		t.Fatalf("render modal-content: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`x-text="'{{' + variable.name + '}}'"`,
		`Use {{variable}} syntax in content to define variables`,
		`placeholder="Use {{variable}} for dynamic text"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}
