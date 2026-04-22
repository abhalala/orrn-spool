# orrn-spool — Project Write-Up

> A production-grade, Dockerized print spool for TSC thermal printers — with AI-powered label design, a priority job queue, and a full web UI.

---

## What It Is

**orrn-spool** is a self-hosted print spooler built specifically for TSC thermal printers. It sits between your application and your printer network, exposing a clean REST API that lets any system submit label-print jobs without knowing anything about TSPL2 (the low-level command language thermal printers speak).

The headline capability: describe a label in plain English — *"shipping label with recipient address, 1D barcode, and QR tracking code"* — and the built-in AI designer (backed by Google Gemini) generates the full structured label schema for you in seconds.

---

## The Problem It Solves

Thermal printers are everywhere in warehouses, retail, and logistics, but integrating with them is painful:

- **TSPL2 is verbose and error-prone** — a mis-positioned element silently truncates or overlaps on the physical label.
- **No standard job queue** — fire-and-forget TCP writes drop jobs when a printer goes offline.
- **Zero observability** — you find out a job failed when a picker can't scan a barcode, not from a dashboard.
- **Label design requires specialist tooling** — proprietary Windows software or deep TSPL2 knowledge.

orrn-spool addresses all four: it abstracts TSPL2 behind a JSON schema, buffers and retries jobs automatically, provides real-time status monitoring, and lets you design labels with a natural-language prompt.

---

## Key Features

| Feature | Details |
|---|---|
| **AI Label Designer** | Describe a label in natural language → Gemini 2.0 Flash returns a validated JSON schema → rendered to TSPL2 and sent to the printer. Supports image-to-label: paste a reference image and the model reconstructs the layout. |
| **TSPL2 Generator** | Converts a JSON schema to TSPL2 commands. Supports 11 element types: `text`, `barcode`, `qrcode`, `pdf417`, `datamatrix`, `box`, `line`, `circle`, `ellipse`, `block`, `image`. |
| **Priority Job Queue** | Configurable worker pool with priority ordering, automatic retry with back-off, pause/resume per job or per printer. |
| **Printer Manager** | TCP connections to port 9100, real-time status polling, health checks, per-printer daily print counters. |
| **Webhook Notifications** | Event-driven HTTP callbacks (`job_started`, `job_completed`, `job_failed`, `printer_status_changed`) signed with HMAC-SHA256. |
| **Encrypted Job Archival** | Completed jobs are archived to encrypted files (age encryption + passphrase). Downloadable and restorable via API. |
| **Web UI** | HTMX + Tailwind CSS + Alpine.js interface covering every feature — no JavaScript framework required. |
| **JWT Auth** | Password-only setup flow with JWT stored in HttpOnly cookies. Single-admin model suitable for internal tooling. |

---

## How the AI Label Designer Works

```
User prompt  ──►  Gemini 2.0 Flash  ──►  JSON schema  ──►  TSPL2 commands  ──►  TSC Printer
"shipping label     (structured output       {elements:[…],      SIZE 800,400,2…
 with barcode"       forced via API)          variables:{…}}      BARCODE 10,20…
                                                │
                                      Validated by Go schema
                                      parser before any print
```

The Gemini client is configured with a domain-specific system prompt that:
- Explains coordinate systems for both 203 DPI and 300 DPI printers
- Lists all valid element types and barcode symbologies
- Forces structured JSON output (`responseMimeType: application/json`)
- Sets temperature to 0.7 — creative enough for layouts, stable enough to avoid hallucinated field names

The returned schema is validated in Go before any print job is created, so a malformed AI response never reaches the printer.

**Optional image input:** Users can paste a base64-encoded reference label image alongside their description. The model analyzes the image and replicates the layout while substituting variable placeholders — useful for recreating existing labels without manual measurement.

---

## Architecture

```
┌─────────────────────────────────────────────┐
│              Web UI  (HTMX / Alpine)         │
└────────────────────┬────────────────────────┘
                     │ HTTP
┌────────────────────▼────────────────────────┐
│            API Server  (Gin + JWT)           │
│  Printers │ Jobs │ Templates │ AI │ Archives │
└──┬─────────────────────────────────┬────────┘
   │                                 │
   ▼                                 ▼
┌──────────────┐             ┌───────────────┐
│  Job Queue   │             │ Printer Mgr   │
│  (workers)   │             │ (TCP monitor) │
└──────┬───────┘             └───────┬───────┘
       │                             │
       ▼                             ▼
┌──────────────┐             ┌───────────────┐
│ TSPL2 Gen    │             │  TSC Printer  │
│ (JSON→TSPL2) │────TCP:9100─►  (Network)   │
└──────────────┘             └───────────────┘
       │
       ▼
┌──────────────────────────────────────────────┐
│           SQLite  (WAL mode)                 │
│  printers │ print_jobs │ label_templates     │
│  webhooks │ print_counters │ settings        │
└──────────────────────────────────────────────┘
```

