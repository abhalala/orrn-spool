package localui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/edgeclient"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
)

type Role string

const (
	RoleAdmin   Role = "admin"
	RolePrinter Role = "printer"
	RoleViewer  Role = "viewer"
)

const (
	adminCookieName    = "orrn_edge_admin_session"
	fallbackCookieName = "orrn_edge_fallback_session"
	csrfHeaderName     = "X-CSRF-Token"
)

type SessionInfo struct {
	Token     string
	Username  string
	Role      Role
	CSRFToken string
	ExpiresAt time.Time
}

type Server struct {
	db              *sql.DB
	jobStore        *jobs.Store
	cacheStore      *cache.Store
	nodeStore       *node.Store
	printerReporter func() []edgeclient.ReportedPrinter
}

func NewServer(ctx context.Context, sqlDB *sql.DB, jobStore *jobs.Store, cacheStore *cache.Store) (*Server, error) {
	if sqlDB == nil {
		return nil, errors.New("sql.DB is required")
	}
	srv := &Server{
		db:         sqlDB,
		jobStore:   jobStore,
		cacheStore: cacheStore,
	}
	if err := srv.initSchema(ctx); err != nil {
		return nil, err
	}
	return srv, nil
}

// WithNodeStore connects the enrolled identity used by local revocation and
// readiness checks. It is optional so the UI can still explain an unenrolled
// node instead of failing during construction.
func (s *Server) WithNodeStore(store *node.Store) *Server {
	s.nodeStore = store
	return s
}

func (s *Server) initSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS local_users (
	username TEXT PRIMARY KEY,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS local_sessions (
	token TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	role TEXT NOT NULL,
	csrf_token TEXT NOT NULL,
	scope TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS local_readiness_probe (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	updated_at TEXT NOT NULL
);
`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) BootstrapAdmin(ctx context.Context, password string) error {
	if len(strings.TrimSpace(password)) < 8 {
		return errors.New("admin password must be at least 8 characters")
	}
	return s.upsertUser(ctx, "admin", password, RoleAdmin)
}

func (s *Server) CreateFallbackUser(ctx context.Context, username string, password string, role Role) error {
	username = strings.TrimSpace(username)
	if username == "" || username == "admin" {
		return errors.New("invalid fallback username")
	}
	if role != RolePrinter && role != RoleViewer {
		return fmt.Errorf("invalid fallback role %q", role)
	}
	if len(strings.TrimSpace(password)) < 4 {
		return errors.New("fallback password/PIN must be at least 4 characters")
	}
	return s.upsertUser(ctx, username, password, role)
}

func (s *Server) upsertUser(ctx context.Context, username, password string, role Role) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO local_users (username, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			password_hash = excluded.password_hash,
			role = excluded.role,
			updated_at = excluded.updated_at
	`, username, string(hash), string(role), now, now)
	return err
}

