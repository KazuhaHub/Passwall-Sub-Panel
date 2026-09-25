package metrics

// The metrics PSP actually records, declared in one place rather than
// beside their call sites.
//
// Two reasons. First, they are cross-cutting by construction: the poll
// (service/traffic), the push fan-out (service/user, service/sharedclient)
// and the panel round trip (adapters/xui) each hold one piece of the same
// cost model, and a reader trying to reconstruct that model should not
// have to find three packages. Second, declaring them together is what
// keeps the names consistent — this package has no registry-side naming
// enforcement, so the file is the convention.
//
// Naming follows Prometheus convention (unit suffix, _total on counters)
// so a future scrape endpoint is a rendering change, not a renaming one.
//
// Each block below names the Phase 0 question it exists to answer; see
// docs/data-plane-plan.md §2.

// ---------------------------------------------------------------------
// Panel round trips — answers "RTT": the real per-operation latency
// distribution against live panels, replacing the ~300ms figure that has
// been carried in a code comment since v3.5.0-beta.12 without ever being
// measured.
//
// Recorded in the adapter, so the histogram covers the actual HTTP
// exchange (including the adapter's own retry/relogin) regardless of
// which service called it. Labelled by operation name only: panel
// identity would make the label space grow with the deployment.
//
// Only the 3X-UI adapter records these. S-UI has no equivalent hook yet, so
// these histograms describe 3X-UI traffic exclusively — read them that way
// before drawing conclusions about a mixed fleet.
// ---------------------------------------------------------------------
var (
	PanelRTT = NewHistogramVec(
		"psp_panel_rtt_ms",
		"Latency of one 3X-UI adapter operation, measured around the HTTP exchange. The S-UI adapter is NOT instrumented, so an S-UI-only deployment reads this empty.",
		"ms", "op", LatencyBucketsMS,
	)
	PanelOpTotal = NewCounterVec(
		"psp_panel_op_total",
		"Adapter operations attempted, by operation name.",
		"op",
	)
	PanelOpErrorTotal = NewCounterVec(
		"psp_panel_op_error_total",
		"Adapter operations that returned an error, by operation name.",
		"op",
	)
)

