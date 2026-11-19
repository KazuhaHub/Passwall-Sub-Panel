import { useCallback, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router'

/**
 * "The selected row is in the URL" (plan 3.1): a list page whose quick-view
 * panel is a link a person can copy, reload and close with Back.
 *
 * select() PUSHES `param=<id>` the first time, so the entry the list was on is
 * still behind it; selecting another row while one is open REPLACES, so moving
 * down a list with the arrow keys does not leave a Back step per row. clear()
 * goes Back if select() made the entry (no dead "panel open" entry is left in
 * history); on a deep link or a reload there is no entry of ours to return to,
 * so it replaces the param away and stays on the page. The mark lives in
 * history state and names the param, so one page's panel never takes another's
 * entry for its own. Every other query param is left as it was.
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
  const raw = new URLSearchParams(location.search).get(param)
  const read = parse ?? ((r: string | null) => (r ? (r as unknown as T) : null))
  const selected = read(raw)
  const cfg = useRef({ format })
  cfg.current = { format }
  const pushedHere = (location.state as SelectionMark | null)?.pspSelection === param

  const select = useCallback((id: T, opts: { replace?: boolean } = {}) => {
    const params = new URLSearchParams(location.search)
    const wasOpen = params.get(param) !== null
    params.set(param, cfg.current.format ? cfg.current.format(id) : String(id))
    const mark: SelectionMark = { pspSelection: pushedHere || !wasOpen ? param : undefined }
    void navigate({ search: `?${params.toString()}` }, {
      replace: opts.replace ?? wasOpen,
      state: { ...(location.state as object | null), ...mark },
    })
  }, [navigate, location.search, location.state, param, pushedHere])

  const clear = useCallback(() => {
    if (pushedHere) {
      void navigate(-1)
      return
    }
    const params = new URLSearchParams(location.search)
    params.delete(param)
    const text = params.toString()
    void navigate({ search: text ? `?${text}` : '' }, { replace: true, state: location.state })
  }, [navigate, location.search, location.state, param, pushedHere])

  return { selected, isOpen: selected !== null, select, clear }
}
