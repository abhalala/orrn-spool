package localui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/orrn/spool/internal/jobs"
)

func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/edge/", http.StripPrefix("/assets/edge/", edgeAssetsHandler()))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.serveReadiness(w, r, "admin")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		s.serveReadiness(w, r, "admin")
	})
	mux.HandleFunc("/admin/login", s.loginHandler("admin", "/admin"))
	mux.HandleFunc("/admin/logout", s.logoutHandler(adminCookieName, "admin", true))
	mux.HandleFunc("/admin/session", s.sessionHandler(adminCookieName, "admin", RoleAdmin))
	mux.HandleFunc("/admin/users", s.adminUsers)
	mux.HandleFunc("/admin/diagnostics", s.adminDiagnostics)
	mux.HandleFunc("/admin/config", s.adminConfig)
	mux.HandleFunc("/admin/printers", s.printersHandler(adminCookieName, "admin", RoleAdmin))
	mux.HandleFunc("/admin/printers/", s.printerTestHandler(adminCookieName, "admin", RoleAdmin, true))
	mux.Handle("/fallback/", s.FallbackHandler())
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		s.serveApplicationPage(w, r, adminCookieName, "admin", RoleAdmin, "/admin/login", "admin.html", nil)
	})
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/" {
			http.NotFound(w, r)
			return
		}
		s.serveApplicationPage(w, r, adminCookieName, "admin", RoleAdmin, "/admin/login", "admin.html", nil)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	return withSecurityHeaders(mux)
}

func (s *Server) fallbackHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/edge/", http.StripPrefix("/assets/edge/", edgeAssetsHandler()))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.serveReadiness(w, r, "fallback")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		s.serveReadiness(w, r, "fallback")
	})
	mux.HandleFunc("/fallback/login", s.loginHandler("fallback", "/fallback/"))
	mux.HandleFunc("/fallback/logout", s.logoutHandler(fallbackCookieName, "fallback", false))
	mux.HandleFunc("/fallback/session", s.sessionHandler(fallbackCookieName, "fallback", ""))
	mux.HandleFunc("/fallback/jobs", s.localJobs)
	mux.HandleFunc("/fallback/jobs/", s.reprintJob)
	mux.HandleFunc("/fallback/printers", s.printersHandler(fallbackCookieName, "fallback", ""))
	mux.HandleFunc("/fallback/printers/", s.printerTestHandler(fallbackCookieName, "fallback", RolePrinter, false))
	for _, sectionName := range readinessCacheSections {
		section := sectionName
		mux.HandleFunc("/fallback/"+section, func(w http.ResponseWriter, r *http.Request) {
			s.cachedSection(w, r, section)
		})
	}
	mux.HandleFunc("/fallback", func(w http.ResponseWriter, r *http.Request) {
		s.serveApplicationPage(w, r, fallbackCookieName, "fallback", "", "/fallback/login", "fallback.html", nil)
	})
	mux.HandleFunc("/fallback/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fallback/" {
			http.NotFound(w, r)
			return
		}
		s.serveApplicationPage(w, r, fallbackCookieName, "fallback", "", "/fallback/login", "fallback.html", nil)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/fallback/", http.StatusSeeOther)
	})
	return withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isRevoked() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "node_revoked"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveApplicationPage(w http.ResponseWriter, r *http.Request, cookieName, scope string, requiredRole Role, loginPath, templateName string, data any) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	sess, err := s.validateSession(r.Context(), r, cookieName, scope)
	if err != nil || (requiredRole != "" && sess.Role != requiredRole) {
		http.Redirect(w, r, loginPath, http.StatusSeeOther)
		return
	}
	serveEdgePage(w, templateName, data)
}

func (s *Server) loginHandler(scope, successPath string) http.HandlerFunc {
	cookieName := fallbackCookieName
	title := "ORRN Edge fallback"
	if scope == "admin" {
		cookieName = adminCookieName
		title = "ORRN Edge administration"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, err := s.validateSession(r.Context(), r, cookieName, scope); err == nil {
				http.Redirect(w, r, successPath, http.StatusSeeOther)
				return
			}
			serveEdgePage(w, "login.html", loginPageData{Scope: scope, Title: title})
		case http.MethodPost:
			username, password := parseCredentials(r)
			if scope == "admin" && username == "" {
				username = "admin"
			}
			sess, err := s.AuthenticateUser(r.Context(), username, password, scope)
			if err != nil || (scope == "fallback" && sess.Role != RolePrinter && sess.Role != RoleViewer && sess.Role != RoleAdmin) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			setSessionCookie(w, cookieName, sess)
			writeJSON(w, http.StatusOK, map[string]any{
				"username": sess.Username, "role": sess.Role, "csrfToken": sess.CSRFToken,
			})
		default:
			methodNotAllowed(w)
		}
	}
}

func setSessionCookie(w http.ResponseWriter, name string, sess *SessionInfo) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
	})
}

func (s *Server) sessionHandler(cookieName, scope string, requiredRole Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		sess, err := s.validateSession(r.Context(), r, cookieName, scope)
		if err != nil || (requiredRole != "" && sess.Role != requiredRole) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"username": sess.Username, "role": sess.Role, "csrfToken": sess.CSRFToken, "expiresAt": sess.ExpiresAt,
		})
	}
}

