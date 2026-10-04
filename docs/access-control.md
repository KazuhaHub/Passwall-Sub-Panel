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
consistency are validated before any write. The optional sync boundary below
now uses this method. Seven focused repository tests passed after first failing against the stub;
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
Application wiring remains outstanding. This increment's [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37199488550)
at `ff182925ffb5429d345f6991e655fe2837454645` passed. Its full Test run was
superseded by the subsequent boundary commit, whose complete Test CI below passed.

The optional `nodesync.PolicyCoordinator` boundary now observes policy status
after control ingestion and before outbound compilation, then attaches only the
compiled Policy subtree and mints config plus candidate atomically. Partial
reports still observe policy status, and one-shot status is stripped from the
full-report cache. Policies and the atomic mint repository must be configured
together; both nil preserve the existing sync path. A compiler cannot emit a
policy without the node's current destination capability. Observation, compiler
or mint failure returns before a new config is offered. Five integration tests
first failed before implementation; full local nodesync, HTTP-handler, SQL-store
and policy suites plus relevant static checks pass. Tests inject the compiler
and real durable observer/repositories, proving that a rejection influences
the same response. The production compiler/state machine and application
activation remain outstanding. At `48bfb4becb0495089ef3e024efd67531008fa8eb`,
the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37199824644)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37199824648)
both succeeded, covering the durable observer and optional sync boundary.

The fallback-pruning increment adds pure `PruneFallback` against one published
definition snapshot. It preserves confirmed matches, action and rule order,
removes disabled/deleted policy sources, removes all rules of retired allowlist
groups and filters scoped subjects to the current roster without adding members.
Current published exemptions replace old exemptions; collection and revision
come from current capabilities, engine and panel settings. Pause takes priority.
An absent LKG returns nil or the supported usage-only candidate; malformed LKG
is observable as unavailable, and an invalid/over-limit pruned policy is rejected
before minting. Returned rule slices do not alias LKG. Five pruning tests first
failed against the stub and now pass, along with full local policy, nodesync,
SQL-store and HTTP-handler suites and static checks. The shared compiler/candidate
types now live in `ports`, avoiding service import cycles. An additional sync
test confirms that capability-less empty candidates preserve legacy bytes,
ETags and versions. At `c1afeb675f76a0f18657ac2ec9973cc33d2c4b54`,
the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37200158504)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37200158484)
both succeeded.

The retry-context increment persists `rejected_context` beside the actual
rejected generation. A same-generation core/sniffing retry can otherwise inherit
the old rejected lock after mint replaces `minted_context`; the separate field
retains the source of that rejection until desired success. Desired status
transitions and post-commit invalidation compare it, while source-only mint leaves
it untouched. One new durable repository regression first failed without the
field's behavior and passed after implementation, including a second rejection
with unchanged generation/digest but changed context. Desired apply clears it;
fallback success preserves it. `PolicyContext` hashes only publication generation,
the three relevant capabilities, desired core engine/version and enabled-listener
sniffing fingerprints. Task capabilities, roster, destination policy and unrelated
listener fields do not unlock a retry. Context tests first failed against the stub.
Latest local policy/SQL-store test executables were blocked by Windows Application
Control after compilation; security settings were unchanged. At
`b681d906d99d2cd1584925250f5117a8648b28da`, all backend CI checks succeeded.
The first frontend run had one installation-view test exceed 5 seconds; its
24-test file passed locally, and rerunning only failed CI jobs succeeded.
The [complete Test workflow, attempt 2](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37230887464)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37230887467)
are successful at that SHA.

The candidate-selection and compiler increment joins publication, current input
capabilities, node quota/preflight, durable decisions, lazy LKG loading and the
existing atomic config/candidate mint boundary. The independent quota candidate
uses all tag-matched members, surviving removal of ineligible roster clients.
Same-context rejection selects pruned LKG; exhausted fallback stays empty across
roster changes and restarts; core/publication/sniffing context changes retry
without erasing the original rejection before desired confirmation. Preflight
and full validation run on fallback too. `SetPaused` advances definition generation
atomically and idempotently. `PublishedState` reads its live pause flag and
selected published body in the same consistent transaction; pause changes force
publication before compiling and dominate LKG. Invalid new definitions retain
the prior valid snapshot without vetoing the live pause flag.

Five candidate tests first failed against the stub. Three production-compiler
sync tests use real SQL repositories and prove rejection/recovery, immediate
pause without replacing LKG, and rejected fallback exhaustion surviving compiler
restart. The initial compiler integration and pause repository tests failed
before implementation. Integration also exposed a report-only JSON empty-set
comparison bug: SQL `[]` and domain nil must be equivalent rather than triggering
confirmed-body validation against omitted blobs. A dedicated regression first
failed and now passes. Full local policy, nodesync, SQL-store and HTTP-handler
suites and relevant static checks pass; all three production integration tests
pass. At `e6579af86886afd42e46e6747ba35ebac848efb7`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37231717404)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37231717330)
both succeeded. The latter verifies the released Node baseline, not the unmerged
destination-policy Node implementation.

