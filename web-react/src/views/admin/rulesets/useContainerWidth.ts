import { useEffect, useState, type RefObject } from 'react'

/**
 * The content width of an element, kept live, or null until it is measured (and always null where there is no ResizeObserver, as in
 * tests), so a layout that depends on it has a defined answer for "not known yet". Rounded, so a sub-pixel change redraws nothing.
 */
export function useContainerWidth(ref: RefObject<HTMLElement | null>): number | null {
  const [width, setWidth] = useState<number | null>(null)
  useEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const read = () => setWidth(Math.round(el.getBoundingClientRect().width))
    read()
    const ro = new ResizeObserver(read)
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return width
}
