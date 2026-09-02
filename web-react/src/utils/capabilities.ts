import type { IPLimitEnforcement, PanelCapability, Server } from '@/api/servers'

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

/**
 * Panels where a concurrent-IP cap of `limit` would be stored and then ignored.
 *
 * Distinct from `unenforceablePanels(limit, servers, 'client.iplimit')`, which
 * asks whether the panel can STORE the field. Every supported 3X-UI can: the
 * write returns success and the value reads back. Whether anything happens
 * depends on fail2ban being present and `XUI_ENABLE_FAIL2BAN` not being set to
 * something other than the literal "true" — facts about the NODE that PSP only
 * learns by probing it. The two lists overlap in neither direction.
 *
 * Only `disabled` and `not_installed` are named. `unknown` and `unsupported`
 * are excluded on purpose: naming a panel we could not read would accuse an
 * operator of a fault that may not exist, and the resulting permanent warning
 * on every pre-3.7.0 panel is how a warning stops being read. That silence is
 * a real gap and it is covered where it belongs — the panel's own row carries
 * the state, including the ones left out here.
 *
 * `limit <= 0` is silent, matching the helpers above: a cap nobody set is not
 * a broken promise.
 */
export function ipCapUnenforcedPanels(limit: number, servers: Server[]): string[] {
  if (!Number.isFinite(limit) || limit <= 0) return []
  return servers
    .filter(s => s.ip_limit_enforcement === 'disabled' || s.ip_limit_enforcement === 'not_installed')
    .map(s => s.name)
}

/**
 * How a node's IP-cap verdict should be drawn: which tone, or nothing at all.
 *
 * Kept out of the view and tested directly, because two of these rules are
 * easy to "simplify" into a badge that lies:
 *
 * - `enforced` draws NOTHING. It is the clean state, like a supported compat
 *   version — decoration there would make the fleet unreadable and bury the
 *   rows that need attention.
 * - `unsupported` also draws nothing, for the opposite reason: a panel older
 *   than 3.7.0 has no way to answer, so a badge would be permanent and
 *   un-actionable, and the version cell beside it already says the panel is
 *   old. This is a deliberate blind spot, recorded in
 *   docs/connection-limits.md §5.2.
 * - `unknown` DOES draw, greyed, and must never be folded into either of the
 *   above. It means a panel that could have answered did not, so the probe
 *   itself is broken — and a broken probe that renders like a healthy fleet is
 *   precisely the failure this whole feature exists to prevent.
 */
export type IPCapTone = 'none' | 'error' | 'info' | 'neutral'

export function ipCapBadgeTone(state: IPLimitEnforcement | undefined): IPCapTone {
  switch (state) {
    case 'disabled':
    case 'not_installed':
      return 'error'
    case 'disconnect_only':
      return 'info'
    case 'unknown':
      return 'neutral'
    default:
      return 'none'
  }
}
