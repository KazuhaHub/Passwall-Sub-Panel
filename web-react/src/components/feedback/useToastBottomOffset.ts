import { useEffect, type RefObject } from 'react'

const VAR = '--toast-bottom-offset'

// Every mounted bar registers its own offset; the variable is the largest of them, and it is
// removed only when the last one goes. With two bars mounted, the first to unmount used to
// remove the variable for both.
const offsets = new Map<symbol, number>()

function publish() {
  const root = document.documentElement
  if (offsets.size === 0) { root.style.removeProperty(VAR); return }
  root.style.setProperty(VAR, `${Math.max(...offsets.values())}px`)
}

/**
 * Keeps the toasts clear of a bottom sticky bar (a form's save bar, a bulk-action bar):
 * while the calling component is mounted, `--toast-bottom-offset` on <html> is at least the
 * bar's height (the largest, if several bars are mounted), and it is removed when the last
 * of them unmounts. Pass a number of pixels, or a ref to the bar, whose height is then
 * followed as it changes.
 *
 * `env(safe-area-inset-bottom)` is deliberately not part of the toast position: using it
 * needs `viewport-fit=cover` on the page's viewport meta, which also lets the whole page
 * draw under a phone's notch and home indicator, a layout change for every page that is
 * not this package's to make.
 */
export function useToastBottomOffset(target: number | RefObject<HTMLElement | null>): void {
  const px = typeof target === 'number' ? target : null
  const ref = typeof target === 'number' ? null : target
  useEffect(() => {
    const id = Symbol('toast-bottom-offset')
    const set = (v: number) => { offsets.set(id, Math.max(0, Math.round(v))); publish() }
    let observer: ResizeObserver | undefined
    if (px !== null) set(px)
    else if (ref?.current) {
      const el = ref.current
      set(el.getBoundingClientRect().height)
      if (typeof ResizeObserver !== 'undefined') {
        observer = new ResizeObserver(() => set(el.getBoundingClientRect().height))
        observer.observe(el)
      }
    }
    return () => { observer?.disconnect(); offsets.delete(id); publish() }
  }, [px, ref])
}
