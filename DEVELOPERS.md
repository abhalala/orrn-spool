# DEVELOPERS.md

Development guide for contributing to TSC Spool.

## Prerequisites

### macOS

```bash
# Install Go 1.22+
brew install go

# Install SQLite (required for CGO)
brew install sqlite

# Install age for encryption (optional, for archive testing)
brew install age

# Install golangci-lint for linting
brew install golangci-lint
```

### Linux (Ubuntu/Debian)

```bash
# Install Go 1.22+
sudo apt update
sudo apt install -y golang-go

# Or use Snap for latest version
sudo snap install go --classic

# Install build tools (required for CGO/SQLite)
sudo apt install -y build-essential

# Install age for encryption
sudo apt install -y age

# Install golangci-lint
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

### Linux (Fedora/RHEL)

```bash
# Install Go
sudo dnf install -y golang

# Install build tools
sudo dnf install -y gcc make

# Install age
sudo dnf install -y age
```

### Windows

```powershell
# Using Chocolatey
choco install golang
choco install mingw  # For CGO/SQLite

# Using Scoop
scoop install go
scoop install gcc
```

## Development Setup

### 1. Clone and Initialize

```bash
# Clone the repository
git clone https://github.com/your-org/orrn-spool.git
cd orrn-spool

# Download dependencies
go mod download

# Verify setup
go version
# Should output: go version go1.22.x ...
```

### 2. Environment Configuration

```bash
# Copy example environment file
cp .env.example .env

# Edit as needed
vim .env
```

### 3. IDE Setup

#### VS Code

Recommended extensions:
- `gopls` (Go language server)
- `Go` (official Go extension)
- `SQLite Viewer`

Settings (`.vscode/settings.json`):
```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "workspace",
  "[go]": {
    "editor.formatOnSave": true,
    "editor.codeActionsOnSave": {
      "source.organizeImports": true
    }
  }
}
```

#### GoLand / IntelliJ

1. Open project directory
2. Enable Go module support
3. Set Go SDK to Go 1.22+
4. Enable golangci-lint integration

### 4. Run Development Server

```bash
# Run with hot reload (install air first: go install github.com/cosmtrek/air@latest)
air

# Or run directly
go run ./cmd/spool

# Build and run
go build -o spool ./cmd/spool
./spool
```

## Project Structure

```
orrn-spool/
├── cmd/
│   └── spool/
│       └── main.go              # Application entry point
├── internal/
│   ├── api/
│   │   ├── handlers/            # HTTP request handlers
│   │   │   ├── ai.go            # AI label generation endpoints
│   │   │   ├── archive.go       # Archive management endpoints
│   │   │   ├── jobs.go          # Job CRUD and queue operations
│   │   │   ├── printers.go      # Printer management endpoints
│   │   │   ├── settings.go      # Settings endpoints
│   │   │   ├── templates.go     # Template CRUD endpoints
│   │   │   ├── webhooks.go      # Webhook management endpoints
│   │   │   └── webui.go         # Web UI page handlers
│   │   └── middleware/
│   │       └── auth.go          # JWT authentication middleware
│   ├── ai/
│   │   └── gemini_client.go     # Gemini 3 Flash API client
│   ├── archive/
│   │   └── archiver.go          # Job archival with age encryption
│   ├── config/
│   │   └── config.go            # Configuration loading and validation
│   ├── core/
│   │   ├── job_manager.go       # Job lifecycle management
│   │   ├── printer_manager.go   # TCP printer communication
│   │   ├── queue.go             # Job queue with worker pool
│   │   ├── tspl2_generator.go   # TSPL2 label generation
│   │   └── types.go             # Shared types and interfaces
│   ├── db/
│   │   ├── db.go                # Database connection and migrations
│   │   ├── migrations/          # SQL migration files
│   │   ├── models.go            # Data model structs
│   │   ├── operations.go        # Repository pattern operations
│   │   └── queries.go           # SQL query constants
│   ├── utils/
│   │   └── encryption.go        # AES-256-GCM encryption utilities
│   └── webhook/
│       └── sender.go            # Webhook notification sender
├── web/
│   ├── static/
│   │   └── css/custom.css       # Custom CSS styles
│   └── templates/
│       ├── auth/                # Authentication templates
│       ├── layout/              # Base layout and sidebar
│       ├── modals/              # Modal dialogs
│       ├── pages/               # Main page templates
│       └── partials/            # Reusable template components
├── config.yaml                  # Default configuration
├── docker-compose.yml           # Docker Compose configuration
├── Dockerfile                   # Multi-stage Docker build
├── go.mod                       # Go module definition
├── go.sum                       # Go module checksums
└── README.md                    # Project documentation
```

## Development Workflow

### 1. Create a Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Changes

Follow the coding standards:
- Use `gofmt` for formatting
- Run `golangci-lint run` before committing
- Write tests for new functionality
- Update documentation as needed

### 3. Run Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests for a specific package
go test -v ./internal/core/...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### 4. Lint Your Code

```bash
# Run linter
golangci-lint run

