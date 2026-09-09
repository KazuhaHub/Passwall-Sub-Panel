/**
 * The config_sync_state set, as data rather than a literal buried in a render
 * function — so a guard can check it without scraping source text, and so the
 * list has one place to be wrong in.
 *
 * Mirrors the domain.ConfigSync* constants in internal/domain/types.go. Two
 * lists, one guard each: neither language can read the other, so the Go side
 * has TestConfigSyncStatesAreExhaustive and this side has
 * configSyncStates.test.ts.
 */
export const CONFIG_SYNC_STATES = ['', 'synced', 'pending', 'failed', 'drift'] as const

export type ConfigSyncState = (typeof CONFIG_SYNC_STATES)[number]

/** i18n key under `admin:nodes.config_sync` for each state. */
export const CONFIG_SYNC_KEY: Record<ConfigSyncState, string> = {
  '': 'uncaptured',
  synced: 'synced',
  pending: 'pending',
  failed: 'failed',
  drift: 'drift',
}

/**
 * Dot colour per state.
 *
 * `pending` and `failed` share red because both are a failed push, but they
 * carry different wording: pending has a retry queued, failed has had its
 * retry cancelled and will not move without the operator. Collapsing them was
 * the bug — a node that had given up went on showing "pending retry" forever.
 *
 * `drift` is unreachable today (reconcile repairs a drift inside the same call
 * so no node rests in it) and is kept so the dot already works the day a
 * writer appears.
 */
export function configSyncColor(
  state: string,
  md: { error: string; outlineVariant: string },
): string {
  switch (state) {
    case 'synced':
      return '#22c55e'
    case 'drift':
      return '#f97316'
    case 'pending':
    case 'failed':
      return md.error
    default:
      // Anything unrecognised is drawn as "never captured" — which is why an
      // unlisted state is worse than a missing one: it renders a different,
      // wrong claim about the node rather than an obvious gap.
      return md.outlineVariant
  }
}
