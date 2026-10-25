# Print exactly-once (job key)

A label must come out of the printer once per intent, even when the caller
retries (timeout, dropped response, double-click, ORRN retry queue). The caller
sends a **job key**; the spool stores it uniquely and answers a repeat with the
first job instead of queueing another print.

## API

`POST /api/jobs` and `POST /api/templates/:id/print` accept:

- `job_key` in the JSON body, or
- the `Idempotency-Key` header (used only when the body has no `job_key`).

No key means no protection: the submission is an independent job, as before.

| Situation | Response |
| --- | --- |
| New key | `201` (`/api/jobs`) or `202` (quick print), `"duplicate": false`, the new job id |
| Key already stored, same label | `200`, `"duplicate": true`, the **first** job's `id`/`job_id` and its current `status`. Nothing is queued or printed |
| Key already stored, different printer/template/variables/copies | `409 job_key_conflict` with the first job's `id`. Nothing is queued |
| Malformed key | `400 invalid_job_key` |
| Two identical POSTs at the same instant | exactly one `201`/`202`, the other `200` duplicate (the unique index decides) |

A duplicate is answered before the printer is checked, so a retry of a label
that is already queued is never refused because the printer has since paused or
gone offline.

## Key format

1 to 200 characters from `A-Z a-z 0-9 . _ : / # @ = + ~ -`. No spaces, no
control characters, no non-ASCII. Keys are unique across the whole spool
(unique index on `print_jobs.job_key`, migration `002_job_key.sql`).

## What ORRN sends

One key per **print intent**:

```
bundle:<bundle serial>:label:<label version>:attempt:<attempt number>
e.g. bundle:B-24-001017:label:v3:attempt:1
```

- `bundle serial` is the bundle's serial exactly as printed on the label.
- `label version` is the version of the label layout used (so a changed layout
  is a deliberately new print).
- `attempt` starts at 1 and **only increases when a person asks for a reprint**
  (with a reason, recorded in ORRN). Transport retries of the same print reuse
  the same key; they must never increment `attempt`.

Reprint = new key (attempt + 1). A retry = the same key.

## Guarantees and limits

- Exactly one stored job per key, so at most one `print_jobs` row is ever sent
  to the printer for that key. A job that fails inside the spool is retried by
  the spool's own queue (same row), not by a new submission.
- The spool cannot tell whether a label that reached the printer physically
  came out (jam, empty roll). That is why a reprint needs a new key and a reason.
- Jobs created by the spool itself (UI reprint button) carry no key.
- Not part of this change: re-sending the signed result webhook until ORRN
  answers 2xx. Tracked separately in the build backlog (rank 53).

## ORRN side (note for the merger; not changed here)

`spool.printBundleLabel` (packages/server, the proc that POSTs to the spool)
must add `job_key` to the request body (or send the `Idempotency-Key` header):
build it as `bundle:${bundle.serial}:label:${labelVersion}:attempt:${attempt}`
where `attempt` is the count of explicit reprints for that bundle and label
version plus one (store it on the print log row, never derive it from a retry
counter), reuse the identical key on every transport retry or timeout re-send,
treat a `200` with `"duplicate": true` as success (record the returned job id,
do not show an error or enqueue another), and surface `409 job_key_conflict`
as a bug because it means the same key was reused for different label content.
