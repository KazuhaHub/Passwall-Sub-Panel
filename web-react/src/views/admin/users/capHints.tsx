import type { ReactNode } from 'react'
import type { LimitEnforcement, LimitPanel } from '@/api/limitEnforcement'
import type { Server } from '@/api/servers'
import { FieldHint } from '@/components/common/HelpTip'
import { deviceCapIsInert, ipCapUnenforcedPanels, unenforceablePanels } from '@/utils/capabilities'
import { assessDeviceLimit, assessIPLimit, isMisleading, type CapAssessment } from '@/utils/limitEnforcement'
import { useAdminT } from './useAdminT'

/**
 * What the two connection caps would actually do. A cap the operator can set but the fleet cannot enforce is the
 * failure shape the capability list exists to prevent: the write succeeds and nothing happens. Every hint is a
 * short verdict with the reasoning one click away (a full sentence under the field reflows the form as the admin
 * types). The two halves share a key stem, `<key>` the detail and `<key>_short` the summary, so a hint cannot be
 * added with only one of them written; utils/limitEnforcement.test.ts checks both.
 *
 * `facts` is the per-user answer, edit only: it is SCOPED to the user's own panels and can say the thing the
 * fleet-wide check structurally cannot, that the caps are budgeted per panel-side client, so the reachable total
 * is a multiple of what was typed. Until it arrives (or when it cannot be read, as for an operator) the
 * fleet-wide hint stands in, which warns rather than asserts a cap is fine.
 */
export function useCapHints(servers: Server[], facts: LimitEnforcement | null) {
  const t = useAdminT()

  function hint(key: string, vars?: Record<string, unknown>, tone?: 'warning' | 'muted'): ReactNode {
    return <FieldHint tone={tone} summary={t(`users.field.${key}_short`, vars ?? {})} detail={t(`users.field.${key}`, vars ?? {})} />
  }

  function fleetHint(limit: number, capability: 'client.iplimit' | 'client.devicelimit'): ReactNode {
    // The device cap is stored everywhere and enforced nowhere (3X-UI applies limitHwid only when a client fetches
    // THE PANEL's own subscription URL, and PSP serves subscriptions itself), so its warning is unconditional:
    // naming panels would imply the ones not named do enforce it.
    if (capability === 'client.devicelimit' && deviceCapIsInert(limit)) return hint('device_limit_inert')
    const panels = unenforceablePanels(limit, servers, capability)
    if (panels.length) return hint('limit_unsupported', { panels: panels.join(' / ') })
    // A panel that CAN store the cap can still be unable to act on it (3X-UI needs fail2ban on the node). Checked
    // second: "the panel drops the field" is the bigger problem, and naming both would read as one confused warning.
    if (capability === 'client.iplimit') {
      const dead = ipCapUnenforcedPanels(limit, servers)
      if (dead.length) return hint('ip_limit_unenforced', { panels: dead.join(' / ') })
    }
    return null
  }

  function userHint(limit: number, which: 'ip' | 'device'): ReactNode {
    if (!facts || !Number.isFinite(limit) || limit <= 0) return null
    // The form's live value, not the saved one: an admin who has just typed a number is asking about THAT number.
    const live = which === 'ip' ? { ...facts, ip_limit: limit } : { ...facts, device_limit: limit }
    const a: CapAssessment = which === 'ip' ? assessIPLimit(live) : assessDeviceLimit(live)
    const names = (ps: LimitPanel[]) => ps.map(p => p.name || `#${p.panel_id}`).join(' / ')
    // Warning colour exactly when the number on the form is not the number in force ('multiplied' counts: the cap
    // works, but the fleet-wide total is a multiple of what was typed).
    const tone = isMisleading(a.verdict) ? 'warning' : 'muted'
    switch (a.verdict) {
      case 'never_enforced': return hint('device_limit_inert', undefined, tone)
      case 'void': return hint('cap_void', { panels: names(a.voidPanels) }, tone)
      case 'unknown': return hint('cap_unknown', { panels: names(a.unknownPanels) }, tone)
      case 'multiplied': return hint('cap_multiplied', { total: a.typed * a.enforcingRows, rows: a.enforcingRows, typed: a.typed }, tone)
      case 'no_clients': return hint('cap_no_clients', undefined, tone)
      default: return null
    }
  }

  return {
    ip: (limit: number, edit: boolean): ReactNode => (edit ? userHint(limit, 'ip') : null) ?? fleetHint(limit, 'client.iplimit'),
    device: (limit: number, edit: boolean): ReactNode => (edit ? userHint(limit, 'device') : null) ?? fleetHint(limit, 'client.devicelimit'),
  }
}

export type CapHints = ReturnType<typeof useCapHints>
