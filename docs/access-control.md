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
- Snapshot format compilation and validation are now provided by the policy service below. Candidate minting, pause publication, global exception bundles and custom-entry edits remain application/service work. The repository's JSON integrity check alone does not establish semantic policy validity.

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

An empty remote result (including comments-only, empty payloads and entirely
ignored input) fails with `dest_list_empty`; it cannot overwrite an existing
usable list or be saved as successful content. Custom empty drafts retain their
separate behavior and require reference validation before becoming policy matches.

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
Control initially blocked the service test binary at `d44e45ca`. After the empty
remote-content regression was added and fixed, the new binary at `e7697f51`
ran normally without changing security settings: the complete destlist,
SQL-store, domain and safehttp suites passed locally. Relevant static checks
also pass. The [complete PR Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37192773685)
at `00e8678087c2db1090f424d15dd6f8db7793a536` succeeded, including Linux race,
MySQL/PostgreSQL and the fixed category fixture. Earlier SHA results remain
separate evidence.

## Policy compilation and publication service foundations

`internal/service/destpolicy` now builds deterministic ordinary candidates in
allow/exempt/block/group/observe order. It filters scoped subjects and exemptions
to the local roster, splits address and protocol matches, omits empty regular
rules, preserves group allow/catch-all order, and computes the collection level
and revision from current panel settings, capabilities and engine. Paused or
empty policies retain only explicitly supported usage collection.

Publication uses a consistent definition read and a generation/publication CAS.
It supports trailing debounce, maximum wait and forced publication, rechecks
the debounce window after the read, discards stale candidates/errors and retains
the prior publication on invalid definitions. The default builder checks active
aggregate rule/domain/regexp/CIDR quotas, inactive policy syntax and stored list
integrity, including broad entries. It checks scoped and catch-all rules with
synthetic subjects that never enter the saved snapshot. Actual subjects and
final encoded bytes remain node-specific checks. Snapshots have a versioned,
canonical format containing executable definitions without roster enumeration,
source URLs, administrator comments or parse reports. Semantic corruption or a
generation mismatch returns unavailable.

Xray sniffing preflight follows the shared Protocol conformance vectors and
only checks enabled listeners for domain or protocol rules. Sing-box and
port-only policies skip that check. Malformed listener data retains its existing
listener-error attribution rather than becoming a policy sniffing failure.

The sync handler now shadows `policy_status` with a raw JSON field, then validates
it separately after the control report. Invalid types, content or missing
capability discard only that observation; configuration and roster synchronization
continue. A bounded diagnostic reason is logged at most once per agent per
minute, and `psp_node_policy_status_dropped_total` counts each drop. Its diagnostic
catalog and explanations are present in both languages. Invalid control fields
and trailing JSON still reject the report.

Development pins Protocol PR #4's reviewed commit `01759871165c` through the
fetchable pseudo-version `v0.2.1-0.20261004033110-01759871165c`, with no `replace`.
The released-node `contract_source` remains unchanged as required for 1c; the
formal Protocol v0.3.0 dependency and later 1c′ contract update remain release
gates. Tests first failed against the new builders/stubs and typed status
decoder. Local Go 1.26.8 tests pass for policy compilation/publication/snapshots,
sniffing, sync handlers, nodesync and metrics. The diagnostics catalog's 23 tests
and TypeScript build check pass, as do relevant Go static checks. Three preexisting
SQLite test helpers now close their connections before Windows temporary-directory
cleanup. A separate destlist test invocation was blocked by Windows Application
Control for its new binary; security settings were not changed. These local
results alone do not establish CI results. The [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37196196283)
at `c1ef001b6eba4de39724601cd603227cb84a6db5` subsequently succeeded, including
all SQL dialects, Linux race, frontend checks and release-target builds. The
[published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37196196154)
also succeeded at that SHA; it verifies the released node, not the unmerged
destination-policy Node implementation.

The subsequent candidate-mint increment adds the narrow
`ports.NodePolicyCandidateRepo` boundary. `MintConfigWithPolicyCandidate` locks
the native-agent owner, then commits the config stream and exact policy body,
digest, source, generation and context in one transaction. Source-only changes
still update metadata when the wire ETag is unchanged, while confirmed LKG and
reported status fields remain untouched. Unchanged calls read only stream and
candidate metadata, omitting `desired_body`, `minted_body` and `applied_body`, and
perform no candidate write. Canonical config bytes, policy shape and metadata
consistency are validated before any write. This method is not called by sync
yet. Seven focused repository tests passed after first failing against the stub;
fault injection proves rollback in both write directions and query guards check
the idle blob path. The complete local SQL-store, policy and domain suites pass
for this increment. The [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37196767969)
at `b8ed6ad784b6704d58aa3515f9ba488c8f4522f4` subsequently succeeded, including
SQLite race and both server dialects. [Published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37196767923)
also succeeded at that SHA. These results are separate from `c1ef001b` evidence.

The next increment adds pure `ApplyPolicyStatus` transitions. Matching applied
status promotes the exact minted desired/fallback body and records its rules
and groups; only desired success clears the desired rejection state. A rejection
of a fallback uses the actual minted digest even after pruning changed it, then
clears LKG and sets exhausted. Stale statuses update only reported fields.
Paused, empty and collection-only candidates cannot replace LKG. Repeated settled
observations preserve timestamps and skip candidate decoding. Corrupt matching
candidate bodies return unavailable without partial mutation. Seven status tests
first failed against the stub and pass after implementation. This function has
now has successful [complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37197533086)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37197533146)
at `d1f887e54c83f9b878e88acfac48569f165b6185`.

The durable observer increment implements `ports.DestAgentPolicyRepo` and
`destpolicy.Observer`. Runtime changes lock the same native-agent owner as
candidate minting, load metadata first, and lazily read exact candidate/LKG
bodies inside that transaction only when a new transition requires them.
Runtime callbacks cannot overwrite the minted source. Confirmed rule/group
metadata must match the exact confirmed policy; failed writes roll back both
reported state and LKG. Cache invalidation runs only after commit and also
includes a change in rejected generation even when digest and reason repeat.
Repeated settled observations preserve timestamps, omit policy blobs and write
no runtime row, including after an observer restart. Ten repository tests cover
these behaviors, missing agents/bodies, stale reports and initial no-op state;
new boundaries first failed before implementation or repair. Full local SQL-store,
policy, domain, nodesync and HTTP-handler suites and relevant static checks pass.
Sync and application wiring remain outstanding; this increment's CI is pending.

This is an unconnected C3 increment. It does not implement the full C3 acceptance:
candidate mint and observer wiring, fallback
pruning, eligibility/member accounting, policy caches, immediate pause wiring,
settings and application integration remain outstanding. Existing nodes still
receive the existing configuration because the compiler is not wired into sync.

Stage 1c still requires complete repository operations for multi-row service transactions and application wiring, policy compilation and candidate minting, fallback handling, settings/API boundaries, access-control views and complete browser acceptance. List services are not connected to app lifecycle, persisted settings or HTTP yet; C2's end-to-end acceptance remains incomplete. Audit ingestion, group modes, privacy/consent and subsequent stages remain governed by the full plan. Repository tests and green CI do not establish completion of these requirements.
