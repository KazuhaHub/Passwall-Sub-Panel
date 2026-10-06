// The static half of the diagnostics page: which card each metric family
// belongs to, how the poll's stages and the lifecycle's write reasons are
// grouped for an operator, and how a series name is taken apart.
//
// EVERY TABLE HERE IS PINNED TO THE GO SOURCE by diagnosticsCatalog.test.ts,
// which reads internal/pkg/metrics/psp.go, the traffic poll's mark() calls and
// sharedclient's write reasons directly. A family, stage or reason added on the
// Go side fails that test until it is placed here, instead of reaching the page
// unexplained or dropping out of the breakdown it belongs to.

/** The seven area cards, in the order the page renders them. */
export type CardId = 'poll' | 'lifecycle' | 'floor' | 'panel_api' | 'liveip' | 'node' | 'sso'

export const CARD_ORDER: readonly CardId[] = [
  'poll', 'lifecycle', 'floor', 'panel_api', 'liveip', 'node', 'sso',
]

/** The pages a finding or a card sends the operator to. */
export type FindingLink = 'sync_tasks' | 'settings' | 'servers' | 'risk'

/** Where each link goes, and the navigation name its text reuses. */
export const LINK_TARGET: Record<FindingLink, { path: string; nav: string }> = {
  sync_tasks: { path: '/admin/sync-tasks', nav: 'nav:admin.sync_tasks' },
  settings: { path: '/admin/settings', nav: 'nav:admin.settings' },
  servers: { path: '/admin/servers', nav: 'nav:admin.servers' },
  risk: { path: '/admin/risk', nav: 'nav:admin.risk_center' },
}

/** The page each card's footer links to; single sign-on has none of its own. */
export const CARD_LINK: Record<CardId, FindingLink | undefined> = {
  poll: 'settings',
  lifecycle: 'sync_tasks',
  floor: 'servers',
  panel_api: 'servers',
  liveip: 'risk',
  node: 'servers',
  sso: undefined,
}

export type MetricType = 'counter' | 'gauge' | 'histogram'

export interface FamilyInfo {
  card: CardId
  type: MetricType
  /** Declared through New*Vec: its series are `family{label=value}` children
   *  that appear only once something has happened, so absence means zero. An
   *  unlabelled family is registered at start and always present, so its
   *  absence means an older server. */
  labelled: boolean
}

const c = (card: CardId, labelled = false): FamilyInfo => ({ card, type: 'counter', labelled })
const g = (card: CardId): FamilyInfo => ({ card, type: 'gauge', labelled: false })
const h = (card: CardId, labelled = false): FamilyInfo => ({ card, type: 'histogram', labelled })

