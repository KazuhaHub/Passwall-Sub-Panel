# Destination access control

Implementation follows the [final audit plan](https://github.com/KazuhaHub/Passwall-Sub-Panel/pull/271). Definition publication, native candidate compilation and list management are connected to the application. The feature remains under development: the remaining management interfaces, audit ingestion, browser acceptance and real Node kernel acceptance are pending.

## Current implementation

- Definition tables: `dest_lists`, `dest_policies`, `dest_exemptions`, `dest_group_modes`.
- Publication and runtime state: `dest_policy_state`, `dest_policy_snapshots`, `dest_agent_policy`.
- Durable audit storage: `dest_hits`, `dest_usage_hourly`, `dest_audit_batches`, `dest_audit_loss_hourly`, `dest_audit_ingest_budget`. Collection and ingestion are not connected yet.
- New tables participate in the normal boot migration. JSON columns use TEXT without defaults. Binary list content, original custom-list text, snapshots and policy bodies use SQLite BLOB, PostgreSQL BYTEA and MySQL LONGBLOB.
- Candidate bytes are stored independently from confirmed policy bytes. The compiler and candidate observer use the latter for pruned last-known-good fallback and durable exhaustion, as described below.
- Native panel collection settings are stored as `audit_collect` (`off`, `hits`, `hits_and_usage`) and `audit_collect_revision`. Creation and migration initialize `hits` and revision `1`. A normal panel `Save` omits these columns.
- The native metadata writer validates collection mode and compares it under a transaction lock. A mode change increments revision atomically; repeated writes of the same mode do not. This write is not yet exposed by the HTTP request DTO. The future ingestion gate will use the same collection state to reject outdated batches.

## Definition and publication repository

The concrete destination repository provides policy/list/exemption writes, per-action ordering, expiry removal, consistent definition reads and snapshot publication. Application assembly connects compilation and list refresh to this store. Settings, retry, list, policy, exemption and global exception management have HTTP boundaries.

- Definition writers acquire the singleton publication-state row before changing definitions. The row change and generation advance share one transaction; a failed write rolls both back. No-op writes and unchanged refresh digests do not advance generation.
- Policy/list edit versions advance by at least one millisecond. Column updates persist the exact returned version, including false booleans and nil expiry values. Policy priority is assigned by the server; reorder validates the complete action-specific ID set, including disabled policies, and invalidates affected edit versions.
- Refresh results compare the captured list version and source under the same lock. Deleted or edited sources reject old success and error results. Failures retain usable entries; successful unchanged content updates only metadata. Disabled policy and group references prevent list deletion.
- Definition reads start with generation inside a read transaction. MySQL and PostgreSQL explicitly use repeatable read. Publication compares both definition generation and previous published generation and commits the snapshot and publication state atomically. Error recording has the same version preconditions. Missing or malformed published snapshots return an unavailable error rather than an empty policy.
- Snapshot format compilation and validation are provided by the policy service below. Candidate minting commits with the native config stream; custom-entry edits and global exception bundles use the definition transaction. Manual publication and pause management are connected through administrator-only HTTP routes. The repository's JSON integrity check alone does not establish semantic policy validity.

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
policy validation and JSON serialization at this increment; the following
repository increment removes that remaining idle cost with durable source
checks retained.

Initial compiler and canonical-config regressions failed before implementation.
Tests now cover idle compilation/encoding, post-commit publication, changed
membership/context/collection/capabilities/roster, LKG digest changes, corrupt-LKG
recovery, panel-scope isolation, source-only mint and persistence failure. The
panel-scope regression exposed a missing panel key and first failed before its
fix. Weighted eviction/replacement and concurrent cache bounds are covered.
Full local policy, nodesync, SQL-store and cache suites and relevant static
checks pass. At `00252e64fc460da7611fdad441227a9790cea1bd`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37236223877)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37236223870)
both succeeded; application activation remains pending.

The atomic SQL minter now caches canonical-config validation by the actual
SHA-256 of the supplied bytes. It retains only canonical policy bytes, config
ETag, policy digest and the small policy shape used to validate mint metadata;
decoded listener configs and rule trees are discarded. A 64-entry/32-MiB
weighted cache shares concurrent cold loads; invalid input is never cached and
over-budget proofs remain complete and uncached. SHA-256 equivalence has the
same collision rationale as the existing stream idle path. Each call still
checks body size, source generation/kind/context/desired digest/collection mode,
timestamp and owner existence, and runs the stream/candidate transaction.
Cache hits cannot certify a committed source or bypass a failed SQL write.
SQL update callbacks receive isolated policy bytes.

The initial repository regressions failed on repeated decoding. They now prove
one decode across idle and source-only minting, rejected inconsistent metadata,
failed source-update rollback and recovery even when a callback mutates bytes,
uncached invalid canonical input, shared concurrent cold reads and uncached
over-budget proofs. Full SQL-store/policy/nodesync suites and relevant static
checks pass. At `96b3156432f453bd391752739919ad0703fd8e34`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37236758479)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37236758560)
both succeeded.

Optional allowlist resync hooks now expose the committed eligibility event
boundary. Nodesync compares only presence of `policy.destination.v1` before and
after persisting the current observation, including partial reports. Changes to
audit/task/host capabilities do not enqueue member resync. A later sync failure
does not erase the already committed capability change, and replay cannot
duplicate its notification. Failed observation writes do not notify.

Compiler and observer hooks notify only when the combined fallback-reason/
exhaustion state changes between eligible and blocked. Notification happens
after the owner transaction succeeds, including cached selection transitions;
failed transactions and idle/stale statuses do not notify. Observer retries
reset pending effects on each callback attempt. Desired recovery and exhausted
fallback without a reason-column change are covered by a real SQL regression.
The application callback must invalidate panel eligibility before enqueueing
asynchronous group resync and must not wait on the active agent sync lock.
Compiler callbacks provide agent identity for panel resolution; nodesync
callbacks already have panel identity. Nil hooks leave recovery to periodic
heal. These are event boundaries, not application wiring or complete membership
removal/eligibility enforcement.

Compiler and capability regressions first failed against the missing hooks.
Full local policy/nodesync/SQL-store/group suites and relevant static checks
pass. At `a86bc0bf17a8d9a71375096a9422bf90173f92ef`, the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37237494438)
and [published-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37237494444)
both succeeded.

Group selection now exposes one destination-aware eligibility decision. With
the destination reader configured, allowlist groups in both trial and enforce
select only native panels advertising destination-policy capability and having
neither fallback reason nor exhaustion. Open groups retain tag matching. Narrow
SQL reads exclude credentials and policy bodies; missing identities, corrupt
controls and database failures remain errors rather than ineligible verdicts.
Mode and panel facts use separate bounded caches with a 30-second TTL, shared
cold reads, uncached errors and invalidation that discards in-flight old facts.
Panel invalidation leaves unrelated facts warm; mode/stage changes clear both.

NodesFor, including its all-tags path, applies the same decision. New-node and
recreated-node member additions and reconcile's missing-ownership additions now
delegate to that selector. Reconcile checks the whole group's additions before
writing any, so a later eligibility read error cannot partially add members.
An AST guard rejects raw group.Matches references outside the group package,
while independent quota membership intentionally remains pure tag matching.

Initial eligibility and addition regressions failed before implementation.
Local cache/SQL/group/node/reconcile/user/render/policy/nodesync/HTTP-handler
suites and relevant static checks pass. Real SQL/service integration proves
rejection and recovery invalidate warm membership facts before resync reads,
and covers trial/enforce, capability removal and return to open mode. The app
suite's trusted-account wiring test fails on Windows temporary-database cleanup:
Shutdown does not yet close the application's primary database connection.
Its assertions pass; the lifecycle leak remains to be fixed. This increment
awaits its own CI. App assembly passes the shared selector to node/reconcile,
but does not yet configure destination eligibility or activate the compiler.

