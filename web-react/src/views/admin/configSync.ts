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
 * A coarse "how long ago", for the config-sync tooltip.
 *
 * Coarse on purpose: the reader is deciding whether to wait or to investigate,
 * and that decision turns on minutes-versus-days. Second-level precision would
 * add digits without adding information, and would make the tooltip reflow as
 * it ticked.
 *
 * An unparseable or future timestamp returns '' rather than a guess — the
 * caller renders nothing at all in that case, which is the honest answer.
 */
export function humanizeSince(iso: string, now: Date = new Date()): string {
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return ''
  const ms = now.getTime() - then
  if (ms < 0) return ''
  const mins = Math.floor(ms / 60_000)
  if (mins < 1) return '<1 min'
  if (mins < 60) return `${mins} min`
  const hours = Math.floor(mins / 60)
  if (hours < 48) return `${hours} h`
  return `${Math.floor(hours / 24)} d`
}
