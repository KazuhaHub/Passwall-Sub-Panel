import { useCallback } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'

import { parseUserId } from './riskParams'

/** The state an open() push carries, naming the param it opened. */
interface DrawerState { riskDrawer?: string }

export interface DrawerParam {
  /** The account the URL names, or null (closed). */
  id: number | null
  open: (id: number) => void
  close: () => void
}

/**
 * A drawer driven by one URL param (`user` on the risk center, `risk` on the
 * Users page), so a drawer is a link an admin can copy and the phone's Back
 * closes it.
 *
 * open() PUSHES `param=<id>` — every other param kept — and marks the entry
 * with the param it opened. close() on an entry so marked goes Back: the
 * history then holds no dead "drawer open" entry, and one more Back leaves
 * the page, as a drawer that was never a page should. Any other entry (a deep
 * link, a cold load, a param a redirect wrote) has no entry of ours before
 * it to go back to, so close() REPLACES the param away and stays on the page.
 * The mark names the param, so one page's drawer never takes another's entry
 * for its own.
 */
export function useDrawerParam(param: string): DrawerParam {
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const id = parseUserId(params.get(param))
  const pushedHere = (location.state as DrawerState | null)?.riskDrawer === param

  const open = useCallback((next: number) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      out.set(param, String(next))
      return out
    }, { state: { riskDrawer: param } satisfies DrawerState })
  }, [param, setParams])

  const close = useCallback(() => {
    if (pushedHere) {
      void navigate(-1)
      return
    }
    setParams(prev => {
      const out = new URLSearchParams(prev)
      out.delete(param)
      return out
    }, { replace: true })
  }, [pushedHere, navigate, param, setParams])

  return { id, open, close }
}