The application now owns its primary SQL pool explicitly. Failed assembly
closes it, while successful Shutdown closes it only after tracked background
readers/writers drain. If the shutdown deadline expires, draining and eventual
pool closure continue together rather than closing beneath an active worker.
The real-database lifecycle regression first failed because the pool remained
open. It and the formerly failing trusted-account fixture now pass, and the
complete local app suite and static checks pass. This lifecycle increment
awaits its own CI.

This is a C3 foundation with an optional tested sync boundary. It does not
implement the full C3 acceptance: application assembly, issue/resync integration,
eligibility activation, mode-transition orchestration and membership-change invalidation wiring, immediate pause UI/API wiring,
settings and application integration remain outstanding. Existing nodes still
receive the existing configuration because the compiler is not wired into sync.

Plan F54's member-removal path is now implemented: client provisioning reports
whole-panel retirement separately from replacement within a still-desired
panel. User membership resync deletes committed whole-panel retirements before
unrelated remote lifecycle/provisioning work, including partial local successes.
Replacement retirement retains the successful local-plan, lifecycle and
provisioning prerequisites. Eligibility read errors abort before plan writes
or any deletion. Deletion attempts every retired panel/client and returns joined
failures so user_resync persists a retry rather than relying on logs. Retained
empty attachment rows preserve counter identity and yield retirements on retry.

The initial removal regressions failed on cross-panel coupling and dropped
delete errors. Local clientprov/user/SQL/shared-client/group/nodesync/node/
reconcile/app suites and relevant static checks pass. Actual SQL, selector,
provisioner and user-service integration proves trial-mode removal despite a
different native panel's outage, deletion failure propagation into a durable
task, retry from the retained row and no deletion after an eligibility read
failure. Provisioner tests distinguish same-panel replacement and removed-panel
partial success; user regressions keep old replacements on failed provisioning
or lifecycle/local-plan writes. Head c3baee97 passed its complete Test workflow
and released-node systemd acceptance. Group-mode
CRUD, mode-transition orchestration and production policy/eligibility activation
remain outstanding; these tests do not establish complete stage 5 acceptance.

The destination settings boundary now implements GET/PUT
`/api/admin/dest/settings`, with partial writes under the shared settings lock.
PUT takes `{ "settings": { ...edited destination keys... } }`; the response
contains `settings`, `defaults` and `effective`. `ports.AccessControlSettings`
is held to every `dest_` field in UISettings by a prefix-based guard. The five
fleet settings are hit retention (30 days, 1–365), trial retention (7 days,
1–30), usage retention (7 days, 1–30), list refresh (24 hours, 6–168), and policy
apply debounce (60 seconds, 30–3600). Stored zero selects the domain default;
it never means permanent retention. Domain reads bound all values and cap trial
retention at hit retention. Writes reject invalid values and a trial window
larger than the resolved hit window, attributing that error to both fields.

Missing or null fields retain their stored values. Decode/validation failures
save nothing; refresh notification follows only a successful refresh-hours
change. The system settings page preserves destination values loaded under
the same lock and ignores stale request echoes. Risk-policy diagnostics keep
their own keys. Destination writes' audit rows are excluded from operator
queries, including totals and filtered pages. Real SQL-backed endpoint tests cover persistent zero/default
resets and stale-page preservation; guards cover DTO membership and every
destination field's ownership. Frontend API types/client calls are present,
but the S18 settings dialog and retention workers are still outstanding.

Settings head 582b8d5b passed the complete Test workflow and released-node
systemd acceptance. Application assembly now creates the definition store and
list service, loading the category cache from DataDir without a network fetch.
Run starts one tracked refresh loop. It reads bounded persisted refresh hours
each cycle, skips a round on settings-read failure, and is woken after a
successful refresh-hours save through the assembled settings route.

Refresh round, individual-list and category-download singleflights now drain
their underlying work on cancellation before returning to their owner. The
list service holds shared operation admission across downloads and final writes,
preventing an online backend switch from crossing an old backend's response.
Shutdown waits for background work and admitted operations before closing the
database; a caller deadline returns an error while eventual closure continues
after all owners exit.

The missing-assembly and premature-drain regressions first failed before the
fix. Real App Run/HTTP/SQL checks prove startup, due-row refresh status, actual
settings-save notification and a 168-to-6-hour change without a minute's wait.
A seeded insecure legacy URL is rejected before external network I/O, allowing
the test to observe the actual persisted worker result. Cancellation tests hold
target reads, final commits and downloads until their cleanup exits. Admission
tests keep exclusive operations outside that work; shutdown tests keep the real
database available to admitted readers until they release, including eventual
closure after a caller deadline. Lifecycle head 140092d1 passed its complete
Test workflow and released-node systemd acceptance.

Application Build now activates the same definition store in the compiler and
list service. Native sync receives both that compiler and its atomic candidate
mint repository. Group selection receives the narrow destination eligibility
reader; node additions and reconcile share that group service. Missing
membership, collection-control or atomic-mint dependencies fail assembly.
Publication debounce reads bounded persisted settings on every compile.

One membership generation is shared by policy inputs, committed user/group
changes and subscription-invalidating node changes. Capability-presence and
fallback transitions invalidate eligibility and rendered subscriptions before
tracked asynchronous member resync. Agent-to-panel resolution runs outside the
active sync owner's lock. Member/mode read failures enqueue no partial group
changes; periodic heal provides recovery.

Missing-compiler and missing-eligibility assembly regressions first failed
before the wiring. Actual Build/HTTP/SQL tests prove scoped subjects, durable
exact candidate identity, legacy config bytes/version/ETag stability, user-group
cache invalidation and a live 60-to-30-second debounce change. Warm-selector
checks cover capability gain/loss, rejection removal and restoration after a
new nonempty desired candidate is confirmed. Removal retains zero-attachment
roster identities for counters; scoped rules still use those subjects in a new
publication, so confirmation can restore their attachments. Publication alone
and empty-policy success retain rejection. No empty, paused or fallback success
is used to claim desired recovery.

Local app, policy, nodesync, group, user, node, reconcile, SQL-store and HTTP
suites and static checks passed. Assembly head ff1c4fb0 passed the complete Test
workflow and released-node systemd acceptance. Released-node acceptance does
not establish unmerged Node #78's kernel behavior.

Explicit retry is now available at admin-only
`POST /api/admin/dest/agents/:agent_id/retry`. It serializes with the entire
agent sync and atomically clears rejected/exhausted locks under the same SQL
owner lock as candidate minting. Current sniffing/limit failures remain intact.
Confirmed policy, exact candidate bytes/source and config stream are preserved.
Unknown agents return 404; fresh or already-reset agents return an idempotent
`retry_requested: false` without creating candidate provenance.

The reset retires `minted_at` as a receipt-confirmation marker until the next
candidate mint. Otherwise the old rejected receipt arriving before that mint
would immediately relock the requested retry. The sole config minter rearms
the marker even for identical candidate bytes. This explicit new attempt resets
its dispatch time; ordinary unchanged syncs retain the existing timestamp and
do not write policy blobs. Retry never invents an applied confirmation or changes
the confirmed policy's timestamp. Compiled/config caches and eligibility are
invalidated only after persistence, before asynchronous member resync.

Missing-retry and missing-rearm regressions first failed before implementation.
SQL tests prove metadata-only reads, atomic rollback, unchanged LKG/stream,
fresh/corrupt/missing-state boundaries, receipt retirement and same-byte rearm.
Real Build/HTTP/SQL checks prove administrator authorization, user/operator
rejection, stale-receipt suppression and warm-selector recovery. Coordinator
checks keep retry behind the full active sync while other agents remain
concurrent, and reject cancellation after waiting. Compiler tests preserve warm
caches on failed writes and rebuild after committed resets. All relevant local
Go suites/static checks and frontend TypeScript compilation passed. This retry
increment passed the complete Test workflow and released-node systemd
acceptance at bb9fa18b. True Node 1b/kernel retry acceptance remains pending; its frontend
API client is present, but the retry button and browser acceptance remain pending.

