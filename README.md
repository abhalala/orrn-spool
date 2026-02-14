# orrn-spool

A Dockerized print spool for TSC thermal printers with TSPL2 label generation, AI-powered label design, and comprehensive job queue management.

## Features

- **Printer Management** - TCP/IP connectivity on port 9100, real-time status monitoring, health checks
- **Dynamic TSPL2 Generation** - JSON schema to TSPL2 conversion with variable substitution
- **AI-Powered Label Design** - Gemini 2.0 Flash integration for natural language label creation
- **Job Queue** - Priority-based processing with automatic retry and configurable workers
- **Real-time Monitoring** - Printer status polling, job progress tracking, print counters
- **Webhook Notifications** - Event-driven notifications for job completion, failures, and status changes
- **Encrypted Job Archival** - Secure long-term storage with passphrase protection
- **Web UI** - HTMX-based responsive interface for complete management

# AI Agent and Inference used:
KiloCode w/ GLM 5
```
Context
65,375 tokens
32% used
$0.61 spent
```

## Quick Start

### Prerequisites

- Docker and Docker Compose
- TSC thermal printer with network connectivity
- (Optional) Google Gemini API key for AI label design

### Run with Docker Compose

```bash
# Clone the repository
git clone https://github.com/orrn/spool.git
cd spool

# Copy environment file
cp .env.example .env

# Start the service
docker-compose up -d
```

Access the web UI at `http://localhost:8080`

### First-time Setup

1. **Set Password** - On first access, you'll be prompted to create an admin password
2. **Add a Printer** - Navigate to Printers → Add Printer
3. **Create a Template** - Navigate to Templates → New Template
4. **Print a Test Job** - Use the test print feature to verify connectivity

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SPOOL_PORT` | `8080` | Server port |
| `SPOOL_DB_PATH` | `./data/spool.db` | SQLite database path |
| `SPOOL_ARCHIVE_PATH` | `./data/archives` | Archive storage directory |
| `SPOOL_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `TZ` | `UTC` | Timezone |

### config.yaml Reference

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s

database:
  path: ./data/spool.db
  archive_path: ./data/archives
  archive_days: 30

printers:
  health_check_interval: 30s
  connection_timeout: 10s
  status_poll_interval: 5s

queue:
  max_retries: 3
  retry_delay: 10s
  worker_count: 2

logging:
  level: info
  format: json
