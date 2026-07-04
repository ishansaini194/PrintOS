# PrintOS — Architecture

## Data flow

```
Customer browser
      │  upload, pay
      ▼
Cloud backend (Go + PostgreSQL)
      │  normalize → clean PDF, create job, push down held connection
      ▼
Local agent (Go + SQLite)   ── outbound WebSocket, agent-initiated ──┐
      │  silent-print clean PDF via installed driver                 │  heartbeat ~30–60s
      ▼                                                              │  status events
OS driver → existing printer                                        ─┘
```

## Why the agent pulls

Shop PCs sit behind home routers / CGNAT / mobile networks with **no port
forwarding**. If the cloud tried to reach *in*, it would fail on most real
shops. Instead the agent reaches *out* and holds an outbound WebSocket; the
cloud pushes jobs down that existing connection. A polling fallback covers
hostile networks that break long-lived sockets.

Consequences:
- Connection type (WiFi / hotspot / mobile) is irrelevant.
- No NAT/firewall configuration at the shop.
- The agent **never** listens for inbound connections — smaller attack surface.

## Cloud responsibilities

Auth, shop registry, job orchestration, payments and refunds, **PDF
normalization** (every upload → one clean, Ghostscript-optimized PDF), and it is
the **source of truth for money**. The agent never renders; it only silent-prints
a clean PDF the cloud produced.

## Agent responsibilities

Hold the pull connection, keep a **persistent on-disk SQLite queue**, silent-print
via the installed driver, emit a heartbeat with printer status, and auto-update
itself. The agent is the **source of truth for what physically printed**.

## The one coupling point

`pkg/protocol` — imported by both binaries, importing neither. It defines the
`Job`, `PrintSettings`, the job states, the wire messages, and the protocol
`Version`. Because agents auto-update independently, the cloud tracks each
agent's protocol version.

## Storage split (by design)

| Side  | Store       | Why                                             |
|-------|-------------|-------------------------------------------------|
| Agent | SQLite      | Crash-safe, zero-dependency on a stranger's PC  |
| Cloud | PostgreSQL  | Relational source of truth, money paths         |

Store code is **not** shared between them.

## Print path

Reuse the shop's installed driver (Approach B). The driver handles
rasterization, printer language, and best-quality output — including cheap
host-based / GDI printers that have no page language of their own. Silent
printing via SumatraPDF first, Ghostscript if finer tray/duplex control is ever
needed.
