# AGENTS.md

Instructions for AI agents working on this codebase.

## Project Overview

TSC Spool is a Dockerized print spool for TSC thermal printers. It manages printers, generates TSPL2 labels, handles job queues, and provides a web UI for administration.

This branch also implements the outbound-only `cmd/orrn-edge` agent. Its runtime
packages are `internal/node`, `jobs`, `printer`, `worker`, `edgeclient`, `cache`,
`localui`, `install` and `service`; embedded local applications live in
`web/templates/edge` and `web/static/edge`.
The current Dockerfile/Compose and release workflow build Edge. `cmd/spool`
remains the legacy tunnel runtime and must not be silently substituted for Edge.
Read README's ORRN Edge section and the verified deployment skill at
`../../.claude/skills/orrn-spool-edge/SKILL.md` when working in this workspace.
Native Edge artifacts must use `CGO_ENABLED=0`, explicit release versions and
the pure-Go SQLite driver. Never publish a non-main package archive as an
executable or patch node secrets into immutable release binaries.

## Reformed spool safety and evidence

- B is operations-dense, A is polished alternate, and C is compact alternate. Keep all three as versioned label documents; compiled acceptance must verify their TSPL geometry, symbology and copy counts.
- Physical ruler measurements, QR/Code 128 scans and physical copy counts are separate observed-evidence gates. Never mark them passed from renderer tests, `sent` status, container health or cross-compilation.
- Discovery is read-only evidence. Desired media/post-print settings change only through authorized preferences; discovered optional hardware never activates itself.
- DHCP recovery uses OS neighbor state only, requires matching verified MAC plus genuine status and a supported model, and retransmits only after a zero-byte retryable outcome. Never redirect `outcome_unknown` jobs.
- Device actions are limited to audited calibrate, cutter, RTC and bounded font download after feature validation. Do not add `AUTO.BAS`, reset, firmware update, arbitrary file execution or response suppression.
- Keep the legacy Fourcubes spool and live deployment unchanged until native Windows SCM, Linux systemd, Docker rollback and ARM64 execution evidence is separately approved.

## Technology Stack

- **Language:** Go 1.22
- **Web Framework:** Gin
- **Database:** SQLite via `modernc.org/sqlite` (pure Go), age-encrypted archives
- **Web UI:** HTMX + Tailwind CSS + Alpine.js
- **AI:** Google Gemini 3 Flash

## Build & Run

```bash
# Install dependencies
go mod tidy

# Run locally
go run ./cmd/spool

# Build binary
go build -o spool ./cmd/spool

# Build for production (pure-Go SQLite, with optimizations)
CGO_ENABLED=0 go build -ldflags="-w -s" -o spool ./cmd/spool

# Run with Docker
docker-compose up -d --build
```

## Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests for a specific package
go test ./internal/core/...

# Verbose output
go test -v ./...
```

## Code Style

- Follow Go standard formatting: `go fmt ./...`
- Use `golangci-lint` for linting: `golangci-lint run`
- No commented-out code in production files
- Use meaningful variable names, avoid abbreviations except common ones (ctx, db, id)

## Project Structure

```
cmd/spool/          - Application entrypoint
internal/
  api/handlers/     - HTTP handlers organized by domain
  api/middleware/   - HTTP middleware (auth, logging)
  core/             - Business logic (printer manager, queue, TSPL2 generator)
  db/               - Database layer (models, operations, migrations)
  ai/               - Gemini AI client
  archive/          - Job archival with age encryption
  webhook/          - Webhook notification sender
  utils/            - Utility functions (encryption)
  config/           - Configuration loading
web/
  templates/        - HTML templates (Go html/template)
  static/           - Static assets (CSS, JS)