func (s *Server) logoutHandler(cookieName, scope string, loopback bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		sess, err := s.validateSession(r.Context(), r, cookieName, scope)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if err := validateCSRFAndOrigin(r, sess, loopback); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
			return
		}
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM local_sessions WHERE token = ?`, sess.Token)
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authorize(w, r, adminCookieName, "admin", RoleAdmin, true)
	if !ok {
		return
	}
	_ = sess
	switch r.Method {
	case http.MethodGet:
		rows, err := s.db.QueryContext(r.Context(), `
			SELECT username, role, created_at FROM local_users
			WHERE role IN ('viewer', 'printer') ORDER BY username ASC
		`)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "users_unavailable"})
			return
		}
		defer rows.Close()
		users := make([]map[string]any, 0)
		for rows.Next() {
			var username, role, createdAt string
			if err := rows.Scan(&username, &role, &createdAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "users_unavailable"})
				return
			}
			users = append(users, map[string]any{"username": username, "role": role, "createdAt": createdAt})
		}
		if err := rows.Err(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "users_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	case http.MethodPost:
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		if err := s.CreateFallbackUser(r.Context(), body.Username, body.Password, Role(body.Role)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"username": strings.TrimSpace(body.Username), "role": body.Role})
	case http.MethodDelete:
		username := strings.TrimSpace(r.URL.Query().Get("username"))
		if username == "" || username == "admin" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid fallback username"})
			return
		}
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "user_delete_failed"})
			return
		}
		defer func() { _ = tx.Rollback() }()
		result, err := tx.ExecContext(r.Context(), `DELETE FROM local_users WHERE username = ? AND role IN ('viewer', 'printer')`, username)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM local_sessions WHERE username = ? AND scope = 'fallback'`, username)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "user_delete_failed"})
			return
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user_not_found"})
			return
		}
		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "user_delete_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) adminDiagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r, adminCookieName, "admin", RoleAdmin, false); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	recent := make([]jobView, 0)
	if s.jobStore != nil {
		items, err := s.jobStore.ListJobs(r.Context(), 25)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "diagnostics_unavailable"})
			return
		}
		recent = s.publicJobs(r.Context(), items)
	}
	freshness, err := s.cacheFreshness(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "diagnostics_unavailable"})
		return
	}
	states := map[string]int{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT state, COUNT(*) FROM edge_jobs GROUP BY state`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var state string
			var count int
			if rows.Scan(&state, &count) == nil {
				states[state] = count
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "database": true, "jobStates": states, "jobs": recent, "cache": freshness,
	})
}

func (s *Server) adminConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r, adminCookieName, "admin", RoleAdmin, false); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	config, err := s.appliedConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "config_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) localJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r, fallbackCookieName, "fallback", "", false); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.jobStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "job_store_unavailable"})
		return
	}
	limit, offset, err := cachePage(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	items, total, err := s.jobStore.ListJobsPage(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "jobs_unavailable"})
		return
	}
	var nextOffset *int
	if next := offset + len(items); len(items) > 0 && next < total {
		nextOffset = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "local", "jobs": s.publicJobs(r.Context(), items), "total": total,
		"limit": limit, "offset": offset, "nextOffset": nextOffset,
	})
}

func (s *Server) reprintJob(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authorize(w, r, fallbackCookieName, "fallback", RolePrinter, false)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/fallback/jobs/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "reprint" {
		http.NotFound(w, r)
		return
	}
	if s.jobStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "job_store_unavailable"})
		return
	}
	job, err := s.jobStore.CreateOfflineReprint(r.Context(), parts[0], sess.Username)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, jobs.ErrJobNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": publicJob(*job)})
}

func (s *Server) printersHandler(cookieName, scope string, requiredRole Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authorize(w, r, cookieName, scope, requiredRole, false); !ok {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		printers, version, err := s.printerViews(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "printers_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"configVersion": version, "printers": printers})
	}
}

func (s *Server) printerTestHandler(cookieName, scope string, requiredRole Role, loopback bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.authorize(w, r, cookieName, scope, requiredRole, loopback)
		if !ok {
			return
		}
		if sess.Role != RolePrinter && sess.Role != RoleAdmin {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "printer_role_required"})
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		prefix := "/fallback/printers/"
		if scope == "admin" {
			prefix = "/admin/printers/"
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "test" {
			http.NotFound(w, r)
			return
		}
		printerID, err := url.PathUnescape(parts[0])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_printer_id"})
			return
		}
		job, err := s.createLocalTest(r.Context(), printerID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"job": publicJob(*job)})
	}
}

func (s *Server) cachedSection(w http.ResponseWriter, r *http.Request, section string) {
	s.cachedSectionPage(w, r, section)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, cookieName, scope string, requiredRole Role, mutationLoopback bool) (*SessionInfo, bool) {
	sess, err := s.validateSession(r.Context(), r, cookieName, scope)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return nil, false
	}
	if requiredRole != "" && sess.Role != requiredRole && !(requiredRole == RolePrinter && sess.Role == RoleAdmin) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "role_required"})
		return nil, false
	}
	if err := validateCSRFAndOrigin(r, sess, mutationLoopback); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return nil, false
	}
	return sess, true
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
}