// ---------------------------------------------------------------------
// Shared-client lifecycle push — answers "what is the skip hit rate?"
// (§1.2 predicts ~0 for active users) and "how many clients does a user
// have?" (P in the cost model).
// ---------------------------------------------------------------------
var (
	LifecycleTotal = NewCounter(
		"psp_lifecycle_sync_total",
		"SyncLifecycle calls that reached the compare-then-write decision.",
	)
	LifecycleSkippedTotal = NewCounter(
		"psp_lifecycle_sync_skipped_total",
		"SyncLifecycle calls the no-op skip elided. Skip rate = this / psp_lifecycle_sync_total.",
	)
	LifecycleWriteTotal = NewCounter(
		"psp_lifecycle_sync_write_total",
		"SyncLifecycle calls that issued an UpdateClient.",
	)
	// The skip decision compares seven fields; knowing WHICH one defeated
	// it separates the predicted cause (the quota floor moves every cycle)
	// from any other drift, and is what tells us whether a deadband on
	// totalGB alone would actually recover the skip.
	LifecycleWriteReasonTotal = NewCounterVec(
		"psp_lifecycle_sync_write_reason_total",
		"Why the no-op skip did not fire, by the first field found to differ.",
		"reason",
	)
	LifecycleNotProvisionedTotal = NewCounter(
		"psp_lifecycle_sync_not_provisioned_total",
		"SyncLifecycle calls that returned early because no attachment is provisioned yet.",
	)
	LifecycleErrorTotal = NewCounter(
		"psp_lifecycle_sync_error_total",
		"SyncLifecycle calls that failed.",
	)
	// The gap between this and the quota floor's true value is the
	// staleness a Phase 1 deadband would introduce, so measuring the
	// distribution now is what lets a band be chosen from data rather
	// than picked.
	LifecycleQuotaDeltaBytes = NewHistogram(
		"psp_lifecycle_quota_delta_bytes",
		"Absolute change in the pushed traffic floor versus what the panel already held. The distribution a Phase 1 deadband would be sized against.",
		"bytes", ByteBuckets,
	)
	// The deadband's yield: quota differences it absorbed. This is the
	// acceptance signal for Phase 1a — psp_lifecycle_sync_skipped_total
	// rising says the skip fires more, this says the BAND is why. Note a
	// compare counted here can still end in a write, because another field
	// may differ in the same call; it counts absorbed quota drift, not
	// elided calls.
	LifecycleQuotaBandSkipTotal = NewCounter(
		"psp_lifecycle_quota_band_skip_total",
		"Compares where the panel's stored cap differed from the intended one but fell inside the deadband, so it was left alone.",
	)
	// A cap PSP intends but the panel in front of it cannot enforce. Counted
	// rather than logged per cycle, because the condition is steady-state (it
	// persists until the operator moves the user or the panel gains the
	// feature) and a per-cycle log line would bury everything else.
	CapabilityGapTotal = NewCounterVec(
		"psp_capability_gap_total",
		"Lifecycle pushes carrying a setting the target panel cannot enforce, by capability.",
		"capability",
	)
	// What each 3X-UI node's fail2ban probe concluded about the concurrent-IP
	// cap, counted once per panel per probe tick.
	//
	// Labelled by state, with unknown carried alongside the rest, because the
	// question an operator actually has is a RATIO: "enforced" on its own says
	// nothing while the denominator is hidden. A fleet whose probe has started
	// 404ing or timing out shows up here as unknown climbing, and looks exactly
	// like a healthy fleet if only the enforced line is watched.
	IPLimitEnforcementTotal = NewCounterVec(
		"psp_ip_limit_enforcement_total",
		"Node fail2ban probes, by what they concluded about the concurrent-IP cap.",
		"state",
	)
	// An SSO login where the IdP sent NONE of the attributes the rules read,
	// so neither the role nor the group could be re-evaluated and whatever is
	// stored was kept.
	//
	// Counted because the guard that produces it is a deliberate
	// fail-open: silence is treated as "no opinion" so a directory-side
	// accident (an Entra group overage, a broken claim mapping) cannot demote
	// a whole fleet at once. The cost of that choice is that anyone able to
	// make the claim disappear keeps the role and OU they already have, and
	// the guard is otherwise invisible. A rate above roughly zero means
	// either the IdP stopped sending a claim PSP depends on, or someone is
	// holding a grant the directory has already taken back.
	SSOClaimSilentTotal = NewCounterVec(
		"psp_sso_claim_silent_total",
		"SSO logins where none of the attributes the rules read were present, so the stored value was kept.",
		"kind",
	)
	// P split by the two things that produce it.
	//
	// UserClientCount is P — clients per user — and it conflates two
	// independent multipliers that happen to look the same in the cost model
	// and do NOT look the same for the connection caps:
	//
	//   * how many PANELS the user is on. Each panel enforces limitIp on its
	//     own and cannot see the others, so this multiplier is inherent to a
	//     multi-panel fleet.
	//   * how many CLIENTS the user needs on ONE panel. clientplan splits by
	//     (password class, flow) because a 3X-UI client row holds a single
	//     password and a single flow, and an SS-2022-128 node cannot share a
	//     row with an SS-2022-256 one. Each split client gets its own email,
	//     and 3X-UI budgets IPs per email — so this multiplier is PSP's, and
	//     it silently multiplies a cap the admin typed once.
	//
	// P alone cannot tell them apart: a user on four panels with one client
	// each and a user on one panel split four ways both report 4. This
	// histogram is the second factor by itself, so the two can be separated
	// without guessing which one the fleet is paying.
	UserClientsPerPanel = NewHistogram(
		"psp_user_clients_per_panel",
		"Shared clients a user needs on ONE panel, sampled once per SyncUserLifecycle. Above 1 means clientplan split them, and each split carries its own copy of the connection caps.",
		"clients", CountBuckets,
	)
	// The number the connection cap was always meant to bound: how many
	// distinct source addresses ONE PERSON is using across the whole fleet,
	// right now. Every enforcement point below bounds something else —
	// 3X-UI applies limitIp per client EMAIL, and PSP splits a user into
	// several emails per panel and across panels — so this is the first
	// place the per-user figure exists at all.
	//
	// Read the distribution before arming anything. It is what says whether
	// a cap of N would catch sharers or households: CGNAT, a phone changing
	// networks and a carrier NAT pool all inflate it honestly.
	UserLiveIPs = NewHistogram(
		"psp_user_live_ips",
		"Distinct live source IPs per user across the whole fleet, sampled once per traffic poll. Source addresses, never devices - the data plane has no device concept.",
		"ips", CountBuckets,
	)
	// Verdicts per poll, labelled by outcome so the DENOMINATOR stays
	// visible. Watching only the flagged line cannot distinguish a clean
	// fleet from a detector that has silently stopped detecting: "unknown"
	// is what a stale or switched-off geo database produces, and "disabled"
	// and "exempt" are policy choices that also look like zero flags.
	GeoVerdictTotal = NewCounterVec(
		"psp_geo_verdict_total",
		"Users classified per traffic poll by concurrent-location policy: clean, suspect, flagged, idle, unknown, exempt, disabled.",
		"state",
	)
	// Users whose fleet-wide live-IP count is a FLOOR rather than a total,
	// because a panel holding their clients could not be read. Above zero
	// means psp_user_live_ips understates, and so does anything drawn from it.
	LiveIPUsersIncompleteTotal = NewCounter(
		"psp_live_ip_users_incomplete_total",
		"Users whose fleet-wide live-IP count was a floor this poll because a panel could not be read.",
	)
	// What the detector actually JUDGED, as opposed to what the upstream
	// remembered. psp_user_live_ips is the 30-minute window; this is the
	// sources live at poll time and left after every exclusion. The gap
	// between the two is how much of the old number was memory and relays.
	UserConcurrentIPs = NewHistogram(
		"psp_user_concurrent_ips",
		"Concurrent, non-excluded sources per judged user per traffic poll (IPv6 folded by /64). The number the location verdict is drawn from.",
		"ips", CountBuckets,
	)
	// Window addresses that were not live at poll time. A large, steady
	// value is the commuter effect the freshness rule exists to remove. A
	// busy 3X-UI fleet reading zero here for hours is worth a look: it is
	// what a fleet whose timestamps stopped arriving looks like, with every
	// remembered address judged as live again.
	LiveIPStaleTotal = NewCounter(
		"psp_live_ip_stale_total",
		"Live-IP window addresses judged stale (not seen within the freshness window of their node's newest scan), summed over judged users.",
	)
	// Sources set aside before judging, by rule: shared, listed, infra,
	// internal. Labelled so an operator can tell a relay that is not
	// registered (shared climbing) from a working exclusion (infra), and
	// notice an ignore list that has grown to swallow real users (listed).
	LiveIPExcludedTotal = NewCounterVec(
		"psp_live_ip_excluded_total",
		"Live sources excluded from location judging, by reason: shared, listed, infra, internal.",
		"reason",
	)
	// Over-samples by the coarsest tier that was over. The dial for the
	// default's sensitivity: a fleet whose region line dwarfs the country
	// one is mostly home-router-plus-phone, and a region tolerance of 2 is
	// the one-number fix.
	GeoOverTierTotal = NewCounterVec(
		"psp_geo_over_tier_total",
		"Judged samples over the flag tolerances, by the coarsest tier over: country, region, city.",
		"tier",
	)
	// Users NOT re-judged because their last judgement was less than half a
	// poll interval ago. Only a manual poll can produce one; a steady count
	// means someone is pressing "poll now" repeatedly, which is exactly the
	// acceleration this guard refuses to count.
	GeoSamplesSpacedTotal = NewCounter(
		"psp_geo_samples_spaced_total",
		"Users skipped by the location detector because they were judged less than half a traffic interval ago.",
	)
	// The optional automatic suspension (geo_auto), by outcome. Two lines
	// matter most. lifted_admin against suspended is the false-positive
	// rate: every geo_auto an admin resumes by hand is one the detector got
	// wrong or one a person overruled. lift_error / suspend_error are the
	// failures that otherwise show only as Warn logs. deferred and
	// lift_deferred climbing mean the per-poll caps are being hit — a mass
	// event, or a broken location database with suspension on.
	GeoAutoSuspensionTotal = NewCounterVec(
		"psp_geo_auto_suspension_total",
		"Automatic location suspensions by outcome: suspended, skipped_held, skipped_unwired, deferred, suspend_error, lifted_expiry, lifted_admin, lift_skipped, lift_deferred, lift_error.",
		"outcome",
	)
	// PSP's own node and relay addresses, excluded as infrastructure. A
	// drop to zero with nodes configured means the refresh is failing or
	// every hostname stopped resolving.
	InfraAddresses = NewGauge(
		"psp_infra_addresses",
		"Distinct node and relay addresses currently excluded from location judging as PSP infrastructure.",
	)
	InfraAddressResolveFailuresTotal = NewCounter(
		"psp_infra_address_resolve_failures_total",
		"Node or relay hostnames that failed to resolve during an infrastructure-address refresh. The previous addresses are kept.",
	)
	// P in the cost model.
	UserClientCount = NewHistogram(
		"psp_user_client_count",
		"Shared clients per user, sampled once per SyncUserLifecycle.",
		"clients", CountBuckets,
	)
	SyncUserDuration = NewHistogram(
		"psp_sync_user_lifecycle_ms",
		"Wall time for one user's full lifecycle fan-out across all their clients. P x 2 serial round trips today.",
		"ms", LatencyBucketsMS,
	)
)

