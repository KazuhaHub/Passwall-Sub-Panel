/**
 * Per-resource freshness policy.
 *
 * Every value here is a STARTING CONFIG, not a measured result — see
 * `docs/react-data-freshness-plan.md` §2.2 and the review's §6.2. The
 * freshness window comes from how stale the UI is allowed to be; the polling
 * period is additionally bounded by backend load, so an interval is only
 * switched on once its budget has actually been measured.
 */

const SECOND = 1000
const MINUTE = 60 * SECOND

export interface ResourcePolicy {
  /** How long a cached read is reused before a mount/focus/reconnect revalidates. */
  staleTime: number
  /** How long an inactive query stays in memory for reuse. */
  gcTime: number
  /** Background polling period, or false to poll only on explicit triggers. */
  refetchInterval: number | false
  /** Why these numbers — required, so a later edit knows what it is changing. */
  note: string
}

export const policies = {
  /** Keeps the existing 60s cadence; pauses while the tab is hidden. */
  alerts: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: 60 * SECOND,
    note: 'NotificationBell polled every 60s before the migration; preserved.',
  },
  usersList: {
    staleTime: 15 * SECOND,
    gcTime: 5 * MINUTE,
    // Enabled only after the per-refresh amplification was removed: a list
    // refresh used to drag the Top-N full-table scan along with it. Verified
    // against a real backend — 0 /admin/traffic/top calls after a list refresh.
    // The one variable still unmeasured is concurrent admin count (plan §2.2);
    // this is a one-line revert if that turns out to matter.
    refetchInterval: 60 * SECOND,
    note: 'One read/minute per visible list instance; hidden tabs paused by refetchIntervalInBackground=false.',
  },
  /** Slow-moving dropdown dictionaries; invalidated on write, never polled. */
  dictionaries: {
    staleTime: 5 * MINUTE,
    gcTime: 15 * MINUTE,
    refetchInterval: false,
    note: 'Groups/servers pickers change rarely; a write invalidates them explicitly.',
  },
  /**
   * The user page's Top-N usage leaderboard. Deliberately NOT tied to the list
   * refresh: the backend walks every user to build it, so it must not become a
   * per-poll cost (review §2.7).
   */
  topTraffic: {
    staleTime: 5 * MINUTE,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Full-table scan server-side; kept off the list refresh chain on purpose.',
  },
  /** Per-user usage/limit facts read by the edit dialog. */
  userLimits: {
    staleTime: 15 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Keyed by user id so switching targets cannot cross-contaminate.',
  },
  /**
   * The server list. Polling stays OFF here even though the user list polls:
   * refreshing this list must never imply an active upstream probe, and the
   * probe trigger is the row-id set, not the data object. Enable only after
   * the probe coupling has been re-reviewed.
   */
  serversList: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Read-only. Probing is driven by the page id-set, never by a data refresh.',
  },
  /** One user's recent sign-ins, shown inside the edit dialog. */
  userActivity: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Per-user slice of the sign-in log; the dialog is the only reader.',
  },
  /**
   * One user's per-node / per-server usage breakdown. The whole breakdown
   * arrives in one response and is paged/filtered client-side, so this is a
   * single read per user — never poll it.
   */
  userBreakdown: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Per-user breakdown, keyed by user id; bounded by group coverage.',
  },
  /**
   * The caller's own profile (`/user/me`). Everything on the self-service page
   * hangs off it, so a failed read must be surfaced, never rendered as an
   * empty page.
   */
  myProfile: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Self-service profile; the page has nothing to show without it.',
  },
  /**
   * Concurrent-location verdicts. Not polled: the verdicts themselves only move
   * when the traffic poll runs, so re-reading on a browser timer buys nothing
   * and would keep a background tab querying.
   */
  geoAnomalies: {
    staleTime: 60 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Revalidates on tab focus; the server-side cadence is the traffic poll.',
  },
  /**
   * The sync-task queue. NOT polled: it is a bounded list whose contents only
   * change when an operator acts or the retry loop runs, and an operator
   * watching it has an explicit Refresh. Polling it would also mean polling
   * while a batch is selected.
   */
  syncTasks: {
    staleTime: 15 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Operator-driven; explicit Refresh plus focus revalidation.',
  },
  /**
   * The dashboard summary. NOT polled: it is a landing page, and every figure
   * on it is `summary?.x ?? 0` — so it must be an explicit read that can fail
   * loudly, never something that quietly refreshes behind a zeroed card.
   */
  dashboardSummary: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Landing page; focus revalidation only.',
  },
  /** A panel-wide traffic trend window (dashboard sparkline, traffic page). */
  trafficTrend: {
    staleTime: 60 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Keyed by period/range/timezone; historical data does not move.',
  },
  /**
   * The Logs page's lists. Not polled: an operator reads them on demand and
   * each tab carries its own paging, filters and explicit Clear/Purge actions.
   * Polling would also re-read a list someone is reading.
   */
  logList: {
    staleTime: 15 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'On-demand audit views; focus revalidation only.',
  },
  /** Certificate issuance/renewal activity. Same reasoning as the log lists. */
  certEvents: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'On-demand activity log; focus revalidation only.',
  },
  /** The Certificates page's lists (certificates / DNS creds / ACME accounts). */
  certList: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Admin-managed configuration; writes invalidate, no polling.',
  },
  /**
   * Inbounds on a panel PSP does not manage. A working list an operator reads
   * while deciding what to import, so it revalidates on focus but is never
   * polled — the panel is not ours to hammer.
   */
  nodeUnmanaged: {
    staleTime: 30 * SECOND,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Panel-scoped pick list; focus revalidation plus explicit Refresh.',
  },
  /**
   * Observable sync tasks for one user. The interval is deliberately false:
   * the cadence belongs to the observation window in `query/syncStatus.ts`,
   * which has to stop on a wall-clock budget and on there being nothing left
   * to watch — neither of which a fixed interval can express.
   */
  syncStatus: {
    staleTime: 0,
    gcTime: 5 * MINUTE,
    refetchInterval: false,
    note: 'Paced by the observation window (15s, 5 min budget), not by this policy.',
  },
  /**
   * The global UI settings blob. Slow to change and edited in forms, so it is
   * never polled and its cached copy is only a starting point for a draft.
   */
  uiSettings: {
    staleTime: 5 * MINUTE,
    gcTime: 15 * MINUTE,
    refetchInterval: false,
    note: 'Form-backed record; editors keep drafts locally, writes invalidate.',
  },
} as const satisfies Record<string, ResourcePolicy>

export type PolicyName = keyof typeof policies

/**
 * The subset of a policy that maps onto TanStack Query options. Kept separate
 * so the `note` field stays documentation and never reaches the library.
 */
export function freshness(p: ResourcePolicy) {
  return {
    staleTime: p.staleTime,
    gcTime: p.gcTime,
    refetchInterval: p.refetchInterval,
  }
}
