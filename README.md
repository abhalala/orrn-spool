# orrn-spool

A Dockerized print spool for TSC thermal printers with TSPL2 label generation, AI-powered label design, and comprehensive job queue management.

## ORRN Edge V1

This branch also builds `cmd/orrn-edge`, the outbound-only site agent. It is a
different runtime from `cmd/spool`: do not use a legacy tunnel token as its
enrollment token or assume the existing Agni pilot has migrated to it.

### Build and verify

```bash
CGO_ENABLED=0 go build -o /tmp/orrn-edge ./cmd/orrn-edge
/tmp/orrn-edge version
go test ./...
go vet ./...
go test ./cmd/orrn-edge -run TestLocalEdgeEndToEnd -v
bash scripts/release_test.sh
```

The acceptance test builds and starts the real agent against a signed fake
control plane and fake TCP TA220. It proves enrollment, initial config and
snapshots, B/A/C print payloads and copy counts, event delivery, restart
without duplicate transmission, cached reads, authorized offline reprint, and
revocation with diagnostic history retained. `TestPhysicalLabelFixtures`
checks decimal media geometry, CRLF and QR/barcode selection only; it is not
physical-print evidence. `TestWorkerRedirectsAfterVerifiedDHCPRecoveryWithoutDuplicatePrint`
proves zero-byte DHCP recovery with a durable endpoint event, while
`TestWorkerNeverRedirectsAfterPayloadByteOne` proves interrupted writes never
trigger redirection.

Physical ruler, QR/barcode scan, and copy-count checks remain manual cutover
gates. Administrator calibration, cutter, RTC, and font-download commands are
validated against discovered features and audited; no action adds `AUTO.BAS`,
reset, firmware update, arbitrary file execution, or ignored printer responses.
DHCP recovery probes only OS neighbor-table addresses with a matching verified
MAC and supported model; it never scans the network or rewrites desired config.

SQLite uses the pure-Go `modernc.org/sqlite` driver, including archive access.
Release artifacts are Windows AMD64, Linux AMD64 and Linux ARM64; each must
be a real `cmd/orrn-edge` executable. Publish explicit semver artifacts with
SHA-256 sidecars and `orrn-edge-release.json`; Docker references are pinned by
digest. A successful local build does not imply a published release exists.
Cross-compilation is not service verification. Before plant cutover, run the
Windows SCM lifecycle on a Windows VM and systemd plus native AMD64 artifact
checks on Linux. Do not treat QEMU failures or container health alone as proof
of native service readiness.

### Install

Deploy the matching ORRN `/api/edge/v1/*` control plane and D1 migrations before
enrollment. Create a company-bound node and supply its single-use token through
an owner-only file or stdin. No tunnel or public station hostname is needed.

For Linux, `packaging/linux/install.sh` reads `ORRN_EDGE_VERSION`,
`ORRN_EDGE_URL`, `ORRN_EDGE_SHA256`, `ORRN_API_ORIGIN` and
`ORRN_ENROLL_TOKEN_FILE`. The first three come from the verified release
manifest. It does not accept installer `--version` flags. Run it as root with
those variables preserved. Defaults are `/usr/local/bin/orrn-edge` and
`/var/lib/orrn-edge/{node.json,edge.db}`; state belongs to `orrn-edge` and node
credentials are `0600`.

For Docker, `packaging/docker/install.sh` requires `ORRN_EDGE_IMAGE` pinned
by `@sha256:`, plus API origin and a private enrollment token file. Preserve
the `/var/lib/orrn-edge` volume. Publish only the intended LAN fallback port;
do not publish the administration listener. Existing-install upgrades retain
the old container/configuration and take a consistent database backup before
health-gating the candidate.

Compose uses the same immutable image. Set `ORRN_EDGE_IMAGE`,
`ORRN_ENROLL_TOKEN_FILE`, `ORRN_EDGE_FALLBACK_PORT` and `TZ` explicitly. Mount
the token as a user-owned `0600` file. Successful enrollment truncates that
file before the daemon starts; a failure stops startup rather than exposing a
listener or silently reusing the token.