The definition repository now supports atomic group-mode initialization and
edits. First activation requires trial and creates both group-owned custom lists
with the mode and one definition generation. Ownership is derived from the
group, not caller-supplied list IDs. Existing owned contents survive close/reopen.
Edit versions advance even within one millisecond; unchanged edits leave the
generation and version intact. Switching to enforce checks list readiness,
including fetched remote/category references, while ready empty lists remain
valid. Shared lists may be selected; private lists cannot be reused by ordinary
policies or another group.

Deleting a group now removes its mode and all owned lists in the same SQL
transaction. A closed group's owned list can be deleted: its retained mode
pointer is detached and the edit version advances atomically. Reopening creates
only the missing list and preserves the other list's user edits. Active group
references and disabled policy references still prevent list deletion. Failed
generation writes roll back group/list/mode deletion together.

The missing-mode, private-reference, closed-list-deletion and group-cleanup
regressions failed before implementation. Actual SQL tests cover initialization,
stale/no-op edits, ready-empty enforcement, concurrent first activation,
cross-group ownership, preserved user content and injected write failures.
The complete local SQL-store, list, policy, group, user, node, reconcile, app and
ports suites and relevant static checks passed. Server-dialect/race CI passed at
owner-cleanup head e0f01fa1. These are repository foundations for P8 cleanup and later
stage-5 mode orchestration; no group-mode HTTP route is exposed yet. Template
DNS host discovery, commit-following eligibility/resync orchestration and the
trial-report prerequisites remain part of stage 5.

User deletion now removes its destination exemption and advances the definition
generation in the same transaction. Exemption writes check the user's existence
after taking that generation lock, preventing a concurrent write from recreating
an orphan exemption. A generation failure restores both the user and exemption;
deleting a user without an exemption does not advance the definition generation.

Converged native-panel retirement synchronously removes its runtime candidate
under the existing agent owner lock. Candidate cleanup joins the panel/agent
retirement transaction, so failure restores both identities and their runtime
state. Nonconverged retirement preserves the candidate. Runtime cleanup does not
advance the definition generation. Historical audit and consent cleanup remains
with the later retention/orphan workers.

Missing owner cleanup and concurrent orphan regressions failed before these
changes. The complete local SQL-store, user, group, policy, nodesync, app,
HTTP-handler and HTTP-router suites and relevant static checks passed. The
group-transaction head 14da448d passed released-node systemd acceptance;
its PostgreSQL lane failed two whole-struct comparisons that mixed caller UTC
timestamps with SQL-driver timestamp locations. Those comparisons now normalize
timestamps to UTC while retaining all content/identity checks. SQLite race and
MySQL lanes passed at that head. Owner-cleanup head e0f01fa1 then passed the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37260343995),
including PostgreSQL, and
[released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37260344016).

## List management API

Admin-only list routes now provide overview, create, preview, detail, versioned
update, delete, custom-entry edits and asynchronous refresh. Category routes
expose the cached catalog and queue a refresh. Missing catalogs return
`dest_geosite_unavailable`; ordinary users and operators cannot use these routes.
Unknown request fields are rejected. Custom originals retain comments and line
endings; editing requires the returned UTC-millisecond `updated_at`. Stale edits
return `dest_list_stale`. The decoded custom source remains bounded to four MiB;
route-specific JSON limits admit escaped content without raising other routes'
limits. Detail returns at most 200 normalized entries, preview at most 50;
only custom detail with `?text=1` returns original text. Overview includes report
counts, references (including disabled policies), effective global refresh hours
and the expanded definition budget. Subject quota uses each panel's full
tag-matched membership, including disabled users without client rows.

Custom-entry edits reread source and references under the definition lock,
then parse and save in one transaction, preserving concurrent additions.
Removing one hostname from a hosts line preserves its other hostnames.
Changed effective content advances generation once; unchanged content does not.
Source parsing and definition validation precede saves; transaction-local
reference checks reject broad custom content used by allow rules or active
allowlist groups. Community classifications still remove broad entries and
retain the report, including when used by allow rules. A filtering report is
not treated as a fetch failure. Remote sources retain whole-refresh rejection.

Queued and active refreshes expose `refreshing`; background dispatch owns them
after HTTP cancellation. Network work and final persistence share operation
admission, and canceled flights drain before releasing it. Failed refreshes
keep the last successful contents and persist their safe error code.

Missing-route, privacy and transactional-entry regressions failed before these
changes. Actual Build/HTTP/SQL regressions verify CRUD/CAS, source preservation,
large payloads, bounded responses, concurrent appends, referenced deletion,
quota rejection before persistence, full-membership overview, filtered finance
catalog editing and asynchronous refresh persistence. Service checks cover
queued/active state and exclusive-operation admission during downloads. Complete
local SQL-store, list, policy, app, handler, router and middleware suites, relevant
static checks and frontend TypeScript compilation passed. Final service and
middleware reruns passed; the App rerun could not launch because Windows
Application Control blocked its executable. The earlier complete App suite and
the subsequent focused App boundary tests passed. List API head 722a7f21 passed
the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37263135607)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37263135672).
Frontend API types are present; list views and browser
acceptance remain pending.

## Policy management API

Administrator-only policy routes now provide action-segment overview, create,
full-field PUT, delete, complete-segment ordering and readonly previews. Unknown
request fields and client-assigned priorities are rejected. Mutable booleans and
inline matching are explicit; PUT requires the last returned UTC-millisecond
version. Stale edits return `dest_policy_stale`, duplicate names return
`dest_name_taken`, and incomplete or duplicate reorder sets return
`dest_policy_order_stale`. Reorder includes disabled policies. New policies and
action changes append to their action segment; same-action edits preserve order.
Unchanged edits preserve generation and version. Source/group references and
definition quota are checked before persistence; private lists and empty custom
drafts cannot become ordinary policy matches. Pending remote/category references
retain their pending behavior. Repeated references are normalized before commit.
The SQL writer commits policy, priority and generation together; failed commits
do not change the caller's form or expose driver errors to the API.

Preview creates or replaces only a hypothetical definition, computes the shared
budget, and writes neither definitions nor audit rows. Quota uses full
tag-matched panel membership; candidate compilation uses the actual current
roster. Overview includes disabled policies, list readiness (including empty
and missing), missing scope, template identity, exemptions count and active
allowlist stages. The hit window is `min(7, effective hit retention)`.
Before stage 2c ingestion, `hits_recent` and `last_hit_at` remain null.

Missing endpoint/validation/authorization regressions first failed against SPA
fallback. Real Build/HTTP/SQL checks cover CRUD, stale/no-op edits, concurrent CAS,
priority changes, disabled-policy reorder, readonly create/edit previews,
duplicate names, empty/private references, quota rejection, missing scope,
pending sources and live hit-window settings. A committed HTTP policy is
published and synced to a native candidate, verifying subjects and exact durable
candidate digest. An injected generation failure proves HTTP rollback and safe
error responses; a direct failed service save preserves form identity/version.
Complete local app, policy, HTTP router/handler/middleware and SQL-store suites,
static checks and TypeScript compilation passed. Policy-API head
`461f8791cc5040007979e74da672cb132941b109` passed the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37265050796)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37265050807).
Real third-party panel jobs were skipped. Released-node acceptance does not
establish unmerged Node #78 kernel acceptance. Frontend DTOs/client methods are
included; policy views, browser acceptance and subsequent stages remain pending.

## Exemption management API

Administrator-only exemption routes provide list, detail, create, update and
delete. Creation records the authenticated administrator; request fields cannot
forge the creator or timestamps. Updates preserve the original creator and
creation time. Reasons are required and limited to 255 characters. Expiry uses
positive UTC milliseconds; null or omission on PUT makes the exemption permanent.
An already elapsed expiry is accepted and returned as expired. Duplicate creation
returns `dest_exemption_exists`; unchanged updates preserve generation.

