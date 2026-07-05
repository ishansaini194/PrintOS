# PrintOS

Software-first platform that turns an existing print shop into a self-service,
pay-and-print endpoint using the shop's **own** printer.

Customers scan the shop's QR, upload, pay, and the job prints automatically on
the shop's existing printer. No kiosk, no enclosure, no printer purchase — the
platform reuses the shop's installed driver.

> **v1 scope is PRINT-NOW only.** Jobs print immediately on payment.
> Hold-for-release is deferred (the `mode` field is reserved in the protocol so
> it can be added later without a schema change).

---

## Architecture

```
Customer browser  →  Cloud backend (Go)  →  Local agent (Go)  →  OS driver  →  Printer
```

| Layer         | Responsibility                                                            | Tech                          |
|---------------|---------------------------------------------------------------------------|-------------------------------|
| Cloud backend | Auth, shop registry, job orchestration, payments, refunds, PDF normalization, source of truth for money | Go + PostgreSQL |
| Web app       | Upload, print options, payment, live job status, claim code               | Responsive web                |
| Local agent   | Pull connection, persistent local queue, silent print, heartbeat, auto-update | Go + SQLite               |
| Print path    | Reuse the shop's installed driver to render/print a clean PDF              | OS driver + SumatraPDF / Ghostscript |

### Core principle — the agent PULLS, never accepts inbound connections

Shop PCs sit behind home routers / CGNAT / mobile networks with no port
forwarding. The agent reaches out (outbound WebSocket, primary) and holds the
connection; the cloud pushes jobs down it, with a polling fallback for hostile
networks. This makes the connection type (WiFi / hotspot / mobile) irrelevant
and sidesteps NAT/firewalls entirely.

---

## Job model (v1)

States: `printing → done / failed / uncertain`

- **done** — spooler reports clean completion
- **failed** — spooler reports jam / offline / out-of-paper → auto-refund
- **uncertain** — crash with no record on restart → human check, **never** a silent reprint

**Never auto-reprint.** A job that might have printed is resolved by human
confirmation or refund, never a silent second copy. *"Surface, don't guess."*

---

## Repository layout

```
printos/
├── cmd/
│   ├── cloud/main.go      # cloud entry (thin — wire & start)
│   └── agent/main.go      # agent entry (thin)
├── internal/
│   ├── cloud/             # cloud-only: api, jobs, shops, payments, render, store
│   ├── agent/             # agent-only: conn, queue, printer, updater, health
│   └── platform/          # shared infra: config, logging
├── pkg/
│   └── protocol/          # THE CONTRACT both import: job.go, messages.go, version.go
├── migrations/            # PostgreSQL schema (cloud)
├── scripts/               # build, packaging, code-signing
├── build/                 # output binaries
└── docs/                  # design & decision records
```

`cmd/` = entry points only. `internal/` is Go-enforced private, split by binary —
**cloud and agent never import each other.** `pkg/protocol/` is the one coupling
point both import.

---

## Getting started

```bash
git clone git@github.com:<org>/printos.git
cd printos
go mod download
```

### Cloud host prerequisites (file normalization)

The cloud converts any accepted upload into one clean, optimized PDF. This needs
these system tools installed on the cloud host:

| Tool | Package | Purpose |
|------|---------|---------|
| `gs` | Ghostscript | optimize/standardize PDFs |
| `soffice` | LibreOffice | Office/text/image → PDF |
| `heif-convert` or `convert` | libheif-examples / ImageMagick | HEIC (iPhone) → JPG/PNG |

```bash
# Debian/Ubuntu
sudo apt-get install -y ghostscript libreoffice libheif-examples imagemagick
```

Build order (per the agent reliability core):

1. Pull-connection + polling fallback
2. Auto-update + heartbeat
3. Persistent queue + idempotency
4. Silent print via driver
5. Printer-state reporting
6. Windows service wrapper

Get **1–3 rock-solid** before print quality.

---

## Documentation

- [`docs/DECISIONS.md`](docs/DECISIONS.md) — master decision record
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — system design detail
- [`docs/PROTOCOL.md`](docs/PROTOCOL.md) — the cloud ↔ agent contract
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — dev setup & workflow

## License

See [`LICENSE`](LICENSE).