On Windows, verify the downloaded executable's hash and use an elevated shell:
`orrn-edge-windows-amd64.exe install --api-origin https://api.orrn.in`.
Enrollment and administrator bootstrap use stdin, not command-line secrets;
provide protected input. Installation registers the Windows service and
recovery actions. State remains under `C:\ProgramData\ORRN\Edge`; uninstall
preserves it.

### Operation and safety

- Admin UI: `http://127.0.0.1:8081/admin`; LAN fallback: port `8080`.
  Use SSH/local access to administer a VM. Bootstrap the administrator with
  `admin-password --state-dir ...` using protected stdin, then create site-local
  viewer/printer users in the admin UI.
- `healthcheck --url http://127.0.0.1:8080/healthz` requires enrolled identity,
  writable storage, applied config and complete stock/orders/dies/print-status/
  company-format snapshots. An initialized node remains locally usable during
  cloud outages; an unenrolled listener is not ready.
- Printing uses durable command resolution, per-printer serialized sessions and
  an event outbox. Never automatically retry `sent` or `outcome_unknown`: bytes
  may already have produced a label. Cancellation wins only before transmission.
- Fallback is cached reads and explicit eligible reprints, not offline stock,
  order, packing or dispatch mutation. Revocation disables fallback access and
  clears local grants/cache while preserving diagnostic history.
- Upgrade through the service-aware installer/`upgrade` path; keep the previous
  executable/image and consistent state backup. Never run two nodes with copied
  identity/state concurrently. Pi migration uses a native ARM64 release and
  stops the old node before starting the restored identity.
- Check existing listener ports before installing a pilot on a machine already
  running the legacy spool. No legacy system is stopped without authorization.

The following sections document the retained legacy `cmd/spool` API, not the
default Edge Docker runtime.

## Legacy spool features

- **Printer Management** - TCP/IP connectivity on port 9100, real-time status monitoring, health checks
- **Dynamic TSPL2 Generation** - JSON schema to TSPL2 conversion with variable substitution
- **AI-Powered Label Design** - Gemini 2.0 Flash integration for natural language label creation
- **Job Queue** - Atomic pending-to-processing claims, priority processing, and configurable workers
- **Print recovery** - Automatic retry only before device bytes; interrupted or partial writes fail for explicit operator retry
- **TSC status** - Serialized per-printer socket access with one-byte TA220 and four-byte status decoding
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

## Legacy spool quick start

### Prerequisites

- Docker and Docker Compose
- TSC thermal printer with network connectivity
- (Optional) Google Gemini API key for AI label design

### Run the retained legacy service

```bash
go run ./cmd/spool --config config.yaml
```

The legacy web UI uses `http://localhost:8080`. Do not run it on the same port
or state directory as Edge. The branch's current Dockerfile/Compose files build
Edge, not this legacy executable; preserve the existing deployed legacy image
when preparing a cutover.


### First-time Setup

1. **Set Password** - On first access, you'll be prompted to create an admin password
2. **Add a Printer** - Navigate to Printers → Add Printer
3. **Create a Template** - Navigate to Templates → New Template
4. **Print a Test Job** - Use the test print feature to verify connectivity

### Agni deployment

The isolated deployment uses an unprivileged Ubuntu 24.04 LXC, **CT 130**
(`orrn-spool`): 2 CPUs, 2048 MB RAM, 512 MB swap, and a 12 GB root disk on
NVMe `local-lvm`. It uses DHCP on `vmbr1`, VLAN 200 (observed address
`20.0.0.126`). Guest DNS is explicitly `1.1.1.1` / `8.8.8.8`; inheriting
agni's Tailscale resolver did not work before guest enrollment.

- Runtime files: `/opt/orrn-spool/compose.yaml`, `config/config.yaml`,
  private `config/spool.env`, and persistent `data/`.
- Source: `/opt/orrn-spool-src`, from local branch `deploy/agni-fourcubes`
  based on `b554beb`. Published `main`/`latest` does not contain this relay
  integration. Build this source, including the template and webhook fixes.
- Docker publishes only `127.0.0.1:8080` inside the guest. The admin password
  is generated privately in `config/local-admin-password`; do not commit or
  copy it into logs. No AI credential is configured.
