import { useLayoutEffect, useState, type RefObject } from 'react'

/**
 * The content width of an element, kept live, or null until it is measured (and always null where there is no ResizeObserver, as in
 * tests), so a layout that depends on it has a defined answer for "not known yet". Rounded, so a sub-pixel change redraws nothing.
 * A width of 0 is a box that is not drawn (a hidden tab), not a narrow one: the last real width is kept, so coming back does not
 * flash the narrow layout. It is measured before the first paint, so a page wide enough for the docked test starts with it docked.
 */
export function useContainerWidth(ref: RefObject<HTMLElement | null>): number | null {
  const [width, setWidth] = useState<number | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const read = () => { const w = Math.round(el.getBoundingClientRect().width); if (w > 0) setWidth(w) }
    read()
    const ro = new ResizeObserver(read)
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return width
}