export const FAMILY_CATALOG: Record<string, FamilyInfo> = {
  // --- traffic polling ---
  psp_poll_total: c('poll'),
  psp_poll_error_total: c('poll'),
  psp_poll_ms: h('poll'),
  psp_poll_stage_ms: h('poll', true),
  psp_poll_interval_ms: g('poll'),
  psp_poll_users: h('poll'),
  psp_poll_active_users: h('poll'),
  psp_poll_panels: h('poll'),

  // --- user status sync ---
  psp_lifecycle_sync_total: c('lifecycle'),
  psp_lifecycle_sync_skipped_total: c('lifecycle'),
  psp_lifecycle_sync_write_total: c('lifecycle'),
  psp_lifecycle_sync_write_reason_total: c('lifecycle', true),
  psp_lifecycle_sync_not_provisioned_total: c('lifecycle'),
  psp_lifecycle_sync_error_total: c('lifecycle'),
  psp_lifecycle_sync_error_stage_total: c('lifecycle', true),
  psp_lifecycle_sync_error_panel_kind_total: c('lifecycle', true),
  psp_lifecycle_quota_delta_bytes: h('lifecycle'),
  psp_lifecycle_quota_band_skip_total: c('lifecycle'),
  psp_capability_gap_total: c('lifecycle', true),
  psp_ip_limit_enforcement_total: c('lifecycle', true),
  psp_user_client_count: h('lifecycle'),
  psp_user_clients_per_panel: h('lifecycle'),
  psp_sync_user_lifecycle_ms: h('lifecycle'),

  // --- quota safety refresh ---
  psp_push_client_config_total: c('floor'),
  psp_push_client_config_error_total: c('floor'),
  psp_push_client_config_ms: h('floor'),
  psp_push_sem_capacity: g('floor'),
  psp_push_sem_inflight: g('floor'),
  psp_push_sem_waiting: g('floor'),
  psp_push_sem_wait_ms: h('floor'),
  psp_push_sem_carryover_total: c('floor'),
  psp_push_suppressed_total: c('floor'),
  psp_poll_floor_push_enqueued_total: c('floor'),

  // --- panel requests (3X-UI only: the S-UI adapter is not instrumented) ---
  psp_panel_rtt_ms: h('panel_api', true),
  psp_panel_op_total: c('panel_api', true),
  psp_panel_op_error_total: c('panel_api', true),

  // --- live IPs and risk data ---
  psp_user_live_ips: h('liveip'),
  psp_live_ip_users_incomplete_total: c('liveip'),
  psp_user_concurrent_ips: h('liveip'),
  psp_live_ip_stale_total: c('liveip'),
  psp_live_ip_excluded_total: c('liveip', true),
  psp_geo_verdict_total: c('liveip', true),
  psp_geo_over_tier_total: c('liveip', true),
  psp_geo_spread_km: h('liveip', true),
  psp_geo_samples_spaced_total: c('liveip'),
  psp_geo_auto_suspension_total: c('liveip', true),
  psp_infra_addresses: g('liveip'),
  psp_infra_address_resolve_failures_total: c('liveip'),
  psp_live_connections: g('liveip'),
  psp_live_conn_refresh_total: c('liveip', true),
  psp_connection_history_write_errors_total: c('liveip'),
  psp_flag_record_write_errors_total: c('liveip'),
  psp_risk_refresh_total: c('liveip', true),

  // --- native nodes ---
  psp_node_host_report_total: c('node', true),
  psp_node_host_history_total: c('node', true),
  psp_node_host_persist_ms: h('node'),
  psp_node_host_snapshot_bytes: h('node'),
  psp_node_host_rollup_total: c('node', true),
  psp_node_host_pruned_rows_total: c('node', true),
  psp_node_sync_refused_total: c('node', true),
  psp_node_policy_status_dropped_total: c('node'),
  psp_dest_pruned_rows_total: c('node', true),
  psp_dest_policy_publish_total: c('node', true),
  psp_dest_policy_publish_rejected_total: c('node'),
  psp_dest_policy_compile_total: c('node', true),
  psp_dest_policy_compile_ms: h('node'),
  psp_dest_list_refresh_total: c('node', true),

  // --- single sign-on ---
  psp_saml_acs_failure_total: c('sso', true),
  psp_sso_claim_silent_total: c('sso', true),
}

/**
 * The label group (admin:diagnostics.labels.<group>) that names each labelled
 * family's children, for the families whose children an operator reads by
 * name. A family left out shows its children as their raw `label=value`,
 * which is what an unnamed value would show anyway (labelFor's fallback).
 */
export const FAMILY_LABEL_GROUP: Record<string, string> = {
  psp_poll_stage_ms: 'stage',
  psp_lifecycle_sync_write_reason_total: 'write_reason',
  psp_lifecycle_sync_error_stage_total: 'lifecycle_stage',
  psp_lifecycle_sync_error_panel_kind_total: 'panel_kind',
  psp_capability_gap_total: 'capability',
  psp_panel_rtt_ms: 'op',
  psp_panel_op_total: 'op',
  psp_panel_op_error_total: 'op',
  psp_geo_auto_suspension_total: 'geo_auto',
  psp_risk_refresh_total: 'risk_refresh',
  psp_live_conn_refresh_total: 'live_conn_refresh',
  psp_node_host_report_total: 'node_host_report',
  psp_node_sync_refused_total: 'node_refused',
  psp_dest_pruned_rows_total: 'dest_table',
  psp_dest_policy_publish_total: 'dest_publish',
  psp_dest_policy_compile_total: 'dest_compile',
  psp_dest_list_refresh_total: 'dest_list_refresh',
  psp_saml_acs_failure_total: 'saml',
  psp_sso_claim_silent_total: 'sso_kind',
}

