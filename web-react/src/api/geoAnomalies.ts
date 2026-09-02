import { client } from './client'

/**
 * One user's current concurrent-location verdict.
 *
 * `reason` travels with `state` on purpose: an operator deciding whether to
 * act on an account needs why, and a flag with no explanation leaves them
 * trusting the panel blindly or ignoring it.
 */
export interface GeoAnomaly {
  user_id: number
  upn?: string
  display_name?: string
  /**
   * Seven states, and only `flagged` is meant to drive action. `suspect` is
   * the visible ramp; `unknown` / `exempt` / `disabled` / `idle` all mean the
   * detector is not in a position to judge — and they are indistinguishable
   * from "no flags" if a reader only counts the flagged ones.
   */
  state: 'disabled' | 'exempt' | 'unknown' | 'idle' | 'clean' | 'suspect' | 'flagged'
  reason: string
  places: string[]
  live_ips: number
  /**
   * false means `live_ips` is a FLOOR: a panel holding this user's clients
   * could not be read. Must be rendered — a partial count shown as a total
   * reads as "this account is fine" exactly when the evidence is missing.
   */
  complete: boolean
  over_streak: number
  under_streak: number
  updated_at_ms: number
}

/** Every judged user, newest first. Unfiltered by design: the denominator has
 *  to stay reachable so a detector that has stopped working is visible. */
export async function listGeoAnomalies(signal?: AbortSignal): Promise<GeoAnomaly[]> {
  const { data } = await client.get<{ items: GeoAnomaly[] }>('/admin/geo-anomalies', { signal })
  return data.items ?? []
}
