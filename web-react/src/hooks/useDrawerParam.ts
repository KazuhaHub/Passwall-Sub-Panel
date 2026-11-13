import { useCallback, useRef } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'

/**
 * An account id from the URL, or null for anything that is not a positive
 * safe integer: a malformed link asks for nobody rather than for #0 or for
 * whatever Number() makes of the text ("7.5", "07x", "-7").
 */
export function parseUserId(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const id = Number(raw)
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

/** What a test panel is opened with: kept in history state, never in the URL. */
export interface DrawerPrefill {
  target?: string
  port?: number
  network?: string
  userId?: number
  groupId?: number
}

/** The state an open() entry carries, naming the param it opened. */
export interface DrawerState { drawer?: string; prefill?: DrawerPrefill }

export interface DrawerParam<T> {
  /** What the URL names, parsed, or null (closed). */
  value: T | null
  /** The same as `value`, under the name the risk center and the Users page read. */
  id: T | null
  open: (next: T, opts?: { replace?: boolean; prefill?: DrawerPrefill; extra?: Record<string, string> }) => void
  close: () => void
}

/**
 * A drawer driven by one URL param (`user` on the risk center, `risk` on the
 * Users page, `sheet` on the access-control page), so a drawer is a link an
 * admin can copy and the phone's Back closes it.
 *
 * open() PUSHES `param=<value>` — every other param kept, the params in
 * `exclusive` dropped in the same step (only one drawer is open at a time) —
 * and marks the entry with the param it opened. The existing history state is
 * merged, not overwritten; a `prefill` is the caller's to set, and lives in
 * state because a destination must not reach the URL.
 *
 * `replace: true` opens by REPLACING the current entry instead. That is how a
 * page-level sheet hands over to the account drawer: history then holds the
 * page and the drawer, and Back from the drawer lands on the page with no
 * sheet to come back.
 *
 * close() on an entry so marked goes Back: the history then holds no dead
 * "drawer open" entry, and one more Back leaves the page, as a drawer that was
 * never a page should. Any other entry (a deep link, a cold load, a param a
 * redirect wrote) has no entry of ours before it to go back to, so close()
 * REPLACES the param away and stays on the page. The mark names the param, so
 * one page's drawer never takes another's entry for its own. close() does not
 * read the prefill.
 */
export function useDrawerParam<T = number>(
  param: string,
  { parse, exclusive = [], companions = [], format }: {
    parse?: (raw: string | null) => T | null
    exclusive?: readonly string[]
    /** Params that belong to this drawer (`list=<id>` beside `sheet=list`): set by open({ extra }), dropped by close. */
    companions?: readonly string[]
    format?: (value: T) => string
  } = {},
): DrawerParam<T> {
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const read = (parse ?? (parseUserId as unknown as (raw: string | null) => T | null))
  const value = read(params.get(param))
  // `riskDrawer` is the mark the first version wrote; an entry it pushed still closes by going Back.
  const mark = location.state as (DrawerState & { riskDrawer?: string }) | null
  const pushedHere = mark?.drawer === param || mark?.riskDrawer === param
  // The options are literals at the call site, new on every render: read them
  // through a ref so open and close keep their identity.
  const cfg = useRef({ exclusive, companions, format })
  cfg.current = { exclusive, companions, format }

  const open = useCallback((next: T, opts: { replace?: boolean; prefill?: DrawerPrefill; extra?: Record<string, string> } = {}) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      for (const other of cfg.current.exclusive) out.delete(other)
      for (const c of cfg.current.companions) out.delete(c)
      const fmt = cfg.current.format
      out.set(param, fmt ? fmt(next) : String(next))
      for (const [k, v] of Object.entries(opts.extra ?? {})) out.set(k, v)
      return out
    }, {
      replace: opts.replace === true,
      state: {
        ...(location.state as DrawerState | null),
        drawer: param,
        ...(opts.prefill ? { prefill: opts.prefill } : {}),
      } satisfies DrawerState,
    })
  }, [param, setParams, location.state])

  const close = useCallback(() => {
    if (pushedHere) {
      void navigate(-1)
      return
    }
    setParams(prev => {
      const out = new URLSearchParams(prev)
      out.delete(param)
      for (const c of cfg.current.companions) out.delete(c)
      return out
    }, { replace: true })
  }, [pushedHere, navigate, param, setParams])

  return { value, id: value, open, close }
}
