# PrintOS — Decision Record

Condensed from the Master Decision Document (v1 scope locked). Where the earlier
Strategy & Decisions working document disagrees, **the Master doc wins**.

## Strategy

- **Software-first, not hardware-first.** Reuse shops' existing printers: no
  enclosure, no printer purchase, no hardware maintenance. Risk shifts from
  hardware/location to shop acquisition, agent reliability, and support.
- **Value proposition:** a shop's real income is form-filling, government
  document work, stationery, and exam services — not Rs 2 photocopies. Pitch:
  "Let PrintOS handle low-value print jobs automatically so your staff stays
  free for the work that actually pays."
- Kiosks (Print ATM) are a later phase on the same backend — not a rival.

## Architecture

- Flow: Customer browser → Cloud (Go) → Local agent (Go) → OS driver → printer.
- **The agent PULLS, never accepts inbound connections.** Outbound WebSocket
  (primary) + polling fallback. Sidesteps NAT / CGNAT / firewalls; makes the
  connection type irrelevant.
- Different stores by design: agent uses embedded SQLite; cloud uses PostgreSQL.

## Printing method (settled)

- **Reuse the shop's installed driver — never rebuild it (Approach B).** Hand a
  clean PDF to the driver; it handles rasterization, language, and best quality.
  Works even on cheap host-based / GDI printers where a raw approach fails.
- Silent print via SumatraPDF (`-print-to "Printer" -silent`) or Ghostscript.
  Start with SumatraPDF.
- **Cloud-side PDF normalization** removes per-machine format variability (the
  single biggest reliability decision). Quality-first via Ghostscript
  `/prepress`. DPI ceilings: mono 1200, grayscale 300–600, color 600. The driver
  downsamples any mismatch automatically — never an error.

## Job model — v1 (LOCKED)

- **v1 is PRINT-NOW only.** Jobs print immediately on payment.
- Hold-for-release / dedicated release terminal is **deferred**. The `mode`
  field is kept in the protocol (print-now only implemented) so release can be
  added later **without a schema change**.
- States: `printing → done / failed / uncertain`. The held/ready state is
  removed in v1.

### ⚠️ Resolved conflict between source documents

The Master Decision Document locks v1 to print-now. The earlier Strategy &
Decisions working document lists "print-on-release vs print-immediately" as
undecided and describes a collection/release token flow (its §4.5), and the two
project diagrams depict the hold-for-release flow.

**Resolution (team):** the Master Decision Document is authoritative. The
Strategy doc and the hold/release diagrams are **future scope**, retained for
reference only. v1 ships print-now; the protocol reserves `mode` so release
drops in later cleanly.

## Failure handling & refunds (settled)

- Agent polls the OS spooler. Clean completion → `done`. Reported error
  (jam / offline / out-of-paper) → `failed` → **auto-refund**. Crash with no
  record on restart → `uncertain`.
- **Never auto-reprint — in any case.** Uncertain jobs are resolved by human
  confirmation (owner sees the printer) or refund, never a silent second copy.
  "Surface, don't guess."
- PC shutdown mid-flight: job survives on the on-disk SQLite queue → agent
  auto-starts on reboot (Windows service) → resumes. A release/print attempted
  while the shop is offline is blocked by the cloud via the missing heartbeat.
  Idempotency ensures a re-sent job never double-prints.
- Hold/expiry window = 2 hours; expiry outcome = auto-refund. The cloud owns all
  money paths.

## Agent reliability core (the moat)

Pillars: **pull connection**, **persistent queue** (write to disk before
printing), **idempotency** (per-job key, no double-print), **heartbeat**
(~30–60s with printer status).

Self-healing: auto-update (build FIRST — turns every bug into a remote push),
auto-start on boot (Windows service), auto-reconnect with exponential backoff,
crash recovery (resume the on-disk queue).

## Platform & scope

- **Windows-first** (majority of Indian shop PCs, often old).
- **HP + Canon first**, then expand — bounds the driver test matrix.
- **Code-signing** deferred for the first hand-installed pilot shops; when done,
  use an OV cert (EV's SmartScreen advantage was removed in 2024). During the
  unsigned pilot, add a Defender exclusion per install and rely on the heartbeat
  to catch silent quarantine.

## Business track (co-founder owned)

Pricing (Rs 100–200/mo sub + Rs 1–2/txn), payment gateway + refund wiring
(Razorpay / UPI), shop acquisition (the real bottleneck), entity registration,
and trial→paying conversion tracking are **not** the technical founder's
department. Listed here only so nothing is lost.

## Immediate next step

Write `pkg/protocol/` (done) → `internal/agent/queue/` → `internal/agent/conn/`.
