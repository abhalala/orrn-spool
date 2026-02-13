# AGENTS.md

Instructions for AI agents working on this codebase.

## Project Overview

TSC Spool is a Dockerized print spool for TSC thermal printers. It manages printers, generates TSPL2 labels, handles job queues, and provides a web UI for administration.

## Technology Stack

- **Language:** Go 1.22
- **Web Framework:** Gin
- **Database:** SQLite (main), SQLite + age encryption (archives)
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

# Build for production (with optimizations)
CGO_ENABLED=1 go build -ldflags="-w -s" -o spool ./cmd/spool

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