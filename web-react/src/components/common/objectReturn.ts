import { useCallback } from 'react'
import { useLocation } from 'react-router'

/**
 * Coming back to a list the way it was left. A list keeps its place in the URL (page, search, order, filters); an
 * object page opened from it carries that address in the navigation state, and its back link goes there. A page
 * reached any other way (a bookmark, a link from another page, a reload that lost the state) goes to the plain list.
 *
 * The state is the address and nothing else, so it holds no data that could go stale or leak into a shared link.
 */
interface ReturnState { from?: string }

/** `?risk=` and the like are drawers over the list, not part of its place. */
const TRANSIENT = ['risk']

function listAddress(pathname: string, search: string): string {
  const params = new URLSearchParams(search)
  for (const k of TRANSIENT) params.delete(k)
  const rest = params.toString()
  return rest ? `${pathname}?${rest}` : pathname
}

/**
 * For the LIST: a function that turns an object path into link props carrying this list's place, to spread on a
 * `<Link>` or hand to `navigate(to, { state })`.
 */
export function useObjectLink() {
  const { pathname, search } = useLocation()
  return useCallback((to: string): { to: string; state: ReturnState } => ({ to, state: { from: listAddress(pathname, search) } }), [pathname, search])
}

/**
 * For the OBJECT PAGE: where its back link goes. `listPath` is the list's own path; a remembered address is trusted
 * only when it is that list (or a query on it), so a stray state can never turn the back link into an open redirect.
 */
export function useBackTarget(listPath: string): string {
  const state = useLocation().state as ReturnState | null
  const from = state?.from
  return typeof from === 'string' && (from === listPath || from.startsWith(`${listPath}?`)) ? from : listPath
}
