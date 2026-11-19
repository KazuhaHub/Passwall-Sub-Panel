import { useCallback, useContext, useRef } from 'react'
import { UNSAFE_DataRouterContext, useLocation, useNavigate } from 'react-router'
import { useUrlWriter } from './urlBatch'

/**
 * "The selected row is in the URL" (plan 3.1): a list page whose quick-view
 * panel is a link a person can copy, reload and close with Back.
 *
 * select() PUSHES `param=<id>` the first time, so the entry the list was on is
 * still behind it; selecting another row while one is open REPLACES, so moving
 * down a list with the arrow keys does not leave a Back step per row.
 *
 * clear() closes the panel and keeps everything else as it is NOW. If select()
 * made the entry, closing goes Back (no dead "panel open" entry is left in
 * history) and then writes the current params, minus the selection, over the
 * entry it landed on: a keyword changed while the panel was open must survive the
 * close, and a plain Back would restore the one from before. On a deep link or a
 * reload there is no entry of ours to return to, so it just removes the param.
 * The mark lives in history state and names the param, so one page's panel never
 * takes another's entry for its own.
 *
 * It writes through useUrlWriter, so it composes with every other URL hook on
 * the page in the same tick.
 */
export interface UrlSelection<T> {
  selected: T | null
  isOpen: boolean
  select: (id: T, opts?: { replace?: boolean }) => void
  clear: () => void
}

interface SelectionMark { pspSelection?: string }

export function useUrlSelection<T = string>(
  param: string,
  { parse, format }: { parse?: (raw: string | null) => T | null; format?: (id: T) => string } = {},
): UrlSelection<T> {
  const location = useLocation()
  const navigate = useNavigate()
  const router = useContext(UNSAFE_DataRouterContext)?.router
  const write = useUrlWriter()
  const raw = new URLSearchParams(location.search).get(param)
  const read = parse ?? ((r: string | null) => (r ? (r as unknown as T) : null))
  const selected = read(raw)
  const cfg = useRef({ format })
  cfg.current = { format }

  const select = useCallback((id: T, opts: { replace?: boolean } = {}) => {
    const value = cfg.current.format ? cfg.current.format(id) : String(id)
    write((params, ctx) => {
      const wasOpen = params.get(param) !== null
      params.set(param, value)
      // Mark only the entry this call pushes; a switch between rows keeps the mark it already has.
      if (!wasOpen) ctx.state({ pspSelection: param } satisfies SelectionMark)
      if (!wasOpen && opts.replace !== true) ctx.push()
    }, 'replace')
  }, [write, param])

  const clear = useCallback(() => {
    const live = router ? router.state.location : location
    const pushedHere = (live.state as SelectionMark | null)?.pspSelection === param
    const params = new URLSearchParams(live.search)
    params.delete(param)
    const text = params.toString()
    const search = text ? `?${text}` : ''
    if (pushedHere && router) {
      void router.navigate(-1).then(() => {
        if (router.state.location.search !== search) {
          void router.navigate({ pathname: router.state.location.pathname, search }, { replace: true, state: router.state.location.state })
        }
      })
      return
    }
    void navigate({ search }, { replace: true, state: live.state })
  }, [router, location, navigate, param])

  return { selected, isOpen: selected !== null, select, clear }
}
