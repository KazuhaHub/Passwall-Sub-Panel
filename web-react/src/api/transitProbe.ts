import { isAxiosError } from 'axios'
import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { ProbeQuality, ProbeState } from './types'

// Link probes (handler/admin_transit_probe*.go, adminGroup only). Writes and
// reads are silent (`_skipErrorToast`): a 400 names the offending field, which
// the settings section shows under that field.

export type { ProbeQuality, ProbeState } from './types'

export interface TransitProbeSettings {
  transit_probe_enabled: boolean
  /** 0 = default. Valid 30..3600. */
  transit_probe_interval_seconds: number
  /** 0 = default. Valid 500..10000. */
  transit_probe_timeout_ms: number
  /** The actual URL (the default is filled in for a never-configured panel).
   *  Empty = transport-only: no end-to-end fetch, only the transport connect is tested.
   *  Otherwise http/https, port 1..65535. */
  transit_probe_url: string
  transit_probe_withhold: boolean
  /** 0 = default. Valid 1..20. */
  transit_probe_withhold_after: number
  /** 0 = default. Valid 1..20. */
  transit_probe_restore_after: number
}

export type TransitProbeSettingKey = keyof TransitProbeSettings

export interface TransitProbeSettingsView {
  settings: TransitProbeSettings
  defaults: Partial<Record<TransitProbeSettingKey, number | string | boolean>>
  effective: TransitProbeSettings
}

/** One probed link as listed by GET /admin/transit/probes. */
export interface TransitProbe {
  /** `lnk_N` for a tunnel; other prefixes name managed transit lines. */
  key: string
  /** e2e = fetched through the tunnel; transport = connect only. */
  kind: 'e2e' | 'transport'
  entry_panel_id: number
  exit_panel_id: number
  state: ProbeState
  rtt_median_ms: number
  loss_percent: number
  withheld: boolean
  /** The measurement is old; it is shown as "no data". */
  stale?: boolean
  reported_at?: string
}

/** The measurement to show for one entry to exit pair: end-to-end if there is one, else transport. */
export function pickLinkProbe(items: TransitProbe[], entry: number, exit: number): TransitProbe | undefined {
  const pair = items.filter(p => p.entry_panel_id === entry && p.exit_panel_id === exit && p.key.startsWith('lnk_'))
  return pair.find(p => p.kind === 'e2e') ?? pair.find(p => p.kind === 'transport')
}

/** Maps a listed probe to what ProbeQualityChip draws; a stale one is unknown. */
export function probeQualityOf(p: TransitProbe): ProbeQuality {
  const state: ProbeState = p.stale ? 'unknown' : p.state
  return {
    state, median_rtt_ms: p.rtt_median_ms, loss_percent: p.loss_percent,
    withheld: p.withheld, reported_at: p.reported_at,
  }
}

export interface TransitProbeError {
  status: number
  code: string
  field?: string
  message: string
}

const BASE = '/admin/transit'
const quiet = { _skipErrorToast: true } as const

export async function getTransitProbeSettings(opts: ReadOptions = {}): Promise<TransitProbeSettingsView> {
  const { data } = await client.get<TransitProbeSettingsView>(`${BASE}/probe-settings`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, defaults: data.defaults ?? {} }
}

/** Sends only the changed keys; the server answers with the whole view. */
export async function putTransitProbeSettings(changed: Partial<TransitProbeSettings>): Promise<TransitProbeSettingsView> {
  const { data } = await client.put<TransitProbeSettingsView>(`${BASE}/probe-settings`, { settings: changed }, quiet)
  return { ...data, defaults: data.defaults ?? {} }
}

export async function getTransitProbes(panelId?: number, opts: ReadOptions = {}): Promise<TransitProbe[]> {
  const { data } = await client.get<{ probes: TransitProbe[] | null }>(`${BASE}/probes`, {
    params: panelId ? { panel_id: panelId } : {}, signal: opts.signal, _skipErrorToast: true,
  })
  return data.probes ?? []
}

export function transitProbeErrorOf(err: unknown): TransitProbeError | null {
  if (!isAxiosError(err) || !err.response) return null
  const body = (err.response.data ?? {}) as Record<string, unknown>
  return {
    status: err.response.status,
    code: typeof body.code === 'string' ? body.code : '',
    field: typeof body.field === 'string' ? body.field : undefined,
    message: typeof body.error === 'string' ? body.error : err.message,
  }
}