// ---------------------------------------------------------------------
// Config push — the traffic poll's fire-and-forget entry point.
// ---------------------------------------------------------------------
var (
	PushConfigTotal = NewCounter(
		"psp_push_client_config_total",
		"PushClientConfig calls started.",
	)
	PushConfigErrorTotal = NewCounter(
		"psp_push_client_config_error_total",
		"PushClientConfig calls that returned an error.",
	)
	PushConfigDuration = NewHistogram(
		"psp_push_client_config_ms",
		"Wall time for one PushClientConfig, from dequeue to completion.",
		"ms", LatencyBucketsMS,
	)
)

// ---------------------------------------------------------------------
// Push semaphore — answers "is pushSem already backing up across
// cycles?" (§1.4: the failure mode is an avalanche, not a slowdown, so
// the peak and the wait distribution matter more than any average).
// ---------------------------------------------------------------------
var (
	PushSemCapacity = NewGauge(
		"psp_push_sem_capacity",
		"Configured push-semaphore capacity. Set once at construction.",
	)
	PushSemInflight = NewGauge(
		"psp_push_sem_inflight",
		"Pushes holding a semaphore slot right now. Peak equal to capacity means saturation.",
	)
	PushSemWaiting = NewGauge(
		"psp_push_sem_waiting",
		"Pushes queued for a slot right now. A peak that grows cycle over cycle is the avalanche.",
	)
	PushSemWait = NewHistogram(
		"psp_push_sem_wait_ms",
		"Time a push spent waiting for a semaphore slot.",
		"ms", LatencyBucketsMS,
	)
	// A push still queued when the next cycle starts is the concrete
	// signal the cross-cycle guard trips on.
	PushSemCarryoverTotal = NewCounter(
		"psp_push_sem_carryover_total",
		"Poll cycles that began while pushes from an earlier cycle were still queued. Each one is a cycle whose floor pushes the guard suppressed.",
	)
	// The magnitude behind the carryover count: how many pushes were
	// actually dropped, not just how many cycles tripped. A carryover
	// count that stays flat while this climbs means the deployment is
	// permanently past the semaphore's capacity.
	PushSuppressedTotal = NewCounter(
		"psp_push_suppressed_total",
		"Floor pushes the cross-cycle guard skipped rather than enqueued.",
	)
)

