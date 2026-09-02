import type { PanelCapability, Server } from '@/api/servers'

/**
 * Panels that cannot enforce `capability`, named, for a setting PSP intends to
 * push at strength `limit`.
 *
 * This mirrors the server-side rule in sharedclient.reportCapabilityGaps, and
 * deliberately so: a gap only exists when PSP actually wants something
 * enforced. A user left unlimited on a panel that cannot express the cap is
 * not a problem, and warning about it would train the operator to ignore the
 * warning that matters. `limit <= 0` therefore returns no panels at all.
 *
 * A panel reporting no capabilities at all (never probed, older build) counts
 * as lacking it. That errs toward warning, which is the right side to miss on
 * for a hint whose whole job is to stop a silently-unenforced setting.
 */
export function unenforceablePanels(
  limit: number,
  servers: Server[],
  capability: PanelCapability,
): string[] {
  if (!Number.isFinite(limit) || limit <= 0) return []
  return servers
    .filter(s => !(s.capabilities ?? []).includes(capability))
    .map(s => s.name)
}

/**
 * Whether a device cap of `limit` is stored but enforced by nothing.
 *
 * Today this is true of every non-zero device cap, on every panel, at every
 * version — which is why it takes no server list. 3X-UI reads `limit_hwid`
 * for a decision in exactly one place (`effectiveHwidLimitForSubID`, reached
 * only from `EnforceHwidForSubID`), and that path runs only when a client app
 * fetches THE PANEL's own subscription URL. PSP serves subscriptions itself,
 * so the gate never fires, `client_hwids` stays empty for PSP-managed
 * clients, and the cap does nothing. See docs/connection-limits.md §4.1.
 *
 * Deliberately NOT expressed as `unenforceablePanels(limit, servers,
 * 'client.devicelimit')`. That helper names the panels that cannot STORE the
 * field, and naming a subset here would imply the panels it omits do enforce
 * it — a more confident wrong answer than saying nothing.
 *
 * `limit <= 0` is silent, matching unenforceablePanels: a cap nobody set is
 * not a broken promise, and warning about it would train the operator to
 * ignore the warning that matters.
 *
 * This is the single switch to flip once enforcement lands at PSP's own
 * subscription endpoint.
 */
export function deviceCapIsInert(limit: number): boolean {
  return Number.isFinite(limit) && limit > 0
}
