package handlers

import (
	"encoding/json"
	"html/template"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TemplateFuncs returns the helper functions available to the web UI
// templates. It is shared by the router and the template tests.
func TemplateFuncs() template.FuncMap {
	titleCaser := cases.Title(language.English)
	return template.FuncMap{
		"title": func(value string) string {
			return titleCaser.String(strings.ReplaceAll(value, "_", " "))
		},
		// json marshals a value for use inside a script or an Alpine
		// attribute. It returns template.JS so html/template inserts the
		// object itself instead of quoting it as a JS string.
		"json": func(value any) template.JS {
			data, err := json.Marshal(value)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(data)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
	}
}
