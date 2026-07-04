# PrintOS — Protocol (cloud ↔ agent)

The contract lives in code at [`pkg/protocol`](../pkg/protocol). This doc
explains it. **v1 has no release message** — release is reserved.

## Versioning

`protocol.Version` is sent in every `Envelope`. Agents auto-update
independently, so the cloud must know which version each connected agent speaks
and adapt or refuse. Bump MAJOR on a breaking wire change; MINOR on a
backward-compatible addition. `MinSupportedAgentVersion` is the floor below
which the cloud tells an agent to update before receiving jobs.

## Job model (v1)

- **States:** `printing → done / failed / uncertain`. No held/ready state.
- **Mode:** `print_now` (only supported). `release` is defined but **rejected by
  the cloud** in v1 — present purely so adding hold-for-release later needs no
  schema change.
- **ClaimCode:** short alphanumeric stamped on the page corner (a code, not a
  name — privacy on a shared tray) and the handle for "report problem".
- **IdempotencyKey:** a re-sent job never double-prints; the agent dedupes on it.

## Messages

### Cloud → Agent
| Type         | Body            | Purpose                                             |
|--------------|-----------------|-----------------------------------------------------|
| `job_push`   | `JobPushMsg`    | Push a full job down the held connection            |
| `resolve`    | `ResolveMsg`    | Record how an uncertain job was resolved (audit)    |
| `update_now` | —               | Nudge the agent to check for a new build            |

### Agent → Cloud
| Type            | Body               | Purpose                                              |
|-----------------|--------------------|------------------------------------------------------|
| `job_ack`       | `JobAckMsg`        | Confirm durable receipt (written to disk); flags dup |
| `status`        | `StatusMsg`        | Report a job state transition + failure detail       |
| `heartbeat`     | `HeartbeatMsg`     | ~30–60s printer status, agent version, queue depth   |
| `report_problem`| `ReportProblemMsg` | Customer-raised problem for a claim code             |

## Happy path (print-now)

```
Customer pays
   │
Cloud normalizes → clean PDF, creates Job (mode=print_now), pushes job_push
   │
Agent writes job to SQLite [received]  ──► durability point
   │
Agent → job_ack (duplicate=false)
   │
Agent checks spooler ready; silent-prints via driver  ──► status: printing
   │
Spooler clean  ──► status: done
```

## Failure & uncertain

- Spooler reports jam / offline / out-of-paper → `status: failed` → cloud
  **auto-refunds**.
- Agent crashes mid-print; on restart it cannot confirm paper emerged →
  `status: uncertain`. Customer taps "report problem" (`report_problem`); the
  owner confirms printed / not-printed; cloud sends `resolve` and refunds if
  needed. **Never a silent reprint.**

## Durability invariants (do not weaken)

1. Persist the job to disk **before** printing — a crash can't lose a paid job.
2. Enforce the idempotency key — a retry can't double-print.

These two prevent the disputes that would destroy shop trust.