List responses retain expired rows until cleanup and sort active exemptions
first. User names are resolved with batched reads of only user ID and UPN;
missing historical identities return null. These reads do not load list bodies,
credentials or entitlements. Owner existence is rechecked under the definition
lock, and each write commits its generation atomically.

Actual Build/HTTP/SQL tests cover authorization, creator protection, expiry,
permanent updates, duplicate and concurrent creation, no-op writes, deletion and
injected generation failure. An expired exemption still appears in a published
native candidate until durable cleanup removes it; republishing then removes
the subject and changes the candidate digest. This exercises the repository
cleanup path. The existing tracked hourly audit-cleanup loop now also removes
expired destination exemptions on its startup pass and every hour, before
certificate-event cleanup. The write shares operation admission with destination
management and backend switching. Failed commits retain rows and generation,
leave the delete counter unchanged and retry on a later pass; unchanged passes
advance neither generation nor the counter. Shutdown cancellation releases a
waiting admission without writing.

## Global allow exceptions API

Administrator-only POST `/api/admin/dest/exceptions` currently implements the
stage-1c global branch. Targets are normalized locally without DNS resolution.
URLs contribute only their hostname; `host` keeps an exact canonical hostname,
while `site` uses the registrable domain, including private public-suffix rules.
IP targets always produce one `/32` or `/128` address. Public suffixes, credentials
in URLs, unsupported schemes, zones and invalid ports are rejected.

First use creates the custom list and enabled global allow policy, puts that
policy first in the allow segment, preserves the order of existing allow
policies (including disabled policies), and advances generation in one transaction.
The response is 201 with both IDs in `created`. Later calls return 200 and append
against the fresh list under the same lock. Duplicate entries preserve content,
version and generation. Definition quota is checked before writing; late SQL
failure rolls back the whole bundle and priority changes.

The reserved `global-exceptions` template identity survives display-name edits.
Ordinary policy creation cannot forge it or erase it through an update. An
unrelated same-name policy is not adopted; creation chooses a unique policy
name. If the identified bundle has been disabled, repurposed or made invalid,
appending returns a conflict rather than silently changing those edits. The
group branch belongs to stage 5 and remains pending.

Missing-route and authorization regressions failed before implementation.
SQL-backed checks cover concurrent first use and append, same-name collision,
idempotence, reserved identity, explicit disablement, quota rejection and injected
policy/generation failures. An HTTP-created exception is published and synced
to a native candidate; the shared Protocol matcher allows the selected site
before the block rule while still blocking an unrelated site. This is candidate
and matcher evidence, not real packet or kernel acceptance. Complete local app,
SQL-store, list, policy, domain and HTTP suites, relevant static checks and
TypeScript compilation pass. Current exemption/exception-head CI remains pending.
Frontend DTOs and API clients are included; views and browser acceptance remain
pending. Status/test APIs and the remaining C4 work are
still required. The expiry-loop increment passed the complete local app/metrics
suites, static checks, TypeScript compilation and 23 diagnostics-catalog tests;
its own CI remains pending. Tests reproduce the missing loop call and verify
expiry versus permanent/future rows, one committed generation, failure/retry,
no-op passes and cancellation while backend-switch admission is held.

## Account access API

Administrator-only GET `/api/admin/dest/users/:id` returns the current group
identity, open/trial/enforce metadata and exemption, including an expired row
awaiting cleanup. A missing user returns 404; a missing group or historical
exemption creator returns null. An absent mode row means open mode; malformed
persisted mode data returns unavailable rather than a permissive response.

The account, group, mode, exemption and creator display identifier share one
consistent SQL read transaction under backend operation admission. The read
selects only account ID/UPN/group ID and group ID/name; it does not resolve user
entitlements or load credentials or private list bodies. It changes neither
definitions nor generation. Before later collection stages, `hits_available`,
`recent_hits`, `usage_available` and `usage_nodes` are present and null. Usage
query parameters are rejected until their separately audited stage-4 read is
implemented.

The missing-route regression first failed against SPA fallback. Actual
Build/HTTP tests verify group defaults, persisted trial mode, expired exemption
visibility, administrator boundaries, missing/invalid IDs and unchanged
generation. SQL query guards verify display-only reads, missing historical
identities and corruption errors without partial responses. Frontend DTO/client
methods are included; account drawer integration and browser acceptance remain
pending. Complete local app, SQL-store, HTTP router/handler/middleware and domain
suites, relevant static checks and TypeScript compilation pass. Current
account-access head `3f72eb2dea050babba05170c8f2233875b05b475` passed its
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37392283019)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37392283122).
Real third-party panel jobs were skipped; this does not establish unmerged
Node #78 kernel acceptance.

## Manual publication and emergency pause

Administrator-only POST `/api/admin/dest/publish` bypasses debounce and returns
the generation actually committed. Validation rejection returns 409
`dest_policy_over_limit` with structured `publish_error`, including invalid
definition fields or quota kind/used/limit. It retains the previous snapshot
and candidate. A corrected definition publishes normally and clears the stored
error. No-op publication preserves generation; a lost CAS retries from fresh
definitions, with at most three attempts before returning a conflict. Definition
read failures are not mistaken for commit conflicts or publication success.

Administrator-only PUT `/api/admin/dest/pause` requires an explicit boolean and
rejects forged fields. It commits pause and definition generation atomically,
then immediately attempts publication without depending on a debounce-setting
read. Repeated pause values preserve generation but can retry a pending
publication. Pause and resume leave all definitions and confirmed policy bytes
intact. The response includes generation, published generation, pause and any
publication error; successful publication is separate from node confirmation.

Invalid new definitions cannot veto emergency pause. A saved pause remains in
force when a snapshot write fails; the API returns 503
`dest_policy_publish_unavailable` with `pause_saved:true` and the saved pause
value, without driver details. Clients must refresh status after this response.
The compiler verifies the prior durable publication and creates a paused
candidate even if the new snapshot cannot be written. Missing/corrupt prior
publication storage still returns unavailable, and an unpaused compiler retains
normal publication-error handling. A failed pause-definition transaction leaves
both the flag and generation unchanged and does not claim the pause was saved.

Missing-route regressions first failed against SPA fallback. An injected
snapshot write failure then exposed a 500 node-sync response after pause had
already committed; the corrected path now stops policy execution while retaining
confirmed bytes, and an idempotent retry publishes the same generation after
storage recovery. Actual Build/HTTP/SQL checks cover forced publication,
validation rejection and recovery, immediate pause/resume, no-op writes,
authorization, forged requests, definition rollback and preserved candidates.
Service checks cover structured quota rejection, fresh/bounded CAS retries,
read errors, corrupt prior snapshots and committed publication metrics.

Complete local app, SQL-store, router, middleware, metrics and domain suites,
static checks, TypeScript compilation and 23 diagnostics-catalog tests pass.
Windows Application Control blocked the final service/handler test executables;
security settings were not changed. Earlier focused service tests ran normally,
but the current full service/handler suites require Linux CI on this increment.
Publication-control head `0ff2c3397fc8abace20c21bc2cc4c0654bbd384c` passed its
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37394505871)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37394505648).
Real third-party panel jobs were skipped; this does not establish unmerged
Node #78 kernel acceptance. Frontend DTOs/client calls are included; the
publication/pause controls and browser acceptance remain pending.

## Server collection preferences

The existing administrator-only server list includes `audit_collect` on native
PSP rows. Third-party rows omit it. PUT `/api/admin/servers/:id` accepts `off`,
`hits` or `hits_and_usage` only for native PSP servers. Omitted or null values
preserve the saved preference. Invalid values and explicit third-party modes
fail before any submitted display metadata is saved.

