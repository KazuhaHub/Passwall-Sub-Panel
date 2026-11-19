import { useSyncExternalStore } from 'react'

/**
 * Whether a CSS media query matches, live. Unlike use-mobile's useIsMobile it
 * tolerates an environment without matchMedia (tests, very old webviews) by
 * answering false, so a component that switches layout on it never throws.
 */
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    onChange => {
      if (typeof window === 'undefined' || !window.matchMedia) return () => {}
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query).matches : false),
    () => false,
  )
}
