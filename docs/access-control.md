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

Local validation: thirteen new repository tests passed, along with the full SQL-store and domain suites and `go vet`. Initial tests failed against empty repository implementations; additional tests caught no-op generation changes and the ORM replacing explicit edit timestamps. Windows race execution is unavailable with the current CGO-disabled toolchain. Implementation commit `9b47532422d77aae7db2cfae32579215d931c4e7` passed the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37188909110): MySQL/PostgreSQL full repository suites, all Linux race shards, static checks, frontend, release-target builds, published Node contracts, Docker baselines and isolated real third-party panels. The server-dialect tests include a writer committing between the generation read and the definition read, proving both reads remain in the older snapshot.

## Verified foundation

Commit `8d96dd67738c6d0901dded0b6064669c3561198c` passed the [full Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37186124723), including all three SQL dialects, race shards, static checks, release-target builds, frontend checks, published Node contracts and isolated third-party panels.

New tests verify all twelve durable tables, list bodies larger than 2 MiB, exact candidate/LKG bytes, nullable initial publication state, zero user/port identifiers for trial records, repeated boot migrations, additive panel defaults, preservation during stale ordinary saves, and concurrent collection-mode writes.

## List parsing, fetching and shared category cache

The new `internal/service/destlist` package parses custom text and remote rule
providers, canonicalizes and deduplicates entries, bounds body and entry sizes,
and produces a bounded parse report. Custom text ignores unsupported and broad
entries. A remote URL containing a broad entry fails as a whole. Clash records
use CSV parsing: quote values containing commas or use a raw `regexp:` entry;
ambiguous records are reported instead of silently truncating a regexp.

Remote downloads use the existing safehttp transport, require HTTPS through
redirects, limit redirects, enforce a 60-second request timeout and read one
byte beyond the body limit to detect overflow. Stored errors omit URL-bearing
transport error strings. Production requests cannot reach loopback addresses.

Per the sixth final plan and the owner's confirmed choice, geosite selection
matches attributes literally, strips their suffixes, removes broad entries and
retains a report with the original category rule ordinal. Effective category
counts exclude broad entries and duplicates. A completely filtered selection
returns `dest_list_empty_after_filter` with its report and no usable content.
Every consumer must handle that error before committing a refresh.

The shared cache verifies the upstream SHA256 before parsing and replaces
`<DataDir>/destlists/dlc_plain.yml` through a synced temporary file and atomic
rename. Concurrent refreshes share one operation; cached reads continue during
downloads. Failed downloads, checksum/parse failures and failed disk replacement
retain the usable catalog. Missing or corrupt caches return unavailable.
Restart restores the last successful local cache without a network request.

`dest_lists.parse_report` is nullable JSON TEXT without a default. A successful
save or refresh persists a separate copy of the report. Report-only changes
advance the edit version without advancing generation. Failed refreshes retain
the last successful report; stale refreshes cannot overwrite it. Legacy rows
without a report remain null until a successful parse is committed.

Validation first demonstrated failures for the real category, filtering/report
accounting, empty selections, cache operations and report persistence. The
implemented parser/fetch/cache suite, complete local SQL-store/domain/safehttp
suites and relevant `go vet` checks pass. A fixed, compressed upstream release
fixture runs offline on every test run, verifies the published checksum and
parses all 1,542 categories; category-finance has 612 retained entries and
excludes `domain:hsbc`. Its source and MIT license are in `destlist/testdata`.
These checks do not establish Linux race or MySQL/PostgreSQL results for the
new report/cache implementation; those require CI on this code's SHA.

## Remaining implementation

The list service now provides read-only previews, immutable-kind saves preserving
custom source text, pending remote sources on transient fetch failures, and
version-checked refreshes. A refresh round reads lightweight source metadata,
downloads the shared catalog once, selects each category and commits each result
against its captured version. Manual and scheduled refreshes use the same per-list
singleflight gate. Failed attempts are bounded by the current refresh interval;
changing a source allows a new attempt. The `dest-list-refresh` worker registers
through `safego.GoTracked`, rereads the supplied setting each round, wakes after
a setting notification and stops with its lifecycle context. It has not yet
been registered by the app or exposed by HTTP.

Service tests cover preview side effects, original source/report retention,
pending versus invalid remote sources, empty-category preservation, edits/deletion
during a slow fetch, shared downloads, interval changes, failed-attempt bounds,
tracked shutdown and recovery from a settings read failure. Windows Application
Control blocked execution of the newly built service test binary; local static
checks pass, but the service behavior and loop tests require the Linux CI run on
their implementation SHA before they count as verified.

Stage 1c still requires complete repository operations for multi-row service transactions and application wiring, policy compilation and candidate minting, fallback handling, settings/API boundaries, access-control views and complete browser acceptance. List services are not connected to app lifecycle, persisted settings or HTTP yet; C2's end-to-end acceptance remains incomplete. Audit ingestion, group modes, privacy/consent and subsequent stages remain governed by the full plan. Repository tests and green CI do not establish completion of these requirements.
