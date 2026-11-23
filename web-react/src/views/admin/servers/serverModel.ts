import type { PanelCapability, Server } from '@/api/servers'
import type { Tone } from '@/components/common/tones'
import type { AccessToneKind } from '@/utils/accessControl'
import type { NodeRelease } from '@/api/nodeReleases'
import { newerNodeRelease } from '@/utils/nodeReleaseUpdate'
import { newerSUIRelease } from '@/utils/suiReleaseUpdate'

export function hasCapability(s: Server | null, capability: PanelCapability): boolean {
  return !!s?.capabilities?.includes(capability)
}

export const TYPE_LABEL: Record<string, string> = { sui: 'S-UI', psp: 'Passwall Node', '3xui': '3X-UI' }
export const typeLabel = (s: Server) => TYPE_LABEL[s.panel_type] ?? '3X-UI'

/** "4.1.0 (a1b2c3d)" -> the version to show and the commit for the tooltip. */
export function splitVersionIdentity(value: string | undefined): { display: string; commit?: string } {
  const full = value?.trim() ?? ''
  const match = full.match(/^(.*?)\s+\(([0-9a-f]{7,40})\)$/i)
  if (!match) return { display: full }
  return { display: match[1], commit: match[2] }
}

/**
 * The access-control state of a node as a tone. The access page owns the closed colour table; the list needs only the
 * four things a row can say (in effect, needs attention, failing, nothing to say), and the words carry the rest.
 */
export function accessToneOf(kind: AccessToneKind): Tone {
  switch (kind) {
    case 'ok': return 'success'
    case 'attention': return 'warning'
    case 'failing': return 'danger'
    default: return 'neutral'
  }
}

export const ipCapTone: Record<'error' | 'info' | 'neutral', Tone> = { error: 'danger', info: 'info', neutral: 'neutral' }

/** The release a Passwall Node, an S-UI or a 3X-UI server could move to, or undefined when it is current or not yet known. */
export function updateVersionOf(s: Server, nodeReleases: NodeRelease[] | null, suiReleaseVersion: string | null): string | undefined {
  if (s.panel_type === 'psp') return nodeReleases ? newerNodeRelease(s, nodeReleases)?.version : undefined
  // S-UI: the shared release read when it has answered; until then (or when it cannot) the row's own last-known fields.
  if (s.panel_type === 'sui' && suiReleaseVersion) return newerSUIRelease(s.panel_version, suiReleaseVersion)
  const upstream = s.panel_type === 'sui' ? s.latest_sui_version : s.latest_xui_version
  return s.update_available ? upstream : undefined
}

export type ResourceTone = { tone: Tone; quiet: boolean }
/** Resource health of a native node. Healthy and warming up say nothing; unsupported is neutral, never an alarm. */
export function resourceTone(s: Server): ResourceTone | null {
  switch (s.node_resource_health) {
    case 'warning': case 'stale': return { tone: 'warning', quiet: false }
    case 'critical': return { tone: 'danger', quiet: false }
    default: return null
  }
}