The production input provider reads panel collection mode/revision through a
credential-free four-column query and batch-loads only user/group IDs in chunks
of at most 500 IDs. Tag-matched quota membership comes from all members of groups
selected by enabled nodes on the panel, including disabled users without client
rows. It never uses eligibility or current roster presence to reduce quota.
Roster group scope uses current user rows, and collection-only/paused/old-node
paths avoid membership reads. Narrow-query guards, chunking/full-member tests,
tag-filter tests and the input-provider regression passed after their initial
stub failures. A real SQL sync integration proves full quota membership, current
scoped subjects, group moves and collection-off updates. Production input-provider
assembly is available; caching, issue/resync integration and application activation
remain outstanding.

The compiler caches one decoded immutable publication by published generation.
Every call still reads the small live state, retaining immediate pause and new
publication visibility; concurrent cold loads share one snapshot read. Failed
new-generation loads cannot return the old cached definitions or poison a later
successful reload. A real SQL query guard first failed on an idle snapshot read
before implementation; it now proves zero snapshot-body reads on unchanged
syncs and one read on a new publication. Concurrent-load/live-pause and failed
reload regressions pass, as do the full local policy/nodesync suites and static
checks. This caches only published definitions: full candidate, membership and
canonical ConfigBody caching remain outstanding.

User membership invalidation is now an optional synchronous service hook. Local
and SSO creation, committed deletion, administrator/profile group changes and
SSO rule-driven moves notify after persistence and before remote resync. Equal
moves, unchanged SSO logins, display-only edits and failed database writes do not
notify. Three focused regressions first failed on the missing creation hooks;
they now pass along with full local user/auth/policy/group suites and relevant
static checks. This provides the membership-generation event boundary; the
counter, cache provider and application wiring are still pending.

At `ad4e930a45277893bf8d2e8a336879fe49aabc3f`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37233436553)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37233436529)
both succeeded, covering the decoded publication cache and user membership hooks.

Membership inputs now cache identity-only roster mappings and full tag-matched
quota membership by panel, sorted roster IDs and an injected change generation.
Cold loads share one flight; results crossing a generation change are discarded
and retried, with an unavailable result after three unsettled reads. Errors are
never cached. Returned maps/slices are isolated from cached data, and panel
collection mode/revision stay live. The LRU holds at most 64 entries with an
8 MiB conservative identity-weight budget; oversized results remain complete
and uncached. Nil generation callbacks always read fresh. Group create/update/
delete now notify after committed writes, including a delete whose later scope
cleanup fails. Failed/rejected writes do not notify.

The membership/cache and group-hook regressions first failed before implementation.
Real SQL integration proves idle membership/tag reads are omitted, user moves
through the actual user service update policy, and node-region/group-filter
changes through their services invalidate quota membership. Disabled users
without clients still count. Full candidate/canonical-config caching and app
counter/activation wiring remain outstanding; this increment does not enable
the policy compiler in the application.
Full local policy/group/nodesync/HTTP-handler suites and static checks pass;
the actual-service integration, user/node suites and relevant static checks
also pass. At `db5554307dc76e7f7ce3bbf60fc6eaf9e7562834`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37234279420)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37234279421)
both succeeded.

The compiler now caches prepared policies and selected candidates separately,
each bounded to 64 entries and a 32 MiB conservative weight budget. Only inputs
with a tracked, matching membership generation are eligible. Keys include agent
and panel scope, roster identities, membership generation, publication/context,
collection mode/revision, engine and pause; selection additionally includes the
confirmed digest, rejection attribution and exhaustion/limit/precheck decisions.
Every call still checks current controls and durable runtime metadata. Selection
entries become visible only after the runtime transaction succeeds. Returned
policies are isolated from cached slices; untracked inputs remain fresh.

Nodesync caches canonical ConfigBody bytes by the policy-free base digest and
compiler key, bounded to 64 entries and 32 MiB. Listener/base changes and changed
compiler keys encode anew. Empty compiler keys and oversized payloads remain
uncached, and encoding errors cannot poison later attempts. Even a cache hit
calls the atomic candidate minter: source-only generation changes persist with
the same wire ETag, and transaction failures remain errors. Repository adapters
receive their own byte slice. The SQL minter still repeats canonical decoding,
policy validation and JSON serialization; eliminating that remaining idle cost
is a separate pending increment, with durable source checks retained.

Initial compiler and canonical-config regressions failed before implementation.
Tests now cover idle compilation/encoding, post-commit publication, changed
membership/context/collection/capabilities/roster, LKG digest changes, corrupt-LKG
recovery, panel-scope isolation, source-only mint and persistence failure. The
panel-scope regression exposed a missing panel key and first failed before its
fix. Weighted eviction/replacement and concurrent cache bounds are covered.
Full local policy, nodesync, SQL-store and cache suites and relevant static
checks pass. This increment awaits its own CI; application activation remains
pending.

This is a C3 foundation with an optional tested sync boundary. It does not
implement the full C3 acceptance: application assembly, issue/resync integration,
eligibility integration and membership-change invalidation wiring, remaining idle mint preprocessing, immediate pause UI/API wiring,
settings and application integration remain outstanding. Existing nodes still
receive the existing configuration because the compiler is not wired into sync.

Stage 1c still requires complete repository operations for multi-row service transactions and application wiring, policy compilation and candidate minting, fallback handling, settings/API boundaries, access-control views and complete browser acceptance. List services are not connected to app lifecycle, persisted settings or HTTP yet; C2's end-to-end acceptance remains incomplete. Audit ingestion, group modes, privacy/consent and subsequent stages remain governed by the full plan. Repository tests and green CI do not establish completion of these requirements.
