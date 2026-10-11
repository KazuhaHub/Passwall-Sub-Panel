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

Every matrix job must execute all sixteen ordered cases: empty policy, watch,
block, pause/resume, anonymous allowlist trial, enforce, supplemental entry,
collection off, detailed usage, daemon restart, port-only fallback under
insufficient sniffing, pause/resume during fallback, disabled-rule pruning,
sniffing fallback exhaustion, account revocation while exhausted, and a PSP app
restart with the exhausted state persisted.
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
PSP is then shut down and rebuilt with the same configuration and database.
A drained fixture TLS gateway holds the endpoint stable while the actual SQL
pool, router, services and caches are replaced. The continuing real daemon must
sync with the reopened app; the exhausted candidate and historical LKG must
survive, and the revoked user must remain denied while the canary succeeds.

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
tests, sing-box acceptance, core-rejection and listener-fault scenarios, load testing, the
complete UI approval matrix, or staged release verification.

Use the workflow run and PR validation record for execution evidence. All three
jobs must pass on the exact current PR head; an earlier or partial matrix does
not satisfy this gate.
