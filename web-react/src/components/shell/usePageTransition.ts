import { useLayoutEffect, useRef, type RefObject } from 'react'

export const PAGE_ENTER_MS = 150

const REDUCED = '(prefers-reduced-motion: reduce)'

/**
 * Fades and lifts the element in whenever `key` changes, never on first mount.
 *
 * The key is the route's PATH: a `?tab=` change inside a page is not a page change and must not
 * replay the entrance. The element is animated through the Web Animations API instead of being
 * remounted by a changing React key, so a path that only swaps a parameter (users/1 to users/2)
 * keeps its page state, and the keyframes leave no inline style behind. It runs in a layout
 * effect so the first painted frame of the new page is already at the start of the animation.
 *
 * Skipped under reduced motion, and where the browser has no `animate`.
 */
export function usePageTransition(ref: RefObject<HTMLElement | null>, key: string): void {
  const previous = useRef(key)
  useLayoutEffect(() => {
    if (previous.current === key) return
    previous.current = key
    const el = ref.current
    if (!el || typeof el.animate !== 'function') return
    if (typeof window.matchMedia === 'function' && window.matchMedia(REDUCED).matches) return
    el.animate(
      [{ opacity: 0, transform: 'translateY(6px)' }, { opacity: 1, transform: 'none' }],
      { duration: PAGE_ENTER_MS, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
    )
  }, [key, ref])
}