func (s *Server) AuthenticateUser(ctx context.Context, username, password, scope string) (*SessionInfo, error) {
	var hashStr, roleStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT password_hash, role
		FROM local_users
		WHERE username = ?
	`, strings.TrimSpace(username)).Scan(&hashStr, &roleStr)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	role := Role(roleStr)
	if scope == "admin" && role != RoleAdmin {
		return nil, errors.New("admin role required")
	}

	tok := randomHex(32)
	csrf := randomHex(24)
	exp := time.Now().UTC().Add(12 * time.Hour)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO local_sessions (token, username, role, csrf_token, scope, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, tok, username, string(role), csrf, scope, exp.Format(time.RFC3339Nano), now)
	if err != nil {
		return nil, err
	}

	return &SessionInfo{
		Token:     tok,
		Username:  username,
		Role:      role,
		CSRFToken: csrf,
		ExpiresAt: exp,
	}, nil
}

func (s *Server) validateSession(ctx context.Context, r *http.Request, cookieName, scope string) (*SessionInfo, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return nil, errors.New("missing session cookie")
	}
	var sess SessionInfo
	var roleStr, expStr string
	err = s.db.QueryRowContext(ctx, `
		SELECT token, username, role, csrf_token, expires_at
		FROM local_sessions
		WHERE token = ? AND scope = ?
	`, c.Value, scope).Scan(&sess.Token, &sess.Username, &roleStr, &sess.CSRFToken, &expStr)
	if err != nil {
		return nil, errors.New("invalid session")
	}
	sess.Role = Role(roleStr)
	exp, err := time.Parse(time.RFC3339Nano, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		return nil, errors.New("session expired")
	}
	sess.ExpiresAt = exp
	return &sess, nil
}

func validateCSRFAndOrigin(r *http.Request, sess *SessionInfo, requireLoopbackHost bool) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return nil
	}
	if r.Host == "" {
		return errors.New("mutation requires Host header")
	}
	if requireLoopbackHost {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return errors.New("admin listener requires loopback Host")
		}
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return errors.New("mutation requires Origin header")
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid Origin header")
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return errors.New("cross-origin request blocked")
	}

	headerTok := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if headerTok == "" {
		headerTok = strings.TrimSpace(r.FormValue("csrf_token"))
	}
	if subtle.ConstantTimeCompare([]byte(headerTok), []byte(sess.CSRFToken)) != 1 {
		return errors.New("invalid CSRF token")
	}
	return nil
}

// AdminHandler serves the loopback-only administration interface (127.0.0.1:8081).
func (s *Server) AdminHandler() http.Handler {
	return s.adminHandler()
}

// FallbackHandler serves the authenticated LAN fallback interface (:8080).
// It deliberately has no node administration, enrollment, or configuration routes.
func (s *Server) FallbackHandler() http.Handler {
	return s.fallbackHandler()
}

func parseCredentials(r *http.Request) (string, string) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return strings.TrimSpace(body.Username), body.Password
	}
	_ = r.ParseForm()
	return strings.TrimSpace(r.FormValue("username")), r.FormValue("password")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

var readinessCacheSections = [...]string{"stock", "orders", "dies", "print_status", "company_format"}

// RevokeLocalAccess removes every local grant and all company data in one
// SQLite transaction. Job, command, event, and audit history are deliberately
// not touched; the administrator grant remains available for local diagnosis.
func (s *Server) RevokeLocalAccess(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	statements := []string{
		`DELETE FROM local_sessions`,
		`DELETE FROM local_users WHERE role <> 'admin' OR username <> 'admin'`,
	}
	if s.cacheStore != nil {
		statements = append(statements, `DELETE FROM cache_records`, `DELETE FROM cache_sections`)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) isRevoked() bool {
	if s.nodeStore == nil {
		return false
	}
	state, err := s.nodeStore.Load()
	return err == nil && state.Revoked
}

type readinessState struct {
	OK          bool                      `json:"ok"`
	State       string                    `json:"state"`
	Detail      string                    `json:"detail,omitempty"`
	Version     int                       `json:"version,omitempty"`
	RecordCount int                       `json:"recordCount,omitempty"`
	Sections    map[string]readinessState `json:"sections,omitempty"`
}

func (s *Server) identityReadiness() readinessState {
	if s.nodeStore == nil {
		return readinessState{State: "unavailable", Detail: "node store is not configured"}
	}
	state, err := s.nodeStore.Load()
	if err != nil {
		return readinessState{State: "unenrolled", Detail: err.Error()}
	}
	if state.Revoked {
		return readinessState{State: "node_revoked"}
	}
	origin, originErr := url.Parse(strings.TrimSpace(state.APIOrigin))
	validOrigin := originErr == nil && origin.Host != "" && (origin.Scheme == "http" || origin.Scheme == "https")
	valid := state.ProtocolVersion == 1 &&
		strings.TrimSpace(state.DeploymentID) != "" &&
		strings.TrimSpace(state.InstanceID) != "" &&
		strings.TrimSpace(state.InstallID) != "" &&
		validOrigin && state.Active.Version > 0 && strings.TrimSpace(state.Active.Secret) != ""
	if !valid {
		return readinessState{State: "invalid", Detail: "complete protocol v1 identity and active credential are required"}
	}
	return readinessState{OK: true, State: "ready", Version: state.Active.Version}
}

func (s *Server) databaseReadiness(ctx context.Context) readinessState {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO local_readiness_probe (id, updated_at) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at
	`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return readinessState{State: "unwritable", Detail: err.Error()}
	}
	return readinessState{OK: true, State: "writable"}
}

