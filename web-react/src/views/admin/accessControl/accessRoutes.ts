import type { DestAction, DestListKind } from '@/api/accessControl'

/**
 * The addresses of the Access control section. The page itself (`/admin/access-control`, with its four tabs)
 * keeps its own place in its query string; a policy and a list are pages of their own under it, and each has a
 * create form beside it (`…/policies/new`, `…/lists/new`).
 */
export const ACCESS_PATH = '/admin/access-control'
export const POLICIES_PATH = `${ACCESS_PATH}/policies`
export const LISTS_PATH = `${ACCESS_PATH}/lists`

export const policyPath = (id: number) => `${POLICIES_PATH}/${id}`
export const listPath = (id: number) => `${LISTS_PATH}/${id}`

/** What a new policy starts from: a blank one of a step (`report` is a block policy that starts report-only), or a copy of one. */
export type NewPolicySeed = { kind?: 'allow' | 'block' | 'report'; copy?: number }

export function newPolicyPath(seed: NewPolicySeed = {}): string {
  const q = new URLSearchParams()
  if (seed.copy) q.set('copy', String(seed.copy))
  else if (seed.kind) q.set('kind', seed.kind)
  const rest = q.toString()
  return rest ? `${POLICIES_PATH}/new?${rest}` : `${POLICIES_PATH}/new`
}

export function newListPath(kind: DestListKind = 'custom'): string {
  return kind === 'custom' ? `${LISTS_PATH}/new` : `${LISTS_PATH}/new?kind=${kind}`
}

/** A positive whole number from an address, or null for anything else (never a request for #0 or "7x"). */
export function parseObjectId(raw: string | undefined | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const n = Number(raw)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}

export function parseNewPolicySeed(params: URLSearchParams): { seed: NewPolicySeed; invalid: boolean } {
  const rawCopy = params.get('copy')
  if (rawCopy !== null) {
    const copy = parseObjectId(rawCopy)
    return copy ? { seed: { copy }, invalid: false } : { seed: {}, invalid: true }
  }
  const kind = params.get('kind')
  return { seed: { kind: kind === 'allow' || kind === 'report' ? kind : 'block' }, invalid: false }
}

export function parseNewListKind(raw: string | null): DestListKind {
  return raw === 'remote' || raw === 'geosite' ? raw : 'custom'
}

/** The blank form's action for a step. */
export const actionOfStep = (kind: 'allow' | 'block' | 'report'): DestAction => (kind === 'allow' ? 'allow' : 'block')

/** Params that open a drawer over the page, not part of its place: a page opened from there returns to the page itself. */
const TRANSIENT = ['sheet', 'list', 'node_state', 'user']

/**
 * The address a policy or list page's back link returns to: the page as it was left (its tab and the lists tab's
 * filters), without the drawer that may have been open over it. Handed to the new page in navigation state (the shape
 * `useBackTarget` reads), so no data rides along.
 */
export function accessReturnState(pathname: string, search: string): { from: string } {
  const params = new URLSearchParams(search)
  for (const k of TRANSIENT) params.delete(k)
  const rest = params.toString()
  return { from: rest ? `${pathname}?${rest}` : pathname }
}
