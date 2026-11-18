import { destErrorOf, type DestPolicyRef } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import i18n from '@/i18n'

/** The admin declined the route-impact confirmation: nothing was changed, and nothing should be reported. */
export class RouteImpactCancelled extends Error {
  constructor() { super('route impact declined') }
}

export const isRouteImpactCancelled = (err: unknown): err is RouteImpactCancelled => err instanceof RouteImpactCancelled

/**
 * Runs a list write. When the server refuses it with 409 dest_list_route_impact
 * (after the edit the list would give some node's routes nothing), names those
 * nodes and asks; only on a yes is the same write sent again with the go-ahead.
 */
/** `run` gets no argument the first time and `true` once the admin has agreed, so the first request is exactly the ordinary one. */
export async function confirmingRouteImpact<T>(run: (...goAhead: [] | [true]) => Promise<T>): Promise<T> {
  try {
    return await run()
  } catch (err) {
    const e = destErrorOf(err)
    if (e?.status !== 409 || e.code !== 'dest_list_route_impact') throw err
    const used = Array.isArray(e.body.used_by) ? (e.body.used_by as DestPolicyRef[]) : []
    const nodes = used.filter(u => u.kind === 'route').map(u => u.name || `#${u.id}`).join(', ')
    const ok = await confirm({
      title: i18n.t('admin:access_control.confirm.route_impact.title') as string,
      message: i18n.t('admin:access_control.confirm.route_impact.message', { nodes }) as string,
      destructive: true,
      confirmText: i18n.t('admin:access_control.confirm.route_impact.action') as string,
    })
    if (!ok) throw new RouteImpactCancelled()
    return run(true)
  }
}
