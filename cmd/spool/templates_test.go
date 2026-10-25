package main

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/orrn/spool/internal/api/handlers"
)

// webTemplateGlob matches the pattern main.go passes to LoadHTMLGlob
// ("web/templates/**/*"), relative to this package directory.
const webTemplateGlob = "../../web/templates/*/*"

var (
	defineRe   = regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
	templateRe = regexp.MustCompile(`\{\{-?\s*template\s+"([^"]+)"`)
	// c.HTML(http.StatusOK, "name", data)
	htmlCallRe = regexp.MustCompile(`\.HTML\(\s*[^,]+,\s*"([^"]+)"`)
)

func templateFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(webTemplateGlob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates matched %s (err=%v)", webTemplateGlob, err)
	}
	return files
}

func parseWebTemplates(t *testing.T) *template.Template {
	t.Helper()
	files := templateFiles(t)
	// Parsing every file together (as gin.LoadHTMLGlob does) fails on a literal
	// "{{" inside JS/HTML text, which would otherwise panic at server startup.
	for _, f := range files {
		if _, err := template.New("").Funcs(handlers.TemplateFuncs()).ParseFiles(f); err != nil {
			t.Errorf("parse %s: %v", f, err)
		}
	}
	tpl, err := template.New("").Funcs(handlers.TemplateFuncs()).ParseGlob(webTemplateGlob)
	if err != nil {
		t.Fatalf("parse %s: %v", webTemplateGlob, err)
	}
	return tpl
}

func TestWebTemplatesParse(t *testing.T) {
	parseWebTemplates(t)
}

// Two files defining the same name silently override each other in the shared
// set (the original bug: every page defined "content"), so names must be unique.
func TestTemplateDefinesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, f := range templateFiles(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range defineRe.FindAllStringSubmatch(string(raw), -1) {
			if other, dup := seen[m[1]]; dup {
				t.Errorf("template %q defined in both %s and %s", m[1], other, filepath.Base(f))
			}
			seen[m[1]] = filepath.Base(f)
		}
	}
}

// Every {{ template "x" }} reference must point at something that is defined.
func TestTemplateReferencesResolve(t *testing.T) {
	tpl := parseWebTemplates(t)
	for _, f := range templateFiles(t) {
		raw, _ := os.ReadFile(f)
		for _, m := range templateRe.FindAllStringSubmatch(string(raw), -1) {
			if tpl.Lookup(m[1]) == nil {
				t.Errorf("%s uses {{ template %q }} which is not defined", filepath.Base(f), m[1])
			}
		}
	}
}

