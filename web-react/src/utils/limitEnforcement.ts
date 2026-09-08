import type { LimitEnforcement, LimitPanel } from '@/api/limitEnforcement'

/**
 * Turns the per-panel facts into what the admin needs to know at the moment
 * they type a cap.
 *
 * The whole module exists because of one mismatch: the form takes ONE number
 * per user, while both caps are enforced per (panel, client email). Nothing on
 * that form said so, so "2 concurrent IPs" read as a promise the system does
 * not make.
 *
 * Two rules govern every verdict below, and they are not symmetric:
 *
 *   1. A HOLE beats a MULTIPLIER. If any panel cannot enforce the cap, the
 *      user simply routes through that panel — so the fleet-wide cap is not
 *      "higher", it is GONE. Reporting "x3" for a fleet with one unenforcing
 *      panel would understate it to the point of being wrong.
 *   2. UNKNOWN IS NOT OK. A panel whose probe never answered is reported as
 *      unknown, never folded in with the enforcing ones. An unread
 *      precondition rendering as a working one is how a silently dead limit
 *      stays invisible.
 */

export type CapVerdict =
  /** No cap is set, so there is nothing to say. */
  | 'unlimited'
  /** Enforced everywhere the user is, but once PER PANEL — so N x the typed number. */
  | 'multiplied'
  /** Enforced exactly once fleet-wide: the honest happy path. */
  | 'exact'
  /** At least one panel cannot or does not enforce, so the cap is void fleet-wide. */
  | 'void'
  /** Nothing is void, but at least one panel's state is unread. */
  | 'unknown'
  /** The user has no clients yet, so no panel is enforcing anything. */
  | 'no_clients'
  /** Structurally enforced nowhere, on any panel, for any user. */
  | 'never_enforced'

export interface CapAssessment {
  verdict: CapVerdict
  /** The number typed on the form. */
  typed: number
  /**
   * Panel-side client rows that would each enforce `typed` independently.
   * The fleet-wide reachable total is typed x this, and it is only meaningful
   * when the verdict is 'exact' or 'multiplied'.
   */
  enforcingRows: number
  /** Panels that cannot enforce at all — the reason a verdict is 'void'. */
  voidPanels: LimitPanel[]
  /** Panels whose state could not be read. */
  unknownPanels: LimitPanel[]
}

/** Verdicts that mean "the number on this form is not the number in force". */
export function isMisleading(v: CapVerdict): boolean {
  return v === 'multiplied' || v === 'void' || v === 'unknown' || v === 'never_enforced'
}

/**
 * A panel's fail2ban verdict, reduced to whether a stored limitIp does
 * SOMETHING. Mirrors domain.IPLimitEnforcement.Enforcing() exactly: neither
 * "unknown" nor "unsupported" counts, on purpose.
 */
export function ipEnforcing(state: string): boolean {
  return state === 'enforced' || state === 'disconnect_only'
}

/** True when a panel's state is unreadable rather than known-bad. */
function unreadable(p: LimitPanel): boolean {
  return Boolean(p.panel_unreachable || p.panel_missing)
}

export function assessIPLimit(d: LimitEnforcement): CapAssessment {
  const out: CapAssessment = {
    verdict: 'unlimited', typed: d.ip_limit, enforcingRows: 0, voidPanels: [], unknownPanels: [],
  }
  if (d.ip_limit <= 0) return out
  if (d.panels.length === 0) { out.verdict = 'no_clients'; return out }

  for (const p of d.panels) {
    if (unreadable(p)) { out.unknownPanels.push(p); continue }
    if (!p.can_store_ip) { out.voidPanels.push(p); continue }
    // Stored but not acted on is the same hole as not stored at all — worse,
    // because the panel shows the value back and looks configured.
    if (!ipEnforcing(p.ip_limit_enforcement)) {
      if (p.ip_limit_enforcement === 'unknown' || p.ip_limit_enforcement === 'unsupported') {
        out.unknownPanels.push(p)
      } else {
        out.voidPanels.push(p)
      }
      continue
    }
    out.enforcingRows += p.clients
  }

  // Order matters: a hole outranks an unknown, and both outrank the count.
  if (out.voidPanels.length > 0) out.verdict = 'void'
  else if (out.unknownPanels.length > 0) out.verdict = 'unknown'
  else if (out.enforcingRows > 1) out.verdict = 'multiplied'
  else out.verdict = 'exact'
  return out
}

export function assessDeviceLimit(d: LimitEnforcement): CapAssessment {
  const out: CapAssessment = {
    verdict: 'unlimited', typed: d.device_limit, enforcingRows: 0, voidPanels: [], unknownPanels: [],
  }
  if (d.device_limit <= 0) return out
  // Not "no panel happens to support it" — no panel CAN. Upstream counts a
  // device when a client app fetches its subscription from the panel, and PSP
  // serves the subscriptions instead, so that gate never fires for a PSP user
  // on any version. Storing the field is a capability; acting on it is not.
  if (!d.device_limit_enforced_anywhere) { out.verdict = 'never_enforced'; return out }
  if (d.panels.length === 0) { out.verdict = 'no_clients'; return out }

  for (const p of d.panels) {
    if (unreadable(p)) { out.unknownPanels.push(p); continue }
    if (!p.can_store_device) { out.voidPanels.push(p); continue }
    out.enforcingRows += p.clients
  }
  if (out.voidPanels.length > 0) out.verdict = 'void'
  else if (out.unknownPanels.length > 0) out.verdict = 'unknown'
  else if (out.enforcingRows > 1) out.verdict = 'multiplied'
  else out.verdict = 'exact'
  return out
}
