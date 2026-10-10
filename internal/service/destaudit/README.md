# Destination audit ingestion

`App.Build` seeds current native collection controls and gives the same collector
to node sync. Sync freezes receipt time before waiting for its agent lock, then
offers valid telemetry after validating the control response and releasing that
lock. Offer and collection invalidation use memory only. The four queue lanes
retain their batch, byte and per-agent reservations while processing; the worker
yields after each 1,000-row transaction on the seven-slot schedule.

Committed collection edits update the cache while the shared panel gate is still
held. Admission and cache updates share the queue mutex. A changed generation
discards waiting payloads and cancels active payloads; equal-state notices retain
them. Every ingestion transaction checks the durable current permission again.
Native creation reads current state after acquiring the gate, so a late creation
notice cannot restore a deleted or subsequently edited panel.

Exactly one tracked worker owns data ingestion and loss flushing. Account lookup
loads IDs only, in queries of at most 200 IDs. Audit SQL tracing is suppressed and
storage errors become fixed categories. Diagnostic events contain counts, fixed
categories and authenticated agent/panel identifiers; they contain no accounts,
destinations, source addresses, SQL or driver error values.

Receiver-estimated lost rows use a separate 10,000-key buffer. Frozen flushes
contain at most 200 keys with an immutable ID, receipt time and increments. New
increments stay separate until the frozen flush is acknowledged. Ambiguous
commits retry the same identity; marker insertion and increments commit together.
Mapping and budget losses already committed in the first ingestion transaction
are not added to this buffer again. Node dropped/unmatched counters use event
units and remain separate from receiver row estimates. All history is best
effort and incomplete.

The app drains HTTP before stopping offers. Its audit worker context remains
alive after ordinary background cancellation, and the DB pool closes only after
tracked workers and admitted operations finish. The caller's shutdown deadline
cancels audit storage and reports remaining payload estimates and unsaved loss
keys. Each storage call joins online backend admission; idle time holds no permit.

The hourly maintenance pass applies configured hit/trial/usage retention and
matching loss retention at UTC hour boundaries. Trial sources are exact `g<id>`
identities. Legitimate anonymous trial history survives a return to open mode and
is removed when its group or panel disappears. Malformed anonymous rows are
orphans. Batch identity survives the full 72 hours from first receipt; budget
bucket flooring only delays deletion. Empty-agent `receiver_loss` markers are
excluded from Node orphan cleanup and expire solely by their receipt time.
Unreadable settings skip configurable age deletion while fixed retention and
orphan removal still run. Exemption orphan removal advances the definition
generation in the same transaction. Counters describe committed deletions only.

The diagnostics catalogue and both source language bundles cover receiver rows,
node events, unsaved loss keys and failed loss flush attempts. Failed flushes
retain increments; their attempt counter is separate from dropped keys. Unsaved
key counts cannot reconstruct historical lost rows.

The node coverage endpoint reads durable panel counters in one read-only
snapshot across both audit tables and all bounded panel-ID queries. A count
histogram and saturating integer arithmetic avoid SQL SUM overflow and rounding;
no account, destination or source values are loaded. The requested time range
selects overlapping UTC hour buckets. Hits include retained trial observations;
loss rows, dropped events and unmatched events are separate panel totals with
`complete:false`. Historical counts survive collection being switched off;
unsupported nodes keep unknown telemetry. Current collection still requires the
digest, revision, capability and freshness proof independently of these counts.

Focused verification:

```sh
go test -race -count=1 ./internal/service/destaudit
go test -count=1 ./internal/adapters/sqlstore -run '^(TestDestAudit|TestDestinationOrphanExemptionCleanup)'
go test -race -count=1 ./internal/app -run '^(TestBuildDestinationAudit|TestDestinationAudit|TestBuildPrunesDestRows|TestDestinationCleanup)'
```

The repository CI runs SQL tests against SQLite, MySQL and PostgreSQL. These
checks cover ingestion and maintenance; real Node deployment, traffic, mixed
versions and the record-page acceptance remain separate release requirements.
