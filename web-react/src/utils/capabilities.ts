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