# Run with auto-fix where possible
golangci-lint run --fix
```

### 5. Commit Changes

```bash
# Stage changes
git add .

# Commit with descriptive message
git commit -m "feat: add new feature description"

# Push to remote
git push origin feature/your-feature-name
```

## Adding New Features

### Adding a New API Endpoint

1. **Define the handler** in `internal/api/handlers/`:

```go
// internal/api/handlers/example.go
package handlers

import "github.com/gin-gonic/gin"

type ExampleHandler struct {
    db *sql.DB
}

func NewExampleHandler(db *sql.DB) *ExampleHandler {
    return &ExampleHandler{db: db}
}

func (h *ExampleHandler) GetExample(c *gin.Context) {
    c.JSON(200, gin.H{"message": "example"})
}

func RegisterExampleRoutes(r *gin.RouterGroup, h *ExampleHandler) {
    examples := r.Group("/examples")
    {
        examples.GET("", h.GetExample)
        examples.POST("", h.CreateExample)
        examples.GET("/:id", h.GetExampleByID)
    }
}
```

2. **Register in main.go**:

```go
// In setupRoutes() function
exampleHandler := handlers.NewExampleHandler(deps.DB)
handlers.RegisterExampleRoutes(protected, exampleHandler)
```

### Adding a New Database Table

1. **Create migration** in `internal/db/migrations/`:

```sql
-- internal/db/migrations/002_add_examples.sql
CREATE TABLE examples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_examples_name ON examples(name);
```

2. **Add model** in `internal/db/models.go`:

```go
type Example struct {
    ID        int64     `json:"id"`
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"created_at"`
}
```

3. **Add queries** in `internal/db/queries.go`:

```go
const (
    InsertExample = `INSERT INTO examples (name) VALUES (?)`
    GetExampleByID = `SELECT id, name, created_at FROM examples WHERE id = ?`
    ListExamples = `SELECT id, name, created_at FROM examples ORDER BY created_at DESC`
)
```

4. **Add operations** in `internal/db/operations.go`:

```go
type ExampleOperations struct{}

var Examples = &ExampleOperations{}

func (o *ExampleOperations) Create(ctx context.Context, e *Example) error {
    result, err := db.ExecContext(ctx, InsertExample, e.Name)
    if err != nil {
        return err
    }
    e.ID, _ = result.LastInsertId()
    return nil
}

func (o *ExampleOperations) GetByID(ctx context.Context, id int64) (*Example, error) {
    e := &Example{}
    err := db.QueryRowContext(ctx, GetExampleByID, id).Scan(&e.ID, &e.Name, &e.CreatedAt)
    if err != nil {
        return nil, err
    }
    return e, nil
}
```

### Adding a New TSPL2 Element Type

1. **Add element struct** in `internal/core/tspl2_generator.go`:

```go
type NewElement struct {
    Type       string `json:"type"`
    X, Y       int    `json:"x,y"`
    // Add element-specific fields
}
```

2. **Add to LabelElement union**:

```go
type LabelElement struct {
    Type string `json:"type"`
    // ... existing fields
    NewElement *NewElement `json:"new_element,omitempty"`
}
```

3. **Implement generation** in `Generate()`:

```go
switch elem.Type {
case "new_element":
    // Generate TSPL2 command
    lines = append(lines, fmt.Sprintf("NEW_COMMAND %d,%d,...", elem.X, elem.Y))
}
```

4. **Add validation** in `validateElement()`:

```go
case "new_element":
    if elem.X < 0 || elem.Y < 0 {
        errors = append(errors, "new_element: x and y must be non-negative")
    }
```

### Adding a New Web UI Page

1. **Create template** in `web/templates/pages/`:

```html
<!-- web/templates/pages/example.html -->
{{ define "content" }}
<div class="space-y-6">
    <h1 class="text-2xl font-bold">Example Page</h1>
    <!-- Page content -->
</div>
{{ end }}
```

2. **Add handler** in `internal/api/handlers/webui.go`:

```go
func (h *WebUIHandler) ExamplePage(c *gin.Context) {
    // Fetch data
    data := map[string]interface{}{
        "Title": "Example",
    }
    
    // Render template
    c.HTML(http.StatusOK, "example.html", data)
}
```

3. **Register route** in `setupRoutes()`:

```go
router.GET("/example", deps.Auth.OptionalAuth(), webUIHandler.ExamplePage)
```

4. **Add to sidebar** in `web/templates/layout/sidebar.html`:

```html
<a href="/example" class="flex items-center px-4 py-2 hover:bg-gray-700">
    <span>Example</span>
</a>
```

## Testing

### Unit Tests

```go
// internal/core/tspl2_generator_test.go
package core

import "testing"

func TestGenerateText(t *testing.T) {
    generator := &TSPL2Generator{}
    schema := &LabelSchema{
        WidthMM:  100,
        HeightMM: 50,
        Elements: []LabelElement{
            {Type: "text", X: 10, Y: 10, Content: "Hello", Font: "3"},
        },
    }
    
    tspl, err := generator.Generate(schema, nil)
    if err != nil {
        t.Fatalf("Generate failed: %v", err)
    }
    
    if !strings.Contains(tspl, "TEXT 10,10") {
        t.Error("Expected TEXT command in output")
    }
}
```

### Integration Tests

```go
// internal/api/handlers/printers_test.go
package handlers

import (
    "net/http"
    "net/http/httptest"
    "testing"
    
    "github.com/gin-gonic/gin"
)

func TestListPrinters(t *testing.T) {
    // Setup
    router := gin.New()
    handler := &PrinterHandler{db: testDB}
    router.GET("/printers", handler.ListPrinters)
    
    // Request
    req, _ := http.NewRequest("GET", "/printers", nil)
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)
    
    // Assert
    if w.Code != http.StatusOK {
        t.Errorf("Expected 200, got %d", w.Code)
    }
}
```

### Running Tests with Docker

```bash
# Build test image
docker build -t spool-test -f Dockerfile.test .

# Run tests
docker run --rm spool-test go test ./...
```

## Debugging

### Enable Debug Logging

```bash
# Set log level to debug
export SPOOL_LOG_LEVEL=debug
go run ./cmd/spool
```

### Database Inspection

```bash
# Connect to SQLite database
sqlite3 data/spool.db

# Useful queries
.tables
.schema print_jobs
SELECT * FROM print_jobs ORDER BY created_at DESC LIMIT 10;
```

### Network Debugging

```bash
# Test printer connection
nc -zv 192.168.1.100 9100

# Send raw TSPL2 to printer
echo -e "SIZE 4,2\nGAP 0.12,0\nCLS\nTEXT 50,50,\"3\",0,1,1,\"Test\"\nPRINT 1" | nc 192.168.1.100 9100
```

## Release Process

### 1. Version Bump

Update version in code and documentation.

### 2. Build Release Binary

```bash
# Build for multiple platforms
GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="-w -s" -o dist/spool-linux-amd64 ./cmd/spool
GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -ldflags="-w -s" -o dist/spool-darwin-amd64 ./cmd/spool
```

### 3. Build Docker Image

```bash
docker build -t orrn/spool:latest .
docker tag orrn/spool:latest orrn/spool:v1.0.0
docker push orrn/spool:latest
docker push orrn/spool:v1.0.0
```

## Common Issues

### CGO Errors

If you see CGO-related errors:

```bash
# macOS: Install Xcode command line tools
xcode-select --install

# Linux: Install build-essential
sudo apt install build-essential

# Ensure CGO is enabled
export CGO_ENABLED=1
```

### SQLite Driver Issues

```bash
# Reinstall SQLite driver
go clean -cache
go get github.com/mattn/go-sqlite3
go mod tidy
```

### Hot Reload Not Working

```bash
# Install air
go install github.com/cosmtrek/air@latest

# Create .air.toml config
air init
```

## Useful Commands

```bash
# Format all Go files
go fmt ./...

# Check for potential issues
go vet ./...

# Update dependencies
go get -u ./...
go mod tidy

# Generate Go documentation
godoc -http=:6060

# Run with pprof profiling
go run ./cmd/spool -cpuprofile=cpu.prof
go tool pprof cpu.prof
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests and linting
5. Submit a pull request

Please ensure:
- Code passes all tests
- Code is properly formatted
- Documentation is updated
- Commit messages are descriptive
