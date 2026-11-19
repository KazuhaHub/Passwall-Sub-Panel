import { useSyncExternalStore } from 'react'

/**
 * Whether a CSS media query matches, live. It tolerates an environment
 * without matchMedia (tests, very old webviews) by answering false, so a component
 * that switches layout on it never throws. useIsMobile is this with the 768px query.
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