The HTTP path uses the existing narrow native metadata writer. Collection mode,
its revision and any submitted display metadata commit in one SQL transaction.
Only a changed mode increments revision; repeated saves preserve it. The
client cannot assign revision, and normal full-row saves continue to omit both
collection fields. An unavailable narrow writer returns 503 instead of reporting
a successful save that silently discards the preference. Failed SQL writes
preserve all prior fields and return an opaque error. A failed pool replacement
restores the prior mode through the same writer, retaining monotonic revision.

Saved collection settings remain distinct from effective node collection. Every
sync reads the current mode and revision, so a warm candidate cannot reuse an
older collection epoch. Turning collection off retains enforcement rules;
re-enabling uses the new revision. A node reporting only hit capability receives
hits even when the saved mode requests usage. Collection changes advance neither
definition nor publication generation.

Initial actual-HTTP regressions proved ignored collection input could still
commit submitted display metadata. Focused Build/HTTP/SQL and handler checks now
cover saved DTOs, omission/null, no-op revision, off/re-enable, invalid modes,
third-party rejection, administrator boundaries, storage rollback, pool rollback,
missing atomic writers and warm native candidates with capability downgrade.
Complete local app and HTTP-handler suites, relevant static checks and
TypeScript compilation pass. Windows Application Control blocked the HTTP-router
test executable; security settings were not changed and Linux CI must complete
that coverage. Server-collection head `64441286327f3e00132486c689e04e293ee493f3`
passed its [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37395531055)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37395531369).
Real third-party panel jobs were skipped; these results do not establish unmerged
Node #78 kernel acceptance. The recording controls remain deferred to the planned
collection UI stages; audit ingestion and its shared collection gate still
belong to stage 2.

## Destination simulation

Administrator-only POST `/api/admin/dest/test` evaluates a domain or literal IP
through `protocol.MatchDestination`. The optional port defaults to 443 and network
to TCP. HTTP/HTTPS URLs contribute only their host; paths and URL credentials
cannot influence matching. Unicode names normalize through IDNA. Validation rejects
malformed targets, networks, ports, identities and unknown request fields.

Executable rules come only from the selected published snapshot. Saved unpublished
changes set `unpublished:true` and do not alter the result. Live emergency pause
still takes priority. The simulation never publishes, invokes the runtime compiler,
or mints config/roster streams. Its normal write-audit capture remains enabled.

An explicit panel selects that panel's current client scope. A user without an
explicit panel selects the first native panel with a client, ordered by panel ID.
If no native client exists, the result is untestable with `no_native_client`, rather
than inheriting an anonymous scope. With neither selection, anonymous virtual
evaluation has no group or exemption membership. Current roster subjects and group
membership are read alongside publication and display/status metadata in one
consistent SQL transaction. User, panel, agent and client credentials, listener
configs, current list originals and minted/confirmed runtime bodies are not read.

The response preserves Protocol hit/miss/n/a/shadowed/skipped/untestable traces,
source policy/group IDs, available display names and matching list-entry provenance.
An earlier protocol-dependent rule makes the destination-only verdict untestable
and removes a definitive terminating step. Node states accompany the logical
result: a new publication is pending until the node confirms its candidate; a
logical match alone does not prove what a live packet will do. Deleted display
names remain absent while published source IDs remain available.

Initial missing-route regressions failed against SPA fallback. Actual Build/HTTP
checks now prove published-only behavior, current membership, bounded validation,
missing owners, safe corruption errors, audit retention, unchanged definition and
stream/runtime state, and applied-to-pending transition after new publication.
A resolver interception proves these in-process requests make zero DNS attempts;
domain targets do not match addresses derived from their names. SQL query guards
prove transaction ownership and narrow reads. Service checks cover URL/IP
normalization, source provenance, shadowing, exemptions, group trial, anonymous
scope and protocol uncertainty. Complete local app, policy, SQL-store, handler,
router and domain suites, relevant static checks and TypeScript compilation pass.
Simulation head `9982b46ec39b1e661f5d65e8c9e19e70903c6fc1` passed the
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37397780498)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37397780269).
Real third-party panel jobs were skipped. The S13 drawer, browser acceptance and
VM packet-capture/real-node comparisons remain outstanding under the final plan.

## Destination fleet status

Administrator-only GET `/api/admin/dest/status` returns the current definition
and published generations, pause/error state, publication deadline and server
calculated ETA. ETA uses the same debounce/maximum-wait deadline as publication,
plus the effective native-node poll period. Missing pending-write timestamps fail
unavailable. All timestamps are nullable UTC milliseconds.

Node metadata exposes capabilities, saved/effective collection levels, candidate
and fallback state, pending/applied times, last heartbeat, confirmed rule/group
counts and narrow listener display labels. Totals count each node state plus
`total` and `collecting`. Missing historical group names are null. A newly
published generation is pending until the node's corresponding candidate is
confirmed. Confirmed empty/paused candidates show no executing rules or groups,
while the stored LKG remains intact for resume.

`collecting` requires an online Xray agent, matching capabilities and current
collection preference, applied acknowledgement of the exact minted digest, and
canonical digest-verified candidate bytes with block/observe rules. Candidate
collection revision must equal the saved panel revision: an off/on transition
cannot reactivate an old acknowledgement. Allow-only candidates and sing-box do
not prove hit recording. A pending newer publication can coexist with recording
from a still-confirmed previous candidate; these facts are reported separately.

One repeatable-read transaction reads fleet metadata and lazily loads only
eligible current candidate bodies. A 256-entry cache retains verified facts,
not executable bytes. Every request rechecks current metadata and collection
revision. Reads exclude credentials, client data, list originals, snapshots,
listener configurations and LKG bodies; they do not publish, compile or mint.
Before later ingestion stages, `hits_24h` and `losses` remain null.

The initial HTTP regression failed against SPA fallback. Full local SQL-store
and domain suites, relevant static checks and frontend TypeScript compilation
pass. Windows Application Control blocked application, policy and HTTP test
executables. Status head `bdc134d9c85eae48b19243fae35d1f3c91d55479` passed
the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37399543455)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37399543473).
Linux CI completed all blocked test coverage; real third-party jobs were skipped.
The status page and coverage drawer are described below; complete browser and
real-node acceptance remain outstanding.

## Compiler and list-refresh diagnostics

The remaining stage-1c metrics `psp_dest_policy_compile_total`,
`psp_dest_policy_compile_ms` and `psp_dest_list_refresh_total` now record actual
compiler calls and executed refresh work. Compiler outcomes use the seven
planned labels, with transaction failures counted as `invalid` rather than
successful cache hits. Refresh outcomes use four planned labels; remote broad
rejection retains old content and category filtering retains its report.
Previews and joined refresh waiters do not create extra refresh attempts.
All families and bounded label values share the existing native-node diagnostic
card, both language bundles and catalog consistency tests. Exact semantics are
documented in [observability](observability.md#49-目的地编译与列表刷新指标).

Initial actual compiler/refresh regressions failed before implementation.
Full local compiler, list-service and metrics suites and focused catalog tests
pass. This increment passed the [complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37402299093)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37402299028).
Counters do not establish
node enforcement, recording availability or later ingestion-stage completion.

## Settings dialog foundation

The stage-1c S18 `AccessSettingsDialog` now reads the administrator-only settings
endpoint, keeps zero as an empty default field and sends only changed keys.
It validates integer ranges and the effective trial/hit retention relationship,
maps server field errors and confirms reduced effective retention before writing.
Dirty close and navigation preserve edits on cancellation. Navigation waits for
an active confirmation or save instead of opening a competing prompt; failed
writes preserve the draft. Saved settings invalidate the same session's settings,
status and general UI-settings caches.

The shared numeric `PolicyField` now displays server errors; strict integer
parsing is opt-in for destination settings, preserving existing policy-field
typing. The dialog has loading/retry/unwired states, requested-field focus,
mobile full-screen layout and Chinese/English copy. Initial tests exposed the
missing numeric error display and competing navigation confirmation before
their fixes. The dialog is now invoked from the access-control page. Complete
browser screenshots and owner approval remain pending; this does not complete
C5 or the stage-1c screen matrix.