// ---------------------------------------------------------------------
// Traffic poll — answers "N" (active users per cycle) and the per-stage
// wall-clock breakdown that until now only existed as debug log lines.
// ---------------------------------------------------------------------
var (
	// The cadence the traffic loop is ACTUALLY running on, published by the
	// loop itself rather than derived from the settings row. Those two
	// diverge: an admin's change lands in settings immediately, and the loop
	// picks it up on its next tick. A reader that assumes the settings value
	// is live will call a healthy poll dead during that gap - it computes
	// "several intervals have passed" against the new short interval while
	// the loop is still sleeping out the old long one. Reporting what the
	// ticker holds removes the guess.
	PollIntervalMS = NewGauge(
		"psp_poll_interval_ms",
		"Interval the traffic loop's ticker is currently set to. Published by the loop, so it is the effective cadence rather than the configured one.",
	)
	PollTotal = NewCounter(
		"psp_poll_total",
		"Traffic poll cycles started.",
	)
	PollErrorTotal = NewCounter(
		"psp_poll_error_total",
		"Traffic poll cycles that returned an error.",
	)
	PollDuration = NewHistogram(
		"psp_poll_ms",
		"Wall time for one full PollOnce.",
		"ms", LatencyBucketsMS,
	)
	PollStageDuration = NewHistogramVec(
		"psp_poll_stage_ms",
		"Wall time for one stage of PollOnce.",
		"ms", "stage", LatencyBucketsMS,
	)
	PollUsers = NewHistogram(
		"psp_poll_users",
		"Users scanned per poll cycle.",
		"users", CountBuckets,
	)
	// N in the cost model: users whose delta was non-zero, i.e. the ones
	// that trigger a floor push.
	PollActiveUsers = NewHistogram(
		"psp_poll_active_users",
		"Users that moved bytes in a poll cycle. N in the cost model.",
		"users", CountBuckets,
	)
	// The true number of floor pushes handed to the semaphore, which is
	// slightly below psp_poll_active_users: a user can move bytes and
	// still not reach the enqueue (skipped on an inbound-fetch failure,
	// or already suspended for quota). The gap between the two is itself
	// worth seeing — a large one means the model's N overstates the load.
	PollFloorPushEnqueuedTotal = NewCounter(
		"psp_poll_floor_push_enqueued_total",
		"Floor pushes handed to the push semaphore.",
	)
	PollPanels = NewHistogram(
		"psp_poll_panels",
		"Panels fetched per poll cycle.",
		"panels", CountBuckets,
	)
)

