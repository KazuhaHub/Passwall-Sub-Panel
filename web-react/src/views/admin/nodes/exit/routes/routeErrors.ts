import type { TFunction } from 'i18next'
import { routeErrorOf, type RouteNote, type RouteSupportReason, type RouteWarning } from '@/api/routes'
import type { DraftIssue } from './routeDraft'

const K = 'admin:nodes.routes.'

/** How a route is named in a sentence: its position and label, or the raw reference when it is not in the list. */
export type RouteLabeler = (ref: unknown) => string

/** Flattens parameters for a sentence: lists are joined, route references named. */
function valuesOf(params: Record<string, unknown>, label: RouteLabeler): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(params)) {
    if (k === 'route') out[k] = label(v)
    else if (k === 'items' && Array.isArray(v)) {
      out[k] = v.map(item => {
        const i = (item ?? {}) as Record<string, unknown>
        return `${String(i.rule ?? '')} (${String(i.action ?? '')}, ${label(i.route)})`
      }).join('; ')
    } else out[k] = Array.isArray(v) ? v.join(', ') : v
  }
  return out
}

/** The words for a refusal: the editor's own text for its code, the server's English when the code is new. */
export function routeErrorText(t: TFunction, err: unknown, label: RouteLabeler = String): string {
  const e = routeErrorOf(err)
  if (!e) return err instanceof Error ? err.message : String(err)
  if (!e.code) return e.message
  const values = valuesOf(e.params, label)
  if (e.code === 'over_budget' && typeof values.limit === 'string') values.limit = t(`${K}limit.${values.limit}`, { defaultValue: values.limit }) as string
  return t(`${K}error.${e.code}`, { ...values, defaultValue: e.message }) as string
}

export function warningText(t: TFunction, w: RouteWarning, label: RouteLabeler = String): string {
  const values = valuesOf({ ...(w.params ?? {}), ...(w.route_id && !(w.params && 'route' in w.params) ? { route: w.route_id } : {}) }, label)
  if (w.listener) values.listener = w.listener
  if (w.code === 'budget_near_limit' && typeof values.limit === 'string') values.limit = t(`${K}limit.${values.limit}`, { defaultValue: values.limit }) as string
  if (w.code === 'routes_not_sent' && typeof values.reason === 'string') values.reason = supportText(t, values.reason as RouteSupportReason)
  return t(`${K}warning.${w.code}`, { ...values, defaultValue: w.message }) as string
}

export function noteText(t: TFunction, n: RouteNote): string {
  return t(`${K}note.${n.code}`, { list: n.list ?? (n.list_id !== undefined ? `#${n.list_id}` : ''), count: n.count ?? 0 }) as string
}

/** Why the editor is disabled; an unknown reason says so rather than showing nothing. */
export function supportText(t: TFunction, reason: RouteSupportReason | string | undefined): string {
  return t(`${K}reason.${reason || 'unknown'}`, { defaultValue: t(`${K}reason.unknown`) as string }) as string
}

export function issueText(t: TFunction, issue: DraftIssue, label: RouteLabeler = String): string {
  const route = label(issue.route)
  return t(`${K}issue.${issue.code}`, { route, entry: 'entry' in issue ? issue.entry : '' }) as string
}