Protocol fields and capability/receipt boundaries are documented in
[the native-node extension](psp-node-agent.md#11-目的地访问控制扩展开发中).
The [upgrade guide](UPGRADE-v4.md#访问控制版本的升级与降级) now includes the
required warning: downgrading PSP withdraws allowlist guarantees, while offline
or failed-deployment nodes may continue enforcing an old artifact. Actual
per-node upgrade/downgrade acceptance remains pending.

## Policy management page increment

The administrator-only `/admin/access-control` route, capability and sidebar
entry now open the real policy page. Operator/user guards run before destination
reads, and sidebar prefetch excludes the operator. Policy creation, editing,
full-field/versioned enablement, copying, deletion and whole-segment ordering
use the destination administration APIs. Editor conflicts preserve the draft
until explicit reload; duplicate names attach to the name field. CIDRs are
validated by original line number in a lazy CodeEditor with an accessible label.
Debounced, abortable previews expose the server budget. Over-limit previews
prevent saving; failed preview reads leave final validation to the save endpoint.

The first-enable confirmation uses `published_generation` and
`published_has_access_control` from the selected validated snapshot. Saved
enable/disable changes do not pretend to change this published fact. Published
allowlist-only definitions count, and pause retains enabled definitions for
resume. Snapshot reads are confined to the overview, so a corrupt old snapshot
does not block draft preview/write repairs. Read failures do not fabricate a
first-enable answer. No schema or protocol field changes are needed.

The status verdict distinguishes publication, application, unsupported panels,
failed preflight and list readiness. Only outstanding publication/candidate
acknowledgements poll; the countdown is excluded from the primary live region.
Pause publication failure reports the committed pause state without claiming
fleet convergence. The coverage drawer uses five state filters, exact fallback
acknowledgement conditions and explicit retry. Filter replacement preserves
owned history, and closing a cold deep link removes its node filter. Settings
open from the same page. Three inline templates prefill editable policies.

This is a functional increment, not completion of S1–S3/S14 or C5. Category
templates, exemptions, destination simulation,
account access inspection, target-page deep links and the remaining final-plan
screen details still need integration. Recording controls and stage-5 allowlist
tabs remain scoped to their respective stages. Complete browser screenshots,
owner approval and actual policy-node acceptance remain outstanding.

Validation for this increment includes 1,908 passing frontend tests (one skipped),
followed by 20 passing focused cases after the final responsive changes, passing
TypeScript/lint/production build checks, full destination-service and HTTP-handler
tests, and destination application integration tests. The published-fact and
corrupt-snapshot repair regressions failed before their fixes. Browser fixture
previews checked the desktop Chinese page and 375px editor/coverage layouts,
English dark-mode editor/discard confirmation, live announcements and horizontal
overflow. These fixture checks do not replace live backend/node or the complete
final-plan screenshot matrix.

## List management increment

The administrator page now has policies and lists tabs. List management uses
the administrator APIs for creation, versioned editing, deletion and explicit
refresh. Failed first downloads are distinguished from failed refreshes that
retain previous content. The problem filter includes failed lists and pending
lists referenced by enabled policies; filtered community entries are a warning,
not a failed list. References and group ownership prevent deletion.

Custom editing loads original source text with comments and line numbers.
Abortable previews start after 500ms; only settled content owns a preview cache
entry. Remote previews run on explicit test-fetch actions and do not refetch on
focus, reconnect or cache invalidation. HTTPS validation happens before that
request. Temporary fetch failures permit saving the source for retry; known
broad, empty, oversized or invalid remote content blocks submission. Community
data is downloaded only on request and attributes retain literal names such as
`!cn`. Community broad entries are excluded and shown in an amber report; valid
remaining entries can be saved. Empty filtered categories block submission.

Persistent reports show bounded source samples, normalization replacements and
omitted sample counts. Custom report line buttons reveal the source line in the
plain-text CodeEditor. Preview and detail responses expose the complete
canonical SHA-256 independently of their first 50/200 entry samples. Detail type
totals cover the full list; older responses without those totals clearly label
sample-only counts. Search stays in component state. Conflicts preserve the
draft until explicit reload. Saving opens the resulting entry drawer after the
editor's navigation guard unmounts. The phone drawer occupies the full viewport.

All current mutation hooks have session-isolation and dependent-cache tests,
including conflict invalidation and active preview non-refetch behavior. A
refreshing list polls every five seconds while visible, stops when settled and
refreshes an open detail drawer when its overview changes. Settings, list,
policy, publication and retry mutations remain separately scoped.

Local validation: 1,951 frontend tests passed (one skipped), followed by 151
focused checks after browser fixes; TypeScript, lint and production build
passed. Full destination-list service and HTTP-handler tests passed. Application
integration verified canonical identity, complete type totals beyond the 200
sample bound and original normalization metadata. A subsequent application
rerun was blocked by Windows Application Control; current-head Linux CI remains
the final verification for that run. Chinese desktop/375px list, entry and
category-editor fixtures, English dark custom editing and discard confirmation
were checked in the browser. Fixtures do not prove live backend/node acceptance.

Remaining list-screen details include reference navigation, destination testing
from an entry and focusing the refresh interval in settings. The broader C5,
actual policy-node acceptance and full screenshot/owner approval requirements
remain outstanding. Green CI does not complete the approved plan.

## Observation conversion increment

Observation rows now offer a conversion dialog that uses the existing full-field,
versioned policy PUT. It preserves match conditions, list references, group scope,
template identity and enabled state; the risk checkbox is an explicit choice and
starts unchecked. Stale writes preserve that choice until explicit reload. A
reload that finds another action prevents conversion. Pending saves prevent
duplicate writes and closing, and canceling a changed checkbox uses the existing
discard guard.

This stage-1c dialog explains blocking order and impact without requesting hit,
record or usage data or presenting invented counts. The success notification
uses a position only when the refreshed policy overview contains the converted
row, and explicitly awaits publication and node acknowledgement. Desktop and
375px Chinese layouts, English dark-mode disabled-policy wording, conversion
success and discard behavior were checked with browser API fixtures. These
fixtures do not establish live backend or node acceptance; stage-2c impact data
and the complete screen matrix are still outstanding.

Validation: 1,961 frontend tests passed (one skipped) with a single worker after
an earlier Windows worker exit; the affected installation-materials file also
passed all 14 tests independently. The 72 focused page/query/source-guard checks,
TypeScript, changed-view lint and production build passed.

## Category-template increment

The empty policy view now offers five templates and a financial-category
explanation. BT and TCP mail templates block and count as risk; private/cloud
metadata blocks without risk inclusion. Cryptocurrency and adult-content
templates default to observation. Counts come from the current cached catalog,
including regexps; opening the page does not download category data. The main
creation menu also offers templates once policies exist. Financial categories
include banks and payment services, so the explanation leads to a custom or
trusted remote list rather than labeling those categories high risk.

Policy creation and unsaved previews accept an optional `new_list` containing a
name and cached geosite category/attributes. PUT rejects it. Opening or canceling
the editor creates no list. The preview returns the actual definition budget and
the category parse report, including excluded broad entries. The storage writer
creates the list and referencing policy under one destination generation lock
and one transaction. It validates existing references before allocating the new
identity and checks the committed definition budget again inside the transaction.
Any validation, uniqueness, allocation or storage failure rolls back both rows
and leaves caller identities unchanged. Category lists retain normal management
and parsed-report provenance after creation.

New handler, SQLite and application integration regressions failed before the
implementation. The full SQL-store, destination-policy service and HTTP-handler
suites and the application template integration passed locally. The frontend
suite passed 1,969 tests (one skipped); focused page/model/source checks passed.
TypeScript, changed-view lint and production build passed. Chinese desktop and
375px editors, English dark mode, manual category download, cancellation,
first-publish confirmation, regexp-over-quota save blocking and the financial
new-list entry were checked with isolated browser fixtures. Fixtures are not
live node acceptance. Category-template head `8c1d9ace98f20987a88a67e0cbc8c4494e8aaefc`
passed [complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37417980966)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37417980924).
Real third-party panels were skipped, and released Node beta4 installation does
not establish new Node #78 policy-kernel acceptance.

The prior conversion head's web CI failed when a combined installation privacy
and command-feedback test exceeded its five-second timeout. The follow-up splits
those two behaviors into focused cases while retaining their assertions, with
no global timeout increase. Its installation test file and the full frontend
suite passed locally; the new head still needs its own CI results.

## Exemptions and account access increment

The policy rail now opens `sheet=exemptions`; the template empty view retains a
management entry. The page drawer loads current exemptions, shows attribution
and expiry, moves expired rows to the end and never displays a negative expiry.
Its add/edit dialog sits above the normal page drawer. The same editor is used
from the account view with the account locked. Reasons are required and limited
to 255 Unicode characters. Temporary presets resolve from actual submission time;
reason-only edits preserve the exact stored expiry, including seconds and
milliseconds hidden by the browser time control. Dirty cancellation and pending
write/navigation guards prevent losing drafts or admitting duplicate writes.

Cancellation uses the ordinary S20 confirmation. Shared mutation hooks refresh
exemptions, user-access views, the policy overview, node status and budget
previews within the current session on success or conflict. Other sessions are
not invalidated. Opening an account from the exemptions drawer clears sheet-owned
parameters in the same history replacement, and opens the existing RiskUserDrawer on
its administrator-only Access tab. Other hosts retain their overview default.
This follows the general history rule in final-plan section 7.0: closing the
account returns to the page without reviving the replaced sheet. Cold account
links close in place and conflicting account/sheet parameters show only one
drawer.
The Access tab reads `/dest/users/:id` only while mounted and shows the stage-1c
group and exemption rows; destination hit/usage reads are not introduced here.

Query, route and account-tab regressions failed before implementation. The
exemption query, dialog, sheet and account/page tests passed, along with
TypeScript and changed-source lint. The full frontend run passed 1,992 tests
(one skipped). A later browser-discovered phone tab clipping problem was fixed
by using swipeable tabs without space-consuming scroll buttons. A later history
regression proved that closing an account revived its sheet; replacement and
cold-link handling fixed it, followed by focused retesting. Chinese desktop/375px editing and cancellation, account
navigation, English dark-mode account add/cancel and visible selected mobile
tabs were checked with isolated API fixtures. These are not live node acceptance
or completion of the full screen matrix. Exact-head CI remains tracked in draft
PR #274.

## Destination simulation and exceptions increment

The page header opens `sheet=test`. List-entry actions prefill exact domains,
hostnames and single-address CIDRs through history state; keyword/regexp rules
and whole CIDR networks do not fabricate test targets. Destination, port and
account inputs remain outside the URL. Switching a list sheet into testing
replaces its history entry; cold links retain in-place closing. Opening the
matched policy removes the test sheet, selects the policy tab, scrolls and
briefly highlights the row, then opens the existing versioned editor. Deleted
policies produce a warning rather than a synthetic editor.

Tests run only from explicit submit or retry. The audited POST sends a validated
host/IP, integer port, network and optional account/node identities. HTTP/HTTPS
URLs contribute only their host, with an explanation; no DNS is performed.
Pending requests cannot be duplicated and abort on unmount. Changing inputs
clears stale results. The trace preserves API result kinds, shadowed matches and
entry provenance, sharing the policy rail's line/dot primitives. Empty uncertain
traces do not fabricate evaluated steps. Verdict tones distinguish blocking,
allowlist denial, observation, trial, allow, exempt, direct and uncertainty in
both themes. Existing group traces can be explained without exposing new
stage-5 configuration controls.

Node explanations follow actual API states and show at most five until
expanded; a selected unapplied node warns that actual behavior may differ.
Unpublished changes show an explicit publish action. Publishing refreshes
metadata without repeating the audited test or claiming node application.
Published-version and IP/protocol/scope limitations remain visible.

The shared allow-exception dialog calls the existing atomic global-exception
API. It explains that all accounts bypass blocking, the server determines the
registrable domain, and first use creates the list and first allow policy.
IP exceptions remain one address. Save feedback follows the response's actual
`created` facts. Conflicts preserve the match choice; dirty/pending guards retain
the draft. The account-only choice reuses the exemption editor, required reason
and expiry validation, warns about account-wide exemption and closes the flow
after saving. Exception mutations invalidate only the session's relevant list,
policy, status and group caches without repeating network previews.

The exemption head `e99f2fab` passed complete Test and released-node systemd
acceptance. Simulation regressions covered API calls, admission, aborted reads,
manual retry/publication, uncertain/group results, navigation and shared
exception/exemption actions. Chinese desktop and 375px English dark-mode
browser fixtures checked host normalization, result traces, offline-node/IP
notes, above-drawer exception saves and policy navigation. Fixtures were removed
after validation; they are not live backend/node or full-matrix acceptance.

Local full frontend validation passed 2,025 tests (one skipped) in a single
worker. The preceding two-worker run exited with a Windows native worker fault
`3221225477` and a host-normalization assertion while that code was being
refined; it is not counted as a passing run. Final verdict-tone and asynchronous
button changes were followed by 251 passing focused tests. TypeScript, changed-source
lint, locale generation and production builds passed. Exact-head CI for this
increment remains a separate draft-PR gate. Simulation head `3ffbffcc` subsequently
passed [complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37528643108)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37528643110).
Real third-party panels were skipped; the systemd job still covers released Node
beta4, not unmerged Node #78 policy-kernel behavior.