- `docker compose --project-directory /opt/orrn-spool up -d --wait` manages
  the service. CT and Docker start at boot; guest restart recovery was exercised.
- Tailscale 1.104.2 is installed with `/dev/net/tun` device passthrough and
  authorized in agni's `bhalala.org` tailnet as
  `orrn-spool-agni.shrimp-prometheus.ts.net` (**`100.126.110.28`**).
  DNS acceptance, route acceptance, exit-node advertisement and Tailscale SSH
  remain disabled. CT→agni Tailscale ping and Mac→CT ping/TCP 22 were verified.
  Device key expiry is currently `2027-04-24`; expiry policy was not changed.

**Production enrollment is not implied by local health.** Fourcubes enrollment
still requires the signed-in ORRN platform-admin session, its company-bound
deployment secret and tunnel credentials, a separate `cloudflared` connector,
valid HTTPS for the enrolled hostname, and printer/template/profile bindings.
Check for an existing active company deployment before activating a new one.
Only publish signed API paths and `/health`; do not expose the legacy
unauthenticated `/print/:layout/:uid` route or first-time admin setup.

The TSC TA220 at `20.0.0.182:9100` was reachable from CT 130. Read-only printer
pages reported Ready, media **102.25 × 76.20 mm**, gap **2.00 mm**, gap sensor,
and thermal-transfer mode. No physical print or calibration was performed.
Confirm the installed roll and a single test label before production sign-off.

Legacy CT 120, VM 105 (BarTender), VM 181 and router VM 200 remain unchanged.
The legacy web app and BarTender host answered HTTP 200. VM 181's SATA disk
still has permanent ZFS corruption and its previous backup failed: retaining
that VM is **not** a verified backup. Do not put the new spool on `adata_array`.
No legacy print-proxy cutover has been performed.

#### Moving the verified node to a Raspberry Pi

Use a 64-bit Raspberry Pi OS/Ubuntu installation and build this source for
`linux/arm64`; the agni image is `linux/amd64` and cannot simply be copied.
After label and live-ORRN stability sign-off:

1. Drain the queue, stop the old spool and its connector, then securely back up
   `/opt/orrn-spool/data`, `config`, and `compose.yaml`. Stopping first gives a
   consistent SQLite copy, including WAL state; preserve the settings/encryption keys in that DB.
2. Restore the data/config bind mounts on the Pi, adjust the Compose image to
   the native ARM64 build, and run one connector using the enrolled hostname.
   Keep AI credentials unset unless separately configured.
3. Enroll the Pi as a **new** Tailscale device. Do not copy
   `/var/lib/tailscale` from the LXC or run two machines with one tailnet identity.
4. Verify signed API access, ORRN heartbeat, printer state, profile/template
   mappings and one physical label. Keep the LXC stopped but recoverable.
   Never run both print queues concurrently against the same printer.
5. For rollback, stop Pi spool/connector before restarting the LXC pair.
   Keep the legacy BarTender route unchanged until a separately approved cutover.


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

Create and update requests use `schema_json` (a JSON-encoded schema string), with
top-level `width_mm` and `height_mm` matching the schema. Responses include the
same `schema_json` field. Updates may change metadata alone; invalid schema or
inconsistent dimensions are rejected without changing the stored template.


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
    "description": "Product label",
    "width_mm": 100,
    "height_mm": 50,
    "schema_json": "{\"width_mm\":100,\"height_mm\":50,\"gap_mm\":2,\"dpi\":203,\"elements\":[{\"type\":\"text\",\"x\":10,\"y\":5,\"font\":\"3\",\"content\":\"{{product_name}}\"}],\"variables\":{\"product_name\":{\"type\":\"string\",\"required\":true}}}"
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

`X-Webhook-Signature` is the lowercase hexadecimal HMAC-SHA256 of the exact
transmitted JSON envelope (`event`, `timestamp`, `data`), using the webhook's
secret. The signature is not embedded in the JSON. Configuring
`ORRN_SPOOL_SUBDOMAIN` adds `X-Spool-Subdomain`, required by ORRN's callback
receiver. Configure its URL as the API origin plus `/webhooks/spool`, using the
same deployment secret; browser-session credentials are not machine credentials.

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
