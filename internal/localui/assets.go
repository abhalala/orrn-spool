package localui

import (
	"html/template"
	"io/fs"
	"net/http"

	"github.com/orrn/spool/web"
)

var edgeTemplates = template.Must(template.ParseFS(
	web.Assets,
	"templates/edge/login.html",
	"templates/edge/admin.html",
	"templates/edge/fallback.html",
))

type loginPageData struct {
	Scope string
	Title string
}

func serveEdgePage(w http.ResponseWriter, name string, data any) {
	setSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := edgeTemplates.ExecuteTemplate(w, name, data); err != nil {
		// The template is parsed at process start. Execution errors here are I/O
		// errors after the response has begun, so there is no safe second response.
		return
	}
}

func edgeAssetsHandler() http.Handler {
	assets, err := fs.Sub(web.Assets, "static/edge")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		w.Header().Set("Cache-Control", "public, max-age=300")
		files.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}