func (s *Server) configReadiness(ctx context.Context) readinessState {
	if s.jobStore == nil {
		return readinessState{State: "unavailable", Detail: "job store is not configured"}
	}
	version, raw, err := s.jobStore.LoadAppliedConfig(ctx)
	if err != nil {
		return readinessState{State: "unavailable", Detail: err.Error(), Version: version}
	}
	if version <= 0 {
		return readinessState{State: "not_applied", Version: version}
	}
	var config struct {
		ConfigVersion int               `json:"configVersion"`
		Printers      []json.RawMessage `json:"printers"`
		Profiles      []json.RawMessage `json:"profiles"`
		Templates     []json.RawMessage `json:"templates"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || config.ConfigVersion != version || config.Printers == nil || config.Profiles == nil || config.Templates == nil {
		return readinessState{State: "incomplete", Version: version}
	}
	return readinessState{OK: true, State: "applied", Version: version}
}

func (s *Server) cacheReadiness(ctx context.Context) readinessState {
	sections := make(map[string]readinessState, len(readinessCacheSections))
	complete := s.cacheStore != nil
	if !complete {
		for _, section := range readinessCacheSections {
			sections[section] = readinessState{State: "unavailable", Detail: "cache store is not configured"}
		}
		return readinessState{State: "incomplete", Sections: sections}
	}
	for _, section := range readinessCacheSections {
		meta, found, err := s.cacheStore.GetSectionMeta(ctx, section)
		switch {
		case err != nil:
			sections[section] = readinessState{State: "unavailable", Detail: err.Error()}
			complete = false
		case !found:
			sections[section] = readinessState{State: "missing"}
			complete = false
		case meta.Section != section || strings.TrimSpace(meta.SnapshotID) == "" ||
			strings.TrimSpace(meta.CompanyID) == "" || strings.TrimSpace(meta.GeneratedAt) == "" ||
			strings.TrimSpace(meta.ContentSHA256) == "" || meta.RecordCount < 0:
			sections[section] = readinessState{State: "incomplete"}
			complete = false
		default:
			sections[section] = readinessState{OK: true, State: "complete", RecordCount: meta.RecordCount}
		}
	}
	if !complete {
		return readinessState{State: "incomplete", Sections: sections}
	}
	return readinessState{OK: true, State: "complete", Sections: sections}
}

func (s *Server) serveReadiness(w http.ResponseWriter, r *http.Request, listener string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	identity := s.identityReadiness()
	database := s.databaseReadiness(r.Context())
	config := s.configReadiness(r.Context())
	companyCache := s.cacheReadiness(r.Context())
	ready := identity.OK && database.OK && config.OK && companyCache.OK
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ok": ready, "ready": ready, "listener": listener,
		"checks": map[string]readinessState{
			"identity": identity, "database": database, "appliedConfig": config, "cache": companyCache,
		},
	})
}

func cachePage(r *http.Request) (int, int, error) {
	limit, offset := 100, 0
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if limit > 500 {
			limit = 500
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

func (s *Server) cachedSectionPage(w http.ResponseWriter, r *http.Request, section string) {
	if _, ok := s.authorize(w, r, fallbackCookieName, "fallback", "", false); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit, offset, err := cachePage(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_pagination", "detail": err.Error()})
		return
	}
	if s.cacheStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"section": section, "cachedAsOf": "", "records": []any{}, "summaries": []any{},
			"banner": "No cached snapshot", "total": 0, "recordCount": 0,
			"limit": limit, "offset": offset, "nextOffset": nil,
		})
		return
	}
	records, meta, err := s.cacheStore.ListRecords(r.Context(), section, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cached_section_unavailable"})
		return
	}
	banner := "No cached snapshot"
	if meta.GeneratedAt != "" {
		banner = "Cached as of " + meta.GeneratedAt
	}
	var nextOffset *int
	if next := offset + len(records); len(records) > 0 && next < meta.RecordCount {
		nextOffset = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"section": section, "snapshotId": meta.SnapshotID, "cachedAsOf": meta.GeneratedAt,
		"banner": banner, "recordCount": meta.RecordCount, "records": publicCacheRecords(records),
		"summaries": redactJSON(meta.SummariesJSON), "total": meta.RecordCount,
		"limit": limit, "offset": offset, "nextOffset": nextOffset,
	})
}