/** Families on one card, in catalogue order. */
export function familiesOfCard(card: CardId): string[] {
  return Object.keys(FAMILY_CATALOG).filter(f => FAMILY_CATALOG[f].card === card)
}

/**
 * The poll's twelve stages folded into the five things an operator can act
 * on. panel_fetch is its own group because both the traffic read and the
 * live-IP read happen inside it (traffic.go), so it is the network half of a
 * poll; live_ips, despite the name, is the in-memory aggregation and verdict
 * that runs after the reads, so it is compute.
 */
export type StageGroup = 'db' | 'panels' | 'compute' | 'write' | 'geo'

export const STAGE_GROUP_ORDER: readonly StageGroup[] = ['db', 'panels', 'compute', 'write', 'geo']

export const STAGE_GROUP: Record<string, StageGroup> = {
  list_users: 'db',
  latest_prefetch: 'db',
  ownership_prefetch: 'db',
  panel_fetch: 'panels',
  live_ips: 'compute',
  inbound_processing: 'compute',
  node_snapshots: 'compute',
  shared_metering: 'compute',
  user_loop: 'compute',
  baseline_reseed: 'compute',
  sink_flush: 'write',
  geo_enforce: 'geo',
}

/**
 * Why a lifecycle write happened, folded into what caused it. The raw reason
 * is the first field that differed (sharedclient.lifecycleWriteReason), which
 * is a developer's question; the operator's is "was this the quota refresh
 * doing its job, or something else changing".
 */
export type WriteReasonGroup = 'quota' | 'limits' | 'state' | 'credentials' | 'unread'

export const WRITE_REASON_GROUP_ORDER: readonly WriteReasonGroup[] = [
  'quota', 'state', 'limits', 'credentials', 'unread',
]

export const WRITE_REASON_GROUP: Record<string, WriteReasonGroup> = {
  total_gb: 'quota',
  ip_limit: 'limits',
  device_limit: 'limits',
  enable: 'state',
  expiry: 'state',
  id: 'credentials',
  password: 'credentials',
  flow: 'credentials',
  auth: 'credentials',
  panel_unread: 'unread',
}

/**
 * The two breakdowns of psp_lifecycle_sync_error_total, in the order a write
 * happens. Every counted failure lands in exactly one child of each family
 * (sharedclient.countLifecycleFailure), so either list sums to the total.
 * "unknown" is a panel the pool does not hold, which is exactly a pool_get
 * failure.
 */
export const LIFECYCLE_ERROR_STAGES = [
  'pool_get', 'update', 'confirm_read', 'confirm_mismatch', 'record_credentials',
] as const

export const LIFECYCLE_ERROR_KINDS = ['3xui', 'sui', 'psp', 'unknown'] as const

/**
 * Split a series name into its family and, for a labelled child, the one
 * label it carries. The Go registry renders a child as `family{label=value}`
 * (metrics/vec.go) and supports exactly one label, so this is the whole
 * grammar. The value is everything after the first "=": nothing on the Go side
 * forbids one inside it.
 */
export function familyOf(series: string): { family: string; label?: string; value?: string } {
  const open = series.indexOf('{')
  if (open < 0 || !series.endsWith('}')) return { family: series }
  const inner = series.slice(open + 1, -1)
  const eq = inner.indexOf('=')
  if (eq < 0) return { family: series }
  return { family: series.slice(0, open), label: inner.slice(0, eq), value: inner.slice(eq + 1) }
}

/** Fold a label value into a key i18n tooling can walk (client.iplimit → client_iplimit). */
export function labelKey(v: string): string {
  return v.replace(/[^A-Za-z0-9_]/g, '_')
}

export type Translate = (key: string, options?: Record<string, unknown>) => string

/**
 * The translated name of a label value, or the value itself when there is no
 * translation. The fallback is deliberate: a new operation or reason added in
 * Go must be readable on the day it ships, under its own name, rather than as
 * an i18n key path.
 */
export function labelFor(t: Translate, exists: (key: string) => boolean, group: string, value: string): string {
  const key = `admin:diagnostics.labels.${group}.${labelKey(value)}`
  return exists(key) ? t(key) : value
}