```

## Key Patterns

### Database Operations
Use the repository pattern in `internal/db/operations.go`:

```go
printer, err := db.Printers.GetByID(ctx, id)
err := db.Jobs.Create(ctx, &db.PrintJob{...})
```

### HTTP Handlers
Handlers are organized by domain and return JSON:

```go
func (h *PrinterHandler) CreatePrinter(c *gin.Context) {
    var req CreatePrinterRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    // ...
    c.JSON(201, response)
}
```

### Authentication
Protected routes use `RequireAuth()` middleware:

```go
protected := api.Group("")
protected.Use(authMiddleware.RequireAuth())
```

### TSPL2 Generation
Generate labels from schema:

```go
generator := &core.TSPL2Generator{}
tspl, err := generator.Generate(schema, variables)
```

### Legacy queue and printer safety

- `cmd/spool` remains the signed inbound/tunnel runtime. It does not enroll through Edge V1.
- Claim jobs with a status-changing SQL compare-and-swap; duplicate channel delivery must not cause another device write.
- Startup marks interrupted `processing` jobs failed with `process_interrupted_outcome_unknown`; never blindly requeue them.
- Retry only failures known to precede device bytes. Partial writes return `ErrPrintOutcomeUnknown` and require explicit operator retry.
- Serialize status and payload I/O per physical printer. TA220 one-byte and documented four-byte replies are both valid.
- `PrinterManager.Print` owns the printer total counter; the queue owns only the daily counter.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| SPOOL_PORT | Server port | 8080 |
| SPOOL_DB_PATH | Database file path | ./data/spool.db |
| SPOOL_ARCHIVE_PATH | Archive directory | ./data/archives |
| SPOOL_LOG_LEVEL | Log level | info |
| SPOOL_CONFIG | Config file path | config.yaml |

## Database Migrations

Migrations are in `internal/db/migrations/` and run automatically on startup.

To add a migration:
1. Create `internal/db/migrations/NNN_description.sql`
2. Add migration code to `internal/db/db.go` if needed

## Web UI Development

Templates use Go `html/template` with HTMX for interactivity:

- Base layout: `web/templates/layout/base.html`
- Pages: `web/templates/pages/`
- Modals: `web/templates/modals/`
- Partials: `web/templates/partials/`

All files are parsed into ONE template set (`LoadHTMLGlob`), so every
`{{ define "x" }}` name must be unique across files. A define that reuses a name
(the old `content` and `modal-content`) silently overrides the other file.

- A page `pages/foo.html` defines `foo-content` (its body) and `foo`
  (`layout-head` + `foo-content` + `layout-foot`). The handler calls
  `c.HTML(code, "foo", data)`.
- Fragments HTMX refreshes (`printers-grid`, `queue-sections`, `history-table`,
  `audit-table`, `printer-card`) are their own defines in the same file.
- Modals define a unique name (`printer-form`, `job-detail`, ...).
- Page data embeds `handlers.PageBase` (Title, Active, Authenticated, Toasts)
  because the layout and sidebar read those fields.
- `cmd/spool/templates_test.go` fails on duplicate defines, unresolved
  `{{ template }}` references, a `c.HTML` name with no template, and renders
  every page with sample data. Add a render case when you add a handler.
- `handlers.TemplateFuncs()` `json` returns `template.JS`, so
  `{{ .X | json }}` yields an object inside scripts and Alpine attributes.
- HTMX forms and buttons talk to the JSON API: `<body hx-ext="json-enc">`
  sends JSON, and `HTMXFragments` (middleware on `/api`) swaps the JSON reply
  of a successful HTMX call for the HTML of the element in `HX-Target`.

HTMX attributes for real-time updates:
```html
<div hx-get="/api/endpoint" hx-trigger="every 30s">
```

## API Authentication

JWT-based authentication with password-only login:

1. First-time setup: `POST /api/auth/setup` with password
2. Login: `POST /api/auth/login` returns JWT in HttpOnly cookie
3. Protected routes require valid JWT

## Webhook Events

Supported events:
- `job_started`
- `job_completed`
- `job_failed`
- `printer_status_changed`
- `queue_status`

Webhooks are signed with HMAC-SHA256 when a secret is configured.

## Common Tasks

### Add a new API endpoint
1. Create handler in `internal/api/handlers/<domain>.go`
2. Register route in `RegisterXRoutes()` function
3. Add to `setupRoutes()` in `cmd/spool/main.go`

### Add a new TSPL2 element type
1. Add element struct in `internal/core/tspl2_generator.go`
2. Add to `LabelElement` struct
3. Implement generation in `Generate()` switch statement
4. Add validation in `validateElement()`

### Add a new database table
1. Create migration in `internal/db/migrations/`
2. Add model in `internal/db/models.go`
3. Add operations in `internal/db/operations.go`
4. Add queries in `internal/db/queries.go`

## Troubleshooting

### Database locked
SQLite can lock under heavy write load. Consider:
- Increasing busy timeout
- Using WAL mode
- Reducing concurrent writes

### Printer not responding
1. Check network connectivity: `ping <printer_ip>`
2. Check port 9100 is open: `nc -zv <printer_ip> 9100`
3. Check printer status via `<ESC>!?` command

### Build errors with CGO
SQLite requires CGO. Ensure:
- GCC installed: `apt-get install build-essential`
- Set `CGO_ENABLED=1`
- On macOS: Xcode command line tools installed