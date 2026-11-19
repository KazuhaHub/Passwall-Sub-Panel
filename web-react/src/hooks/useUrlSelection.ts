import { useCallback, useRef } from 'react'
import { useLocation } from 'react-router'
import { useUrlWriter } from './urlBatch'

/**
 * "The selected row is in the URL" (plan 3.1): a list page whose quick-view
 * panel is a link a person can copy, reload and close with Back.
 *
 * select() PUSHES `param=<id>` the first time, so the entry the list was on is
 * still behind it; selecting another row while one is open REPLACES, so moving
 * down a list with the arrow keys does not leave a Back step per row.
 *
 * clear() closes the panel by REPLACING the entry with the selection key removed
 * and every other param as it is NOW, so a keyword changed while the panel was open
 * survives. The trade-off of that simple scheme: the entry from before the panel
 * opened is still in history, so after closing from the UI one more Back lands on
 * the list as it was before opening (never back on the panel). Going Back first and
 * rewriting afterwards would remove that extra entry but cannot be batched with other
 * writes in the same tick, which matters more. Back while the panel is open pops the
 * pushed entry, which is what closes it. (No marker is kept in history state: nothing
 * needs to tell a pushed entry from any other.)
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

export function useUrlSelection<T = string>(
  param: string,
  { parse, format }: { parse?: (raw: string | null) => T | null; format?: (id: T) => string } = {},
): UrlSelection<T> {
  const location = useLocation()
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
      // Opening pushes (Back closes it); moving between rows while open replaces.
      if (!wasOpen && opts.replace !== true) ctx.push()
    }, 'replace')
  }, [write, param])

  // Closing is a plain write of "this key is gone": it keeps every other current
  // param, and it composes with any other URL write made in the same tick.
  const clear = useCallback(() => write(params => { params.delete(param) }, 'replace'), [write, param])

  return { selected, isOpen: selected !== null, select, clear }
}