## Account launcher and refresh-setting navigation

The header now uses the shared server-backed account autocomplete. Selection
pushes an account drawer on its Access tab; closing returns to the previous
page/tab. The lookup resets across account open/close edges. Sheet-to-account
navigation retains history replacement, cold closing and parameter cleanup.

The list page and list editor open destination settings focused on
`dest_list_refresh_hours`. Opening settings replaces an existing sheet without
retaining its list/filter/target prefill; a cold sheet closes settings in place.
Opening from the page creates the normal owned history entry. The header's
settings action clears the specialized focus.

The account, focus and cold-navigation regressions failed before implementation.
The page, account autocomplete, settings and drawer-history checks passed 63
tests; the final check including locale parity passed 70. TypeScript, changed
source lint, locale generation and production build also passed. Isolated
browser fixtures verified account selection on the Access tab,
return to the list tab, actual refresh-field focus and a 375px header without
horizontal overflow. Those fixtures were removed after validation.

The account-navigation head `4b2ab6d6` passed released-node systemd acceptance,
but its complete Test workflow failed on a five-second installation command-copy
test timeout. The other frontend tests, all backend/dialect/race checks and
compatibility jobs passed. The failing integration test now waits for the
already-selected stable version instead of redundantly opening its autocomplete,
and scopes queries to its dialog. Command, copy-failure, folded-credential and
write-count assertions remain; the shared command component covers copy success,
false/throw failures, expiry and stale feedback. No global timeout was increased.

## List references

Desktop usage cells and phone cards open a shared reference popover, using the
server's `used_by` identities and separate policy/group counts. Policy actions
open the existing editor, with the same missing-policy warning and navigation
cleanup as simulation results. Popovers dismiss before opening the editor.
Consumed requests are cleared, with monotonically increasing request tokens;
closing the editor and switching tabs cannot replay an old request, while an
explicit second reference action can still reopen it.

Owned-list labels remain plain text rather than status badges. Owner lookup
matches the actual `owner_group_id`, and absent references do not invent a link.
Group names are readable; their navigation callback remains optional until the
stage-5 group destination exists. Full S6 group deep-link acceptance is pending.

