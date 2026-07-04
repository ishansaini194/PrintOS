# Contributing to PrintOS

## Prerequisites

- Go 1.25
- PostgreSQL 15+ (cloud)
- On Windows dev machines (agent testing): SumatraPDF, a real HP or Canon printer

## Setup

```bash
git clone git@github.com:<org>/printos.git
cd printos
go mod download
go build ./...
go test ./...
```

## Repository rules

These are load-bearing, not style preferences:

1. **`cloud` and `agent` never import each other.** They share exactly one
   package: `pkg/protocol`. If you find yourself wanting to import across that
   boundary, the thing you need belongs in `pkg/protocol` or `internal/platform`.
2. **`cmd/*/main.go` stays thin.** Entry points wire and start; logic lives in
   `internal/`.
3. **Protocol changes are contract changes.** Any edit to `pkg/protocol` may
   break every deployed agent. Bump `Version` and think about
   `MinSupportedAgentVersion` before merging.
4. **The agent persists a job to disk BEFORE printing**, and dedupes on the
   idempotency key. These two invariants prevent the disputes that destroy shop
   trust. Don't weaken them.
5. **Never auto-reprint.** An uncertain job is resolved by human confirmation or
   refund. "Surface, don't guess."

## Branching & commits

- Branch from `main`: `feat/…`, `fix/…`, `docs/…`, `chore/…`
- Conventional-style commit subjects (`feat: add heartbeat backoff`)
- Open a PR; CI (build + vet + test) must pass before merge

## Build order (reliability core first)

Work in this order; get 1–3 rock-solid before touching print quality:

1. Pull-connection + polling fallback
2. Auto-update + heartbeat
3. Persistent queue + idempotency
4. Silent print via driver
5. Printer-state reporting
6. Windows service wrapper

## First code

`pkg/protocol/` is already scaffolded. Next:
`internal/agent/queue/` (persistent SQLite queue) →
`internal/agent/conn/` (pull connection).