// ByteBuckets spans a single HTTP request's worth of traffic (a few KB)
// to a heavy user's whole monthly allowance (~1 TB). Sized in the same
// 1-2-5 series as the others; the decades that matter for a deadband are
// MB through tens of GB, which get three points each.
var ByteBuckets = []float64{
	1 << 10, 10 << 10, 100 << 10, // 1 KiB .. 100 KiB
	1 << 20, 5 << 20, 10 << 20, 50 << 20, 100 << 20, 500 << 20, // 1 MiB .. 500 MiB
	1 << 30, 5 << 30, 10 << 30, 50 << 30, 100 << 30, 500 << 30, // 1 GiB .. 500 GiB
	1 << 40, // 1 TiB
}

// ---- Node host telemetry --------------------------------------------
//
// THE LABEL VALUES ARE FIXED SETS, and that is a constraint rather than a
// convention. An agent id or a server name on any of these would be a
// high-cardinality label, which is how a metrics endpoint quietly becomes a
// memory leak. The outcome strings below are the whole vocabulary.

const (
	NodeHostOutcomeAccepted         = "accepted"
	NodeHostOutcomeNoHost           = "no_host"
	NodeHostOutcomeInvalid          = "invalid"
	NodeHostOutcomeIdentityConflict = "identity_conflict"
	NodeHostOutcomeStorageError     = "storage_error"

	NodeHostHistoryInserted     = "inserted"
	NodeHostHistoryThrottled    = "throttled"
	NodeHostHistoryDuplicate    = "duplicate"
	NodeHostHistoryStorageError = "storage_error"

	NodeHostRollupWritten = "written"
	NodeHostRollupEmpty   = "empty"
	NodeHostRollupError   = "error"

	NodeHostPruneRawTable       = "raw"
	NodeHostPruneInterfaceTable = "interface"
	NodeHostPruneHourlyTable    = "hourly"
)