```

### Database Paths

The application uses SQLite for data storage:

- **Main Database**: `./data/spool.db` - Printers, templates, jobs, webhooks, settings
- **Archives**: `./data/archives/` - Encrypted archive files for old jobs

## API Reference

### Authentication

All API endpoints (except `/health` and `/api/auth/*`) require JWT authentication.

```bash
# Login
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "your-password"}'

# Response includes JWT token
{"token": "eyJhbGciOiJIUzI1NiIs..."}
```

Include the token in subsequent requests:

```bash
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/printers
```

### Printers API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/printers` | List all printers |
| `POST` | `/api/printers` | Create a new printer |
| `GET` | `/api/printers/:id` | Get printer details |
| `PUT` | `/api/printers/:id` | Update printer |
| `DELETE` | `/api/printers/:id` | Delete printer |
| `GET` | `/api/printers/:id/status` | Get real-time status |
| `POST` | `/api/printers/:id/test` | Send test print |
| `POST` | `/api/printers/:id/pause` | Pause printer |
| `POST` | `/api/printers/:id/resume` | Resume printer |
| `GET` | `/api/printers/:id/counters` | Get print counters |

### Jobs API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/jobs` | List jobs (with filters) |
| `POST` | `/api/jobs` | Create a print job |
| `GET` | `/api/jobs/queue` | Get queue statistics |
| `GET` | `/api/jobs/stats` | Get job statistics |
| `GET` | `/api/jobs/:id` | Get job details |
| `DELETE` | `/api/jobs/:id` | Delete job |
| `POST` | `/api/jobs/:id/cancel` | Cancel job |
| `POST` | `/api/jobs/:id/retry` | Retry failed job |
| `POST` | `/api/jobs/:id/reprint` | Reprint job |
| `POST` | `/api/jobs/:id/pause` | Pause job |
| `POST` | `/api/jobs/:id/resume` | Resume job |

### Templates API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/templates` | List all templates |
| `POST` | `/api/templates` | Create template |
| `GET` | `/api/templates/:id` | Get template details |
| `PUT` | `/api/templates/:id` | Update template |
| `DELETE` | `/api/templates/:id` | Delete template |
| `POST` | `/api/templates/:id/preview` | Preview TSPL output |
| `POST` | `/api/templates/:id/validate` | Validate schema |
| `POST` | `/api/templates/:id/print` | Quick print with template |

### Webhooks API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/webhooks` | List webhooks |
| `POST` | `/api/webhooks` | Create webhook |
| `GET` | `/api/webhooks/:id` | Get webhook |
| `PUT` | `/api/webhooks/:id` | Update webhook |
| `DELETE` | `/api/webhooks/:id` | Delete webhook |
| `POST` | `/api/webhooks/:id/test` | Test webhook |

**Supported Events:**
- `job_started` - Job began processing
- `job_completed` - Job finished successfully
- `job_failed` - Job failed with error
- `printer_status_changed` - Printer status updated
- `queue_status` - Queue state changed

### AI API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/ai/generate` | Generate label schema from description |
| `GET` | `/api/ai/test` | Test AI connection |
| `GET` | `/api/ai/config` | Get AI configuration status |
| `POST` | `/api/ai/api-key` | Set Gemini API key |
| `DELETE` | `/api/ai/api-key` | Delete API key |

### Archives API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/archives` | List archive files |
| `GET` | `/api/archives/stats` | Archive statistics |
| `GET` | `/api/archives/:filename` | Get archive info |
| `GET` | `/api/archives/:filename/download` | Download decrypted archive |
| `DELETE` | `/api/archives/:filename` | Delete archive |
| `POST` | `/api/archives/run` | Trigger manual archival |
| `POST` | `/api/archives/restore` | Restore job from archive |

### Legacy API

For backward compatibility:

```
GET /print/:layout/:uid
```

Automatically selects an available printer and prints using the named template.

## Usage Examples

### Submit a Print Job

```bash
curl -X POST http://localhost:8080/api/jobs \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "printer_id": 1,
    "template_id": 1,
    "variables": {
      "product_name": "Widget Pro",
      "barcode": "123456789012",
      "price": "$29.99"
    },
    "copies": 2,
    "priority": 1
  }'
```

### Create a Template

```bash
curl -X POST http://localhost:8080/api/templates \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "product_label",
    "description": "Standard product label with barcode",
    "schema": {
      "width_mm": 100,
      "height_mm": 50,
      "gap_mm": 2,
      "dpi": 203,
      "elements": [
        {"type": "text", "x": 10, "y": 5, "font": "3", "x_scale": 2, "y_scale": 2, "content": "{{product_name}}"},
        {"type": "barcode", "x": 10, "y": 25, "symbology": "128", "height": 60, "content": "{{barcode}}"},
        {"type": "text", "x": 10, "y": 90, "font": "2", "content": "{{price}}"}
      ],
      "variables": {
        "product_name": {"type": "string", "required": true},
        "barcode": {"type": "string", "required": true},
        "price": {"type": "string", "required": false, "default": ""}
      }
    }
  }'
```

### Configure a Webhook

```bash
curl -X POST http://localhost:8080/api/webhooks \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Job Notifications",
    "url": "https://your-server.com/webhook",
    "secret": "your-webhook-secret",
    "events": ["job_completed", "job_failed"]
  }'
```

Webhook payloads include HMAC-SHA256 signature in `X-Webhook-Signature` header.

### Use AI Label Designer

```bash
# Set API key first
curl -X POST http://localhost:8080/api/ai/api-key \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"api_key": "your-gemini-api-key"}'

# Generate a label
curl -X POST http://localhost:8080/api/ai/generate \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Create a shipping label with recipient address, barcode, and QR code for tracking",
    "width_mm": 100,
    "height_mm": 75,
    "dpi": 203
  }'
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Web UI (HTMX)                           │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                       API Server (Gin)                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │  Printers   │  │    Jobs     │  │  Templates  │              │
│  │  Handlers   │  │  Handlers   │  │  Handlers   │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │  Webhooks   │  │     AI      │  │  Archives   │              │
│  │  Handlers   │  │  Handlers   │  │  Handlers   │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
└─────────────────────────────────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        ▼                       ▼                       ▼
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│ Print Queue   │     │    Printer    │     │   Webhook     │
│   Manager     │     │   Manager     │     │    Sender     │
│  (Workers)    │     │  (Monitor)    │     │  (Workers)    │
└───────────────┘     └───────────────┘     └───────────────┘
        │                       │
        ▼                       ▼
┌───────────────┐     ┌───────────────┐
│    TSPL2      │     │   TSC TCP     │
│  Generator    │     │   Port 9100   │
└───────────────┘     └───────────────┘
        │                       │
        └───────────┬───────────┘
                    ▼
          ┌───────────────┐
          │   TSC Printer │
          │   (Network)   │
          └───────────────┘
```

### Components

| Component | Description |
|-----------|-------------|
| **API Server** | Gin-based HTTP server with JWT authentication |
| **Print Queue** | Priority-based job queue with configurable workers |
| **Printer Manager** | TCP connections, health checks, status polling |
| **TSPL2 Generator** | JSON schema to TSPL2 command conversion |
| **Webhook Sender** | Async event delivery with retry logic |
| **AI Client** | Gemini API integration for label design |
| **Archiver** | Encrypted job archival with passphrase protection |

### Database Schema

```
┌──────────────┐     ┌──────────────┐     ┌───────────────┐
│   printers   │     │  print_jobs  │     │label_templates│
├──────────────┤     ├──────────────┤     ├───────────────┤
│ id           │◄────│ printer_id   │     │ id            │
│ name         │     │ template_id  │────►│ name          │
│ ip_address   │     │ variables    │     │ schema_json   │
│ port         │     │ tspl_content │     │ width_mm      │
│ dpi          │     │ status       │     │ height_mm     │
│ label_width  │     │ priority     │     └───────────────┘
│ label_height │     │ retry_count  │
│ gap_mm       │     │ error_message│     ┌──────────────┐
│ status       │     │ copies       │     │   webhooks   │
│ total_prints │     │ submitted_by │     ├──────────────┤
└──────────────┘     └──────────────┘     │ id           │
                     ┌──────────────┐     │ name         │
                     │print_counters│     │ url          │
                     ├──────────────┤     │ events_json  │
                     │ printer_id   │     │ enabled      │
                     │ date         │     └──────────────┘
                     │ count        │
                     └──────────────┘
```

## Development

### Build from Source

```bash
# Clone
git clone https://github.com/orrn/spool.git
cd spool

# Install dependencies
go mod download

# Build
go build -o spool ./cmd/spool

# Run
./spool
```

### Project Structure

```
spool/
├── cmd/spool/main.go          # Application entry point
├── config.yaml                # Configuration file
├── docker-compose.yml         # Docker Compose setup
├── Dockerfile                 # Container build
├── go.mod                     # Go module definition
├── internal/
│   ├── api/
│   │   ├── handlers/          # HTTP handlers
│   │   │   ├── printers.go
│   │   │   ├── jobs.go
│   │   │   ├── templates.go
│   │   │   ├── webhooks.go
│   │   │   ├── ai.go
│   │   │   ├── archive.go
│   │   │   ├── settings.go
│   │   │   └── webui.go
│   │   └── middleware/        # Auth middleware
│   ├── ai/                    # Gemini AI client
│   ├── archive/               # Job archival
│   ├── config/                # Configuration loading
│   ├── core/                  # Core business logic
│   │   ├── queue.go           # Job queue
│   │   ├── printer_manager.go # Printer management
│   │   ├── tspl2_generator.go # TSPL2 generation
│   │   ├── job_manager.go     # Job management
│   │   └── types.go           # Type definitions
│   ├── db/                    # Database layer
│   │   ├── db.go              # Connection setup
│   │   ├── models.go          # Data models
│   │   ├── operations.go      # CRUD operations
│   │   ├── queries.go         # SQL queries
│   │   └── migrations/        # Schema migrations
│   ├── utils/                 # Utilities (encryption)
│   └── webhook/               # Webhook sender
├── web/
│   ├── static/                # CSS, JS assets
│   └── templates/             # HTML templates
│       ├── auth/
│       ├── layout/
│       ├── pages/
│       ├── partials/
│       └── modals/
└── tspl2_docs.md              # TSPL2 command reference
```

### Running Tests

```bash
go test ./... -v
```

### Adding New Features

1. **New API Endpoint**: Add handler in `internal/api/handlers/`, register in `cmd/spool/main.go`
2. **New Database Table**: Create migration in `internal/db/migrations/`, add model in `internal/db/models.go`
3. **New TSPL2 Element**: Extend `internal/core/tspl2_generator.go` with element type

## Troubleshooting

### Common Issues

| Issue | Solution |
|-------|----------|
| Printer shows offline | Verify network connectivity, check IP/port settings |
| Job stuck in pending | Check queue workers, verify printer is not paused |
| Template validation fails | Ensure all required variables have values |
| Webhook not received | Check URL accessibility, verify secret/signature |
| AI generation fails | Verify Gemini API key is valid and has quota |

### Debug Mode

Enable debug logging:

```bash
# Environment variable
SPOOL_LOG_LEVEL=debug

# Or in config.yaml
logging:
  level: debug
```

### Log Locations

- **Container**: `docker logs tsc-spool`
- **File**: JSON logs written to stdout (captured by Docker)

### Health Check

```bash
curl http://localhost:8080/health
# Response: {"status": "healthy", "version": "1.0.0"}
```

## TSPL2 Element Types

| Type | Description | Required Fields |
|------|-------------|-----------------|
| `text` | Text string | `x`, `y`, `content` |
| `barcode` | 1D barcode | `x`, `y`, `content`, `symbology` |
| `qrcode` | QR code | `x`, `y`, `content` |
| `pdf417` | PDF417 barcode | `x`, `y`, `content` |
| `datamatrix` | DataMatrix code | `x`, `y`, `content` |
| `box` | Rectangle | `x`, `y`, `x_end`, `y_end` |
| `line` | Line | `x1`, `y1`, `x2`, `y2` |
| `circle` | Circle | `x`, `y`, `radius` |
| `ellipse` | Ellipse | `x`, `y`, `x_radius`, `y_radius` |
| `block` | Text block | `x`, `y`, `width`, `height`, `content` |
| `image` | BMP image | `x`, `y`, `image_path` |

## License

MIT License

Copyright (c) 2026 ORRN.APP

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