Reference, missing-policy and consumed-request regressions failed before
implementation. The final page/policy/history/locale check passed 107 tests;
installation-copy/component/page checks passed 75. TypeScript, changed-source
lint, locale generation and production build passed. Isolated browser fixtures
checked Chinese desktop, English dark mode at 375px, policy identity, dismissed
popovers and no automatic editor replay. The phone popover stayed within the
viewport with no horizontal overflow. Fixtures were removed; these checks do
not establish live backend/node or the full screenshot matrix. Full local
frontend validation and exact-head CI remain separate checks. List-reference
head `eade0aa0` subsequently passed 2,037 local frontend tests (one skipped),
[complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37531699616)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37531699687).
Real third-party panels were skipped; the installation job covers released Node
beta4 rather than the unmerged policy kernel.

## Status reads and coverage navigation

The conclusion area now distinguishes its first load, a first read failure and
a failed refresh with cached data. Initial loading uses a skeleton; first
failure offers an explicit status retry while policy definitions remain usable.
A cached refresh failure retains the previous verdict and marks the data as
possibly stale. A refresh returning 503 cannot erase the page as though the
feature had never been available. Successful-read time and publication countdown
stay outside the primary live announcement; countdowns keep an absolute schedule
label and do not announce every tick. Invalid publication reasons do not invent
numeric quotas, and quota names are localized.

Non-execution summaries count actual third-party panels, native upgrades and
native offline nodes separately, omitting zero segments. Third-party exclusion
does not change the primary verdict. Upgrade opens the upgrade filter; offline
and third-party summaries open the existing excluded filter, which also includes
other non-executing nodes. Opening writes sheet/filter parameters atomically and
preserves owned-history closing. List failures open problem lists, while list
quota refusal opens all lists rather than hiding healthy quota consumers.

Node coverage now uses a real page-level drawer at the default drawer layer,
560px on desktop and full viewport width on phones. Node retry uses shared
asynchronous feedback and immediate admission, preventing two requests from a
fast double click. All prior filters, fallback explanations and receipt-based
states remain available.

The four initial page regressions failed before wiring the overview. Further
regressions reproduced the cached-503 page loss and two retry requests from one
double click before their repairs. The overview/page/query/history/model checks
passed 245 tests; after the final wrapping and plural-language changes, 83
page/overview/locale/text-guard checks passed. TypeScript, changed-source lint,
locale generation and production build passed. Chinese desktop and 375px English
dark fixtures verified first-failure retry, cached-503 explanations, actual
upgrade/excluded filters, default drawer depth, phone width and wrapped stale
text. Fixtures were removed. These checks do not replace live backend/node or
full-matrix acceptance. Recording summaries in stage 2c retain their planned
scope.

The status head `ef87d48b58c6ad755ff4044afe11f172dc3a7200` passed
[complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37534440698)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37534440847).
Real third-party panels were skipped; released-node installation does not
establish unmerged Node #78 policy-kernel acceptance.

## Header actions and tab help

The page menu opens data/deployment settings and confirms pause or resume using
the existing guarded write. An unknown status disables that menu's state-changing
action. A paused conclusion retains its direct resume action. Opening settings
keeps active list filters and owned history. On phones, testing is an accessible
icon and account selection occupies its own full row; desktop keeps the text
action. The scrollable tab strip reserves space for help about only the current
tab. No group/record tab or legal-settings link is invented before its target
exists; the legal menu entry remains stage-3 work.

Policy and list help follow the final plan's complete wording, including native
node enforcement, matching order, observation termination, bypasses, IP/BT
limits, deployment interruptions and broad-entry exclusion/report behavior.
Quota and interval values come from the actual definitions/effective settings;
unread values show an em dash rather than a fabricated default. The shared help
component accepts independent text interpolation, preserves paragraphs and
names its popover as a dialog. Phone help has bounded width and scrollable
height. PageHeader's optional action styling leaves existing callers unchanged.

Four new menu/help regressions failed before implementation. The final page,
status, help, history, risk/diagnostic shared-component and locale checks passed
182 tests after the width adjustment. Three menu-state checks passed after
adding unknown-status and resume coverage. TypeScript, changed-source lint,
locale generation and production build passed.
[Browser evidence](access-control-acceptance/README.md) records
Chinese desktop and 375px English dark fixtures, actual non-default interpolation,
pause cancellation and help margins. This partial evidence does not replace
live acceptance, the full screen matrix or owner approval.

The header head `a5923d7d2d2904d174f5a1b8865d189908add8e5` passed
[complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37535638879)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37535638849).
Real third-party panels remained skipped; installation remains a released-node
baseline.

## Additional conditions

The policy editor's additional conditions now use a controlled accordion. Blank
policies start collapsed; any saved inline condition starts expanded. Collapsing
keeps parent-owned values and exposes a live inline-only summary using the same
matching grammar as the footer. Explicit stale reload resets expansion from the
new record. Server port/CIDR field errors reopen the section on every failed
save, including repeated errors after manual collapse; an initial effect keyed
only by field name missed that repeated-error case and was repaired. Busy saves
disable its toggle. The title and dialog have one shared label target containing
only the title, with the close action outside the heading.

Both initial accordion regressions failed against the prior implementation;
the repeated-error regression also failed before repair. Final page/draft/template
and locale/text guards passed 117 tests, with TypeScript, changed-source lint,
locale generation and production build passing. Chinese desktop and
375px English dark fixtures checked initial collapse, retained BT/port values,
summary, repeated field errors, phone width and title semantics. Temporary
fixtures/server/tabs were removed. This does not complete all S3 layout details
or live acceptance; [partial screenshots](access-control-acceptance/README.md)
retain those limits.

The conditions head `6628f9beb55df207647c40f08d3fc62640d6998e` passed
[complete Test](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37536596750)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37536596751).
This released-node baseline does not establish unmerged Node #78 execution.

## Editor controls and quota accessibility

Desktop editing is bounded at 720px. Full-screen phone editing places Close on
the left and exactly one Save action on the right. Both placements share the
same asynchronous save, validation, dirty/stale checks and admission guard;
pending writes show progress and disable editor/close controls. Action and
network choices expose exclusive segmented buttons. Arrow keys wrap through
choices, Home/End select boundaries and focus follows selection. Selecting Any
preserves the explicit empty network value instead of rejecting it as falsy.

Full quota meters use 4px rounded bars, with phone counts beneath each bar.
Each bar exposes its translated quota name and actual used/limit counts; visual
percentages remain capped at 100 without concealing over-limit counts. Subjects
and bytes retain the 80-percent visibility threshold and unknown budgets retain
loading placeholders. Existing quota refusal and severity semantics are preserved.

Three editor regressions and two quota-accessibility checks failed before their
repairs. Final page/draft/template, quota, shared asynchronous button and locale
checks passed 128 tests. TypeScript, changed-source lint and production build
passed. Chinese desktop and 375px English dark browser fixtures checked actual
width, keyboard focus/selection, phone Save, meter order/counts and a double-click
producing one mock write. Temporary fixtures/server/tabs were removed. Evidence
remains partial: template titles, prescribed field hints, later group-mode UI,
the full screen matrix and live backend/node acceptance are still outstanding.

## Storage and privacy

List previews and policy previews are excluded from write-audit logging by
exact POST path. Other destination writes, including `/dest/test`, retain normal
audit behavior; destination audit rows remain restricted to administrators.
PSP's access logger strips query strings from `/api/admin/dest/` paths. Reverse
proxies may still record API query parameters in their own access logs. Record
search terms will remain component state rather than page URL state, but API
queries can still reach those external logs. Audit ingestion, retention and
consent enforcement remain part of the later implementation stages.

Stage 1c still requires the remaining access-control views and complete browser
acceptance. C2's end-to-end browser
acceptance remains outstanding. Audit ingestion,
retention, privacy/consent and subsequent stages retain the full final-plan
scope. Repository tests and green CI do not establish completion of these
requirements.
