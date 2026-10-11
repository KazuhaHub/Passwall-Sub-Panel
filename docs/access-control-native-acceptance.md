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

A separate job selects the reviewed sing-box `1.14.0` through the same PSP
catalog and launches the candidate Node with its normal sing-box API. Its
eleven required cases are empty policy, watch, block, pause/resume, sniffed
domain blocking, allowlist trial, enforce, supplemental CIDR, daemon restart,
collection settings, and the pre-hotfix private-address baseline. Administrator
mutations require a current applied policy receipt and actual listener/roster
convergence before traffic is checked. Trial allows the connection; enforce
denies it; adding the target CIDR allows it. The first report after restart must
restore the confirmed policy. Collection is clamped to the empty wire level
even when the panel requests B; the status API must disclose no hit/usage
support or active collection. Traffic over multiple sync cycles must leave
both audit tables empty.

Changing an unsupported collection setting leaves sing-box's effective policy
unchanged. That case compares the exact candidate/body/context and historical
LKG, requires its applied receipt and an accepted sync after the saved setting,
and verifies the saved panel level through status. It does not demand a new mint
for identical effective state. Policy-changing mutations still require a mint
after the administrator mutation.

The N0-S baseline uses a disposable loopback HTTP target. It sends distinct
VLESS address types for the private IPv4 literal and the `localhost` domain,
checks the target nonce, and expects the pinned pre-hotfix Node to reach both.
This is prerequisite evidence for a separate conditional Node hotfix, not a
claim that a private-address guard already exists or that cloud metadata and
DNS-resolved private destinations are protected. No cloud endpoint is queried.
The private-address guard is unchanged by this PSP acceptance change.

Every Xray job must execute all twenty-three ordered cases: empty policy, watch,
block, pause/resume, anonymous allowlist trial, enforce, supplemental entry,
unrelated listener rejection and repair, actual core-check timeout and repair,
collection off, detailed usage, daemon restart, port-only fallback under
insufficient sniffing, pause/resume during fallback, disabled-rule pruning,
sniffing fallback exhaustion, account revocation while exhausted, and a PSP app
restart with the exhausted state persisted, returning an allowlist group to open
during sniffing fallback, pause/resume of that pruned fallback, pausing an offline
node, restarting that node with unacknowledged audit backlog, and committed-batch
replay after response loss.
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
the listener's pending intent proves the invalid config was not deployed.

The timeout case binds to the installed executable of the running Node's core.
It pauses only that Node's exact `run -test -config .candidate-*.json` children,
using pidfds and checking parent, executable inode, argument shape and owned
state directory. It never pauses the live proxy process or rewrites any binary,
artifact, report or receipt. The normal production timeout must terminate the
first stopped check and yield a fresh `core_convergence_failed` deadline report.
Checks remain paused until repair so a quick successful retry cannot hide the
failure. While the check is stopped and after timeout, both existing users must
still reach the actual target through the previous core. Policy body, digest,
LKG, candidate kind and member keys remain unchanged; listener intent stays
pending and the status page does not classify this as a policy rejection.
Restoring the original listener requires a fresh applied/converged report and
synced listener intent. Cleanup resumes only still-existing held children and
closes all pidfds. These cases cover a completed unrelated rejection and a real
deadline, not a policy-specific rejection.
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
Before that administrative mutation, the driver captures the prior fallback.
The prune must advance the published generation, mint after that prior mint,
change the canonical body and digest, confirm that exact pruned body, and receive
a real sync after the mutation. Mint timestamps use a sync cycle's clock captured
before policy compilation, so a concurrent write can be visible to compilation
after that clock. This transition proof avoids assuming that the mint's clock
must follow the administrator request's wall time. The exact rule/source, current
generation, LKG, listener, roster and actual traffic assertions remain required.
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

The response-loss case lets the same production sync handler consume every
original report and enqueue its audit batch, then closes the real TLS connection
without delivering the handler's response. PSP must commit the first block hit.
At least three reports replay its unchanged audit-subtree hash, row count and
event count while three new denied connections accumulate for the next batch.
Every processed sync must actually have produced a successful handler response;
the fixture synthesizes no acknowledgement. Storage remains at exactly one new
event throughout the lost responses. After normal responses resume, the three
new events and one fresh connection must be ingested with exact counts and no
duplicate committed hit. This covers short live retries, not the 26-hour
cleanup/reopen and 48-hour/72-hour boundary matrix.

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
rejects missing, duplicate, failed, or skipped cases. Xray and sing-box have
separate required sets; evidence from one engine cannot satisfy the other.

This gate covers one native node with one VLESS TCP listener and two users on
SQLite. The fixture provisions those records directly, obtains a normal admin
JWT, then uses production HTTP routes for every tested policy mutation. It
starts `Build` and the audit worker, not all `App.Run` background services. It
does not replace full service installation/reinstall acceptance, SQL matrix
tests, the complete sing-box matching/upgrade matrix, policy-rejection and timeout
scenarios, multi-node
offline and mixed-version cases, long-window response-loss deduplication, load testing, the
complete UI approval matrix, or staged release verification.

Use the workflow run and PR validation record for execution evidence. All three
Xray jobs and the separate sing-box job must pass on the exact current PR head;
an earlier or partial matrix does
not satisfy this gate.