var (
	NodeHostReportTotal = NewCounterVec(
		"psp_node_host_report_total",
		"Host telemetry reports handled, by outcome.",
		"outcome",
	)
	NodeHostHistoryTotal = NewCounterVec(
		"psp_node_host_history_total",
		"Host telemetry history writes, by outcome.",
		"outcome",
	)
	// The persist budget is 500 ms, so the buckets cluster below it and one sits
	// above: a report that misses the budget has to appear as a miss, not as the
	// last bucket of a range that stops at the budget.
	NodeHostPersistMS = NewHistogram(
		"psp_node_host_persist_ms",
		"Time spent persisting one host telemetry report, in milliseconds.",
		"ms", []float64{5, 10, 25, 50, 100, 250, 500, 1000},
	)
	// The wire bound is 128 KiB, so the buckets climb to it: the interesting
	// question is how close real samples come to a limit that exists to bound
	// storage rather than to be reached.
	NodeHostSnapshotBytes = NewHistogram(
		"psp_node_host_snapshot_bytes",
		"Encoded size of a stored host telemetry snapshot.",
		"bytes", []float64{1 << 10, 4 << 10, 16 << 10, 32 << 10, 64 << 10, 128 << 10},
	)
	NodeHostRollupTotal = NewCounterVec(
		"psp_node_host_rollup_total",
		"Node host hourly rollup passes, by outcome.",
		"outcome",
	)
	// A counter rather than a gauge, because the question is how much has been
	// removed over time — a gauge of what is currently being deleted would read
	// zero on every pass that had nothing to do.
	NodeHostPrunedRowsTotal = NewCounterVec(
		"psp_node_host_pruned_rows_total",
		"Node host telemetry rows removed by retention, by table.",
		"table",
	)
	// The FIRST result counter on /v1/node/sync, and it counts the half that was
	// invisible. A refused report produced no row, no audit entry and no log line
	// carrying an agent id, so a node reporting a generation this panel does not
	// admit was indistinguishable from one that was switched off. The log line is
	// throttled per agent; this is not, because it is the durable record.
	NodeSyncRefusedTotal = NewCounterVec(
		"psp_node_sync_refused_total",
		"Authenticated node reports refused at the wire boundary, by reason.",
		"reason",
	)
)

// ---------------------------------------------------------------------
// SAML ACS refusals — answers "is this refusal an attack, an outage, or a bad
// configuration?" Those three need different responses and, before this counter,
// all arrived as a single reason code.
//
// Labelled by the CLOSED set of classified reasons, never by the raw error, so
// the label space cannot grow with attacker input. The distinction that matters
// operationally is replay vs. replay-store failure: an operator who sees
// "replay" for a database outage will eventually stop trusting the word
// (ADR 0036 §6.5.1).
//
// Recorded in-process, so a database outage cannot suppress it. Note the
// READING path is an admin route that itself needs the database, so process
// logs remain the channel that survives a full outage — the counter is for
// rate and shape, not for outage detection.
// ---------------------------------------------------------------------
var (
	SAMLACSFailureTotal = NewCounterVec(
		"psp_saml_acs_failure_total",
		"SAML ACS requests refused, by classified reason. Replay and replay-store failure are separate labels on purpose.",
		"reason",
	)
)