// handlerTemplateNames greps the Go sources for the template names passed to
// c.HTML, so a handler naming a template that does not exist fails here
// instead of returning an empty 200 at runtime.
func handlerTemplateNames(t *testing.T) []string {
	t.Helper()
	var sources []string
	for _, pattern := range []string{"../../internal/api/handlers/*.go", "../../internal/api/middleware/*.go", "*.go"} {
		m, _ := filepath.Glob(pattern)
		sources = append(sources, m...)
	}
	set := map[string]bool{}
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range htmlCallRe.FindAllStringSubmatch(string(raw), -1) {
			set[m[1]] = true
		}
	}
	if len(set) == 0 {
		t.Fatal("found no c.HTML calls; the grep is broken")
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestEveryHandlerTemplateNameExists(t *testing.T) {
	tpl := parseWebTemplates(t)
	for _, name := range handlerTemplateNames(t) {
		if tpl.Lookup(name) == nil {
			t.Errorf("a handler calls c.HTML(..., %q) but no template has that name", name)
		}
	}
}

func sampleBase(title, active string) handlers.PageBase {
	return handlers.PageBase{Title: title, Active: active, Authenticated: true,
		Toasts: []handlers.Toast{{Type: "info", Message: "hello"}}}
}

type renderCase struct {
	name string
	data any
	want []string
}

func renderCases() []renderCase {
	now := time.Now()
	started := now.Add(-3 * time.Second)
	printer := handlers.PrinterView{ID: 7, Name: "Dock-1", IPAddress: "10.0.0.5", Port: 9100, DPI: 203,
		Status: "online", LabelWidth: 50, LabelHeight: 30, Gap: 2, LastSeen: &now, TotalPrints: 12}
	tpl := handlers.TemplateView{ID: 3, Name: "Bundle label", Description: "serial label", LabelWidth: 50,
		LabelHeight: 30, Gap: 2, DPI: 203, ElementCount: 4, VariableCount: 2, UpdatedAt: now}
	queueJob := handlers.QueueJobView{ID: 11, PrinterName: "Dock-1", TemplateName: "Bundle label", Status: "pending",
		Priority: 2, Copies: 1, VariablesSummary: "serial=B-1", CreatedAt: now, StartedAt: &started}
	card := handlers.PrinterWithStatus{ID: 7, Name: "Dock-1", IPAddress: "10.0.0.5", Port: 9100, Status: "online",
		StatusClass: "bg-green-100", StatusIndicatorClass: "bg-green-400", LastSeenAt: &now,
		LastSeenAtFormatted: "just now", CanPrint: true, Warning: "paper_low"}
	editor := map[string]any{"id": 3, "name": "Bundle label", "label_width": 50, "elements": []any{}, "variables": []any{}}
	history := func(withRows bool) handlers.HistoryPage {
		h := handlers.HistoryPage{PageBase: sampleBase("Job History", "history"), JobIDs: []int64{},
			Printers: []handlers.PrinterView{printer},
			Stats: handlers.HistoryStats{TodaySuccessRate: 90.5, AvgProcessingTime: 1200,
				PrinterStats: []handlers.HistoryPrinterStat{{PrinterName: "Dock-1", Count: 4, Percentage: 80}}}}
		if withRows {
			h.Jobs = []handlers.HistoryJobView{
				{ID: 1, PrinterName: "Dock-1", TemplateName: "Bundle label", Status: "completed", CreatedAt: now, Duration: 800},
				{ID: 2, PrinterName: "Dock-1", TemplateName: "Bundle label", Status: "failed", CreatedAt: now}}
			h.JobIDs = []int64{1, 2}
			h.Pagination = &handlers.HistoryPagination{CurrentPage: 2, Total: 60, From: 26, To: 50, HasPrev: true,
				HasNext: true, PrevPage: 1, NextPage: 3, Pages: []int{1, 2, 3}}
		}
		return h
	}
	settings := func(withRows bool) handlers.SettingsPage {
		s := handlers.SettingsPage{PageBase: sampleBase("Settings", "settings"),
			Config:        handlers.SettingsConfigView{Server: handlers.SettingsServer{Port: 8080}, Database: handlers.SettingsPaths{Path: "/data/spool.db"}, Archive: handlers.SettingsPaths{Path: "/data/archives"}},
			ArchiveConfig: handlers.ArchiveConfigView{Enabled: true, Days: 30, Path: "/data/archives"},
			AIConfig:      handlers.AIConfigView{Model: "gemini-1.5-pro"}}
		if withRows {
			s.Webhooks = []handlers.WebhookView{{ID: 1, Name: "ORRN", URL: "https://orrn.example/hook", Events: []string{"job_completed"}, Enabled: true}}
			s.Archives = []handlers.ArchiveFileView{{Filename: "2026-09.db.age", Size: "1.2 MB", JobsCount: 40, CreatedAt: now}}
			s.AuditLogs = []handlers.AuditView{{ID: 1, Timestamp: now, Action: "print", EntityType: "job", EntityID: 5, IPAddress: "10.0.0.9", Details: "{}", DetailsJSON: `{"a":1}`}}
			s.Pagination = &handlers.SettingsPagination{BaseURL: "/settings/audit", CurrentPage: 1, TotalPages: 3, TotalItems: 60, StartItem: 1, EndItem: 25, PageNumbers: []int{1, 2, 3}}
		}
		return s
	}
	queue := func(withRows bool) handlers.QueuePage {
		q := handlers.QueuePage{PageBase: sampleBase("Print Queue", "queue"),
			Stats: handlers.QueueViewStats{Pending: 1, CompletedToday: 5}}
		if withRows {
			q.PendingJobs = []handlers.QueueJobView{queueJob}
			q.ProcessingJobs = []handlers.QueueJobView{{ID: 12, PrinterName: "Dock-1", TemplateName: "Bundle label", Status: "processing", Copies: 1, CreatedAt: now, StartedAt: &started}}
			q.PausedJobs = []handlers.QueueJobView{{ID: 13, PrinterName: "Dock-1", Status: "paused", Copies: 1, CreatedAt: now}}
		}
		return q
	}
	return []renderCase{
		{"dashboard", handlers.DashboardData{PageBase: sampleBase("Dashboard", "dashboard"),
			Stats:    handlers.DashboardStats{TodayPrints: 5, QueueDepth: 1, TotalPrinters: 1, OnlinePrinters: 1},
			Printers: []handlers.PrinterWithStatus{card},
			Jobs:     []handlers.JobSummary{{ID: 1, PrinterName: "Dock-1", TemplateName: "Bundle label", Status: "completed", StatusClass: "bg-green-100", Copies: 1, CreatedAtFormatted: "Oct 24, 10:00"}}},
			[]string{"Dock-1", "Printer Status", "paper_low"}},
		{"dashboard", handlers.DashboardData{PageBase: sampleBase("Dashboard", "dashboard")}, []string{"No printers"}},
		{"printer-card", card, []string{`id="printer-card-7"`, "Dock-1"}},
		{"printers", handlers.PrintersPage{PageBase: sampleBase("Printers", "printers"), Printers: []handlers.PrinterView{printer}},
			[]string{"Dock-1", "203 DPI", "50x30mm", `id="printer-grid"`}},
		{"printers", handlers.PrintersPage{PageBase: sampleBase("Printers", "printers")}, []string{"No printers configured"}},
		{"printers-grid", handlers.PrintersPage{Printers: []handlers.PrinterView{printer}}, []string{"Dock-1"}},
		{"printer-form", handlers.PrinterModalData{}, []string{"Add Printer", `name="label_width_mm"`}},
		{"printer-form", handlers.PrinterModalData{Printer: printer}, []string{"Edit Printer", "Dock-1"}},
		{"test-print", handlers.PrinterModalData{Printer: printer, Templates: []handlers.TemplateView{tpl}}, []string{"Test Print - Dock-1", "Bundle label"}},
		{"queue", queue(true), []string{"Print Queue", `data-job-id="11"`, "serial=B-1"}},
		{"queue", queue(false), []string{"Print Queue"}},
		{"queue-sections", queue(true), []string{`data-job-id="12"`, `data-job-id="13"`}},
		{"history", history(true), []string{"Job History", "#1", "#2", "Showing 26 to 50 of 60"}},
		{"history", history(false), []string{"Job History", "No jobs found"}},
		{"history-table", history(true), []string{"#2", "Failed"}},
		{"templates", handlers.TemplatesPage{PageBase: sampleBase("Label Templates", "templates"), Templates: []handlers.TemplateView{tpl}},
			[]string{"Bundle label", "4 elements"}},
		{"templates", handlers.TemplatesPage{PageBase: sampleBase("Label Templates", "templates")}, []string{"Label Templates"}},
		{"template-form", handlers.TemplateModalData{}, []string{"templateEditor(null", "Use {{variable}} syntax in content to define variables"}},
		{"template-form", handlers.TemplateModalData{Template: editor, TSPLPreview: "SIZE 50 mm"}, []string{`templateEditor({&#34;elements&#34;`, "SIZE 50 mm"}},
		{"template-preview", handlers.TemplateModalData{Template: editor, TSPLCode: "SIZE 50 mm", MergedPreview: "SIZE 50 mm"}, []string{"templatePreview("}},
		{"job-detail", handlers.JobDetailData{Job: handlers.JobDetailView{ID: 9, PrinterName: "Dock-1", PrinterIP: "10.0.0.5",
			TemplateName: "Bundle label", Status: "completed", Copies: 1, CreatedAt: now, StartedAt: &started, CompletedAt: &now, TSPLContent: "SIZE 50 mm"},
			VariablesJSON: `{"serial":"B-1"}`, WaitDuration: "1s", ProcessingDuration: "2s", TotalDuration: "3s"}, []string{"Dock-1", "SIZE 50 mm"}},
		{"new-job", map[string]any{}, []string{"newJobModal"}},
		{"settings", settings(true), []string{"Settings", "8080", "/data/spool.db", "ORRN", "2026-09.db.age", "1 archives"}},
		{"settings", settings(false), []string{"Settings", "No audit log entries found"}},
		{"audit-table", settings(true), []string{`id="audit-table"`, "10.0.0.9"}},
	}
}

func TestPagesRenderWithSampleData(t *testing.T) {
	tpl := parseWebTemplates(t)
	rendered := map[string]bool{}
	for i, rc := range renderCases() {
		var buf bytes.Buffer
		if err := tpl.ExecuteTemplate(&buf, rc.name, rc.data); err != nil {
			t.Errorf("case %d: render %q: %v", i, rc.name, err)
			continue
		}
		rendered[rc.name] = true
		out := buf.String()
		if strings.TrimSpace(out) == "" {
			t.Errorf("case %d: %q rendered an empty body", i, rc.name)
		}
		if strings.Contains(out, "<no value>") {
			t.Errorf("case %d: %q rendered <no value> (a field the template reads is missing from the data)", i, rc.name)
		}
		for _, want := range rc.want {
			if !strings.Contains(out, want) {
				t.Errorf("case %d: %q output is missing %q", i, rc.name, want)
			}
		}
		// A full page must be a complete document with the shared layout.
		if rc.name == "dashboard" || rc.name == "printers" || rc.name == "queue" || rc.name == "history" ||
			rc.name == "templates" || rc.name == "settings" {
			for _, want := range []string{"<!DOCTYPE html>", "TSC Spool", "</html>", `id="modal-container"`} {
				if !strings.Contains(out, want) {
					t.Errorf("case %d: page %q is missing layout piece %q", i, rc.name, want)
				}
			}
		}
	}
	// Every name a handler uses must have a render case, so new handlers get one.
	for _, name := range handlerTemplateNames(t) {
		if name == "login.html" {
			continue // rendered with a plain gin.H; covered below
		}
		if !rendered[name] {
			t.Errorf("handler template %q has no sample-data render case", name)
		}
	}
}

func TestLoginPageRenders(t *testing.T) {
	tpl := parseWebTemplates(t)
	for _, data := range []map[string]any{{}, {"FirstTimeSetup": true}, {"Error": "Invalid password"}} {
		var buf bytes.Buffer
		if err := tpl.ExecuteTemplate(&buf, "login.html", data); err != nil {
			t.Fatalf("login.html %v: %v", data, err)
		}
		if !strings.Contains(buf.String(), `name="password"`) {
			t.Errorf("login.html %v has no password field", data)
		}
	}
}

// The label editor must still show literal {{name}} placeholder syntax.
func TestTemplateFormShowsPlaceholderBraces(t *testing.T) {
	tpl := parseWebTemplates(t)
	var buf bytes.Buffer
	data := handlers.TemplateModalData{Template: map[string]any{}, TSPLPreview: ""}
	if err := tpl.ExecuteTemplate(&buf, "template-form", data); err != nil {
		t.Fatalf("render template-form: %v", err)
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
