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
