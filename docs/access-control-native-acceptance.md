# Native destination traffic acceptance

The `Native destination traffic acceptance` workflow runs a real Node daemon
against PSP's production `Build` router, TLS sync endpoint, administrator HTTP
routes, SQLite stores, and destination audit worker. It never fabricates a Node
report or an applied receipt, and it never rewrites the emitted core artifact.

The Node candidate is pinned to
`KazuhaHub/Passwall-Node@63c60f6d5e8cd093bbaa8385f5b514dbe2f40390`.
This is a reviewed development candidate, not a published stable release. Its
own preferred Go compiler is checked separately from PSP's preferred compiler.
No local module replacement is used. The daemon installs and runs each exact
Xray version: `26.6.27`, `26.7.28`, and `26.9.9`.
The isolated fixture explicitly acknowledges the reviewed restricted `26.9.9`
selection; the production catalog gate remains enforced.

Every matrix job must execute all twenty-one ordered cases: empty policy, watch,
block, pause/resume, anonymous allowlist trial, enforce, supplemental entry,
unrelated listener rejection and repair,
collection off, detailed usage, daemon restart, port-only fallback under
insufficient sniffing, pause/resume during fallback, disabled-rule pruning,
sniffing fallback exhaustion, account revocation while exhausted, and a PSP app
restart with the exhausted state persisted, returning an allowlist group to open
during sniffing fallback, pause/resume of that pruned fallback, pausing an offline
node, and restarting that node with unacknowledged audit backlog.
The restarted daemon's very
first real TLS report must already carry the confirmed applied policy, and a new
traffic hit must map to its original user. A bounded in-memory probe copies only
policy status from that first request; the production handler receives its
unchanged bytes and validates it normally. Allow/deny is checked against an actual
VLESS HTTP request and the destination's nonce and request count. Audit and
usage counts come from the real ingestion worker. The usage HTTP read must
also create its declared administrator audit action.

Fault cases mutate sniffing and policies through production administrator HTTP.
They check canonical candidate bytes, historical LKG retention, the acknowledged
current digest, actual allow/deny traffic, and the status API's current rule count
and fault state. A confirmed port-only fallback continues blocking when a new
domain policy cannot be sniffed; pausing stops it and disabling its source prunes
it. A confirmed domain-dependent LKG exhausts under metadata-only sniffing.
The same HTTP Host is denied with full sniffing and allowed after exhaustion;
route-only sniffing preserves the local IP destination and requires no DNS.
Account revocation must then reach the acknowledged roster without rearming that
fallback. The revoked user must fail while a separate enabled canary still reaches
the same target, proving the core is serving traffic.
The listener rejection case runs while the group enforces its allowlist.
Administrator HTTP stores syntactically valid TLS settings with a numeric ALPN;
the real Xray configuration check rejects that listener. A fresh real
`core_convergence_failed` report must reach the operator inbox, while the policy
receipt and LKG remain unchanged, the invalid config stays unapplied, and both
original client keys remain attached. Both users still reach the target through
the running previous artifact. Restoring the original settings then requires a
fresh accepted report, current config/roster convergence and a synced production
listener intent. Stream convergence alone acknowledges a received document;
the listener's pending intent proves the invalid config was not deployed. This covers an
unrelated completed core check, not a simulated policy rejection or timeout.
PSP is then shut down and rebuilt with the same configuration and database.
A drained fixture TLS gateway holds the endpoint stable while the actual SQL
pool, router, services and caches are replaced. The continuing real daemon must
sync with the reopened app; the exhausted candidate and historical LKG must
survive, and the revoked user must remain denied while the canary succeeds.

After the reopen, a group catch-all and a global port watch are genuinely applied:
canary traffic is denied and a real group hit is ingested. A new BitTorrent rule
then fails the metadata-only sniffing precheck. The confirmed fallback retains
the group source. Returning the group to open must prune that source, retain only
the exact port watch, and acknowledge the original canary's enabled attachment.
The previously revoked user must remain disabled. Actual canary traffic must
reach the target and create a watch hit. Pausing this fallback acknowledges zero
rules and allows canary traffic without adding watch hits after prior batches
have drained. Resuming restores the confirmed watch and ingests a newly counted
hit. These checks cover both policy pruning and asynchronous membership resync.

Offline cases partition only real Node TLS sync requests with HTTP 503; the
production administrator routes remain available. With a confirmed port deny,
the administrator pauses execution while multiple actual polls fail. The status
API must still show the node as offline with its historical active rules, and
real canary traffic remains denied. Reconnection must then acknowledge the pause
before canary traffic succeeds; resuming restores the deny without re-enabling
the previously revoked user.

The next partition observes real frozen block batches over several failed polls.
A bounded fixture probe retains only counts and the first audit subtree's hash;
new blocked connections must not change that batch's ID, hour, rows or counters.
No partitioned hit reaches PSP storage. Node is then stopped and restarted while
sync remains unavailable. Its first real report must already restore the applied
policy, but multiple new failed reports must contain none of the old in-memory
backlog. The private sink token remains identical with mode 0600. New denied
traffic must produce a new batch through the restored sink. Reconnection admits
that new event and another fresh event, with exact counts excluding pre-restart
backlog. This records actual volatile-data loss across multiple cycles; it does
not bound restart loss to one poll interval or establish durable audit delivery.

Current candidate readiness requires its published generation, a mint after
the administrator mutation, a matching applied report, and an accepted sync
after that mint. Desired candidates additionally require the matching confirmed
LKG. Empty and paused candidates may retain a different historical LKG;
unchanged report timestamps are not treated as a new status transition.

The driver only runs as root on a disposable GitHub-hosted Linux VM. It adds
one random run-owned `2001:db8::/128` address to loopback, verifies its exact
local route, and removes that exact address after stopping the daemon. TLS
trust is scoped to a temporary CA file; host trust stores and core private
address protections are unchanged. Credentials and process output stay in
temporary private files or memory and are never uploaded. The retained artifact
contains Go test execution results and count-only assertions. The result parser
rejects missing, duplicate, failed, or skipped cases.

This gate covers one native node with one VLESS TCP listener and two users on
SQLite. The fixture provisions those records directly, obtains a normal admin
JWT, then uses production HTTP routes for every tested policy mutation. It
starts `Build` and the audit worker, not all `App.Run` background services. It
does not replace full service installation/reinstall acceptance, SQL matrix
tests, sing-box acceptance, policy-rejection and timeout scenarios, multi-node
offline and mixed-version cases, committed-batch response-loss deduplication, load testing, the
complete UI approval matrix, or staged release verification.

Use the workflow run and PR validation record for execution evidence. All three
jobs must pass on the exact current PR head; an earlier or partial matrix does
not satisfy this gate.
