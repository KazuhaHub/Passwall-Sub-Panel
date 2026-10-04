# Destination access control

Implementation follows the [final audit plan](https://github.com/KazuhaHub/Passwall-Sub-Panel/pull/271). The feature is under development; the schema foundation alone does not enable policy enforcement or destination collection.

## Current implementation

- Definition tables: `dest_lists`, `dest_policies`, `dest_exemptions`, `dest_group_modes`.
- Publication and runtime state: `dest_policy_state`, `dest_policy_snapshots`, `dest_agent_policy`.
- Durable audit storage: `dest_hits`, `dest_usage_hourly`, `dest_audit_batches`, `dest_audit_loss_hourly`, `dest_audit_ingest_budget`. Collection and ingestion are not connected yet.
- New tables participate in the normal boot migration. JSON columns use TEXT without defaults. Binary list content, original custom-list text, snapshots and policy bodies use SQLite BLOB, PostgreSQL BYTEA and MySQL LONGBLOB.
- Candidate bytes are stored independently from confirmed policy bytes. The latter are the basis for the later last-known-good fallback implementation; persistence does not yet implement that state machine.
- Native panel collection settings are stored as `audit_collect` (`off`, `hits`, `hits_and_usage`) and `audit_collect_revision`. Creation and migration initialize `hits` and revision `1`. A normal panel `Save` omits these columns.
- The native metadata writer validates collection mode and compares it under a transaction lock. A mode change increments revision atomically; repeated writes of the same mode do not. This write is not yet exposed by the HTTP request DTO. The future ingestion gate will use the same collection state to reject outdated batches.

## Definition and publication repository

The concrete destination repository now provides policy/list/exemption writes, per-action ordering, expiry removal, consistent definition reads and snapshot publication. These methods are not wired into the application or HTTP API yet.

- Definition writers acquire the singleton publication-state row before changing definitions. The row change and generation advance share one transaction; a failed write rolls both back. No-op writes and unchanged refresh digests do not advance generation.
- Policy/list edit versions advance by at least one millisecond. Column updates persist the exact returned version, including false booleans and nil expiry values. Policy priority is assigned by the server; reorder validates the complete action-specific ID set, including disabled policies, and invalidates affected edit versions.
- Refresh results compare the captured list version and source under the same lock. Deleted or edited sources reject old success and error results. Failures retain usable entries; successful unchanged content updates only metadata. Disabled policy and group references prevent list deletion.
- Definition reads start with generation inside a read transaction. MySQL and PostgreSQL explicitly use repeatable read. Publication compares both definition generation and previous published generation and commits the snapshot and publication state atomically. Error recording has the same version preconditions. Missing or malformed published snapshots return an unavailable error rather than an empty policy.
- Snapshot format compilation, policy validation, candidate minting, pause publication, global exception bundles and custom-entry edits remain application/service work. The repository's JSON integrity check does not establish semantic policy validity.

Local validation: thirteen new repository tests passed, along with the full SQL-store and domain suites and `go vet`. Initial tests failed against empty repository implementations; additional tests caught no-op generation changes and the ORM replacing explicit edit timestamps. Windows race execution is unavailable with the current CGO-disabled toolchain; Linux race and server-dialect results must be verified in CI for this implementation commit.

## Verified foundation

Commit `8d96dd67738c6d0901dded0b6064669c3561198c` passed the [full Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37186124723), including all three SQL dialects, race shards, static checks, release-target builds, frontend checks, published Node contracts and isolated third-party panels.

New tests verify all twelve durable tables, list bodies larger than 2 MiB, exact candidate/LKG bytes, nullable initial publication state, zero user/port identifiers for trial records, repeated boot migrations, additive panel defaults, preservation during stale ordinary saves, and concurrent collection-mode writes.

## Remaining implementation

Stage 1c still requires complete repository operations for multi-row service transactions and application wiring, list parsing/fetching, policy compilation and candidate minting, fallback handling, settings/API boundaries, access-control views and complete browser acceptance. Audit ingestion, group modes, privacy/consent and subsequent stages remain governed by the full plan. Repository tests and green CI do not establish completion of these requirements.
