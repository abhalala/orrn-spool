package main

import (
	"encoding/json"
	"html/template"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// templateFuncs returns the helper functions available to the web UI
// templates. It is shared by the router and the template parse test.
func templateFuncs() template.FuncMap {
	titleCaser := cases.Title(language.English)
	return template.FuncMap{
		"title": func(value string) string {
			return titleCaser.String(strings.ReplaceAll(value, "_", " "))
		},
		"json": func(value any) string {
			data, err := json.Marshal(value)
			if err != nil {
				return "null"
			}
			return string(data)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
	}
}