The application is a single Go binary with no external runtime dependencies beyond SQLite. Everything — queue workers, printer monitor goroutines, webhook sender — runs inside the same process, keeping the operational footprint minimal.

---

## Tech Stack

| Layer | Choice | Why |
|---|---|---|
| Language | **Go 1.22** | Single static binary, goroutines for concurrent printer polling, no GC pauses long enough to affect queue throughput |
| HTTP | **Gin** | Fast router, middleware chain for JWT auth, minimal boilerplate |
| Database | **SQLite + WAL** | Zero-dependency persistence; WAL mode handles concurrent reads from the queue workers |
| AI | **Google Gemini 2.0 Flash** | Structured JSON output mode, multimodal (text + image), fast enough for interactive use |
| Frontend | **HTMX + Tailwind CSS + Alpine.js** | Full interactivity without a build step; templates rendered server-side with Go's `html/template` |
| Encryption | **age** (via `golang.org/x/crypto`) | Modern, audited encryption for job archives |
| Container | **Docker + Docker Compose** | Single-command deployment; persistent volumes for DB and archives |

---

## Quickstart

```bash
# 1. Clone and configure
git clone https://github.com/abhalala/orrn-spool.git
cd orrn-spool
cp .env.example .env          # add GEMINI_API_KEY if using AI features

# 2. Start
docker-compose up -d

# 3. Open the web UI
open http://localhost:8080
# → Set admin password on first visit
# → Add Printer → Add Template → Submit Job
```

No external services required to run. Add a Gemini API key only if you want the AI label designer.

---

## Notable Engineering Decisions

### 1. JSON Schema as the label contract
Instead of exposing raw TSPL2 to callers, every label is defined as a JSON schema with typed elements and named variables. This lets callers (and the AI) work in a human-readable format while the generator handles the dot-coordinate math and command syntax. Variable substitution (`{{product_name}}`) is resolved at print time, so one template serves many jobs.

### 2. Structured output for AI reliability
The Gemini client sets `responseMimeType: application/json` in the generation config rather than parsing markdown-wrapped JSON from a free-text response. This makes the AI integration significantly more robust — the model is constrained to valid JSON from the start, and the Go validator catches any structural issues before they propagate.

### 3. Queue-first printer communication
Jobs are never sent directly to a printer. Every print goes through the queue, which means:
- Jobs survive printer downtime (they retry automatically)
- Priority ordering ensures urgent labels jump the queue
- Pause/resume lets operators hold a printer for maintenance without losing jobs

### 4. In-process everything
The queue workers, printer monitor, and webhook sender all run as goroutines inside the single Go process. There's no message broker, no separate worker process, no sidecar. For the single-tenant, moderate-throughput use case this targets (a warehouse or retail floor), this is the right tradeoff: simpler ops, easier debugging, lower resource use.

### 5. Encrypted archives as audit trail
Completed jobs are periodically archived to encrypted files. The design treats the archive as append-only long-term storage — jobs can be restored and reprinted, and the encryption means the archive files can be offloaded to cold storage without exposing print data.

---

## What I Learned

- **Prompt engineering for structured output** matters more than model choice. Getting Gemini to reliably return valid, coordinate-correct label schemas required iterating on the system prompt — specifically the coordinate math explanation (1mm = 8 dots at 203 DPI) and the explicit "return ONLY valid JSON" instruction. The structured output MIME type was the final piece that made it production-reliable.

- **Go's concurrency model is a genuine fit for device integration work.** Managing N printers each with their own TCP connection, status poll ticker, and reconnect logic is natural with goroutines and channels in a way that would be messy in most other stacks.

- **SQLite with WAL mode handles more than people expect.** The queue workers do frequent short writes (status updates per job step) while the API reads concurrently. WAL mode handled this without tuning — no "database is locked" errors under normal load.

---

## Future Improvements

- [ ] **Multi-tenant / multi-user auth** — right now it's single-admin; adding role-based access would make it usable in larger operations
- [ ] **Label preview rendering** — render a PNG preview of the TSPL2 output in the browser before printing, so operators can spot layout issues without wasting labels
- [ ] **AI feedback loop** — let the user mark a generated label as "wrong" and have the model refine it iteratively in the same session
- [ ] **Printer driver plugins** — currently TSC-only (TSPL2); a plugin interface would allow Zebra (ZPL), Brother (PT-Touch), or other brands
- [ ] **Print analytics dashboard** — daily/weekly volume charts, failure rate trends, per-printer utilization

---

*Built with Go 1.22, Gin, SQLite, HTMX, and Google Gemini 2.0 Flash.*
*MIT License — © 2026 ORRN.APP*
