import { createContext, useContext, useEffect, useState, type Dispatch, type ReactNode, type SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'

import { PageSkeleton } from '@/components/feedback/LoadingState'

/** How long a route may take before the bar appears; a navigation faster than this shows nothing extra. */
export const ROUTE_PROGRESS_DELAY_MS = 150

// The Suspense fallback lives INSIDE the animated <main> (a transformed ancestor makes `fixed`
// position against it, so a bar drawn there slid under the sticky top bar while the entrance ran).
// So the bar is drawn by the shell, outside <main>, and the fallback only reports that it is
// mounted: "a fallback is mounted" is exactly "a route chunk is still loading" (navigation
// commits synchronously, see router/AppRouter).
const LoadingCount = createContext<Dispatch<SetStateAction<number>> | null>(null)

/** The shell level: owns the count of mounted route fallbacks and draws the bar above everything in the shell. */
export function RouteProgressHost({ children, delay = ROUTE_PROGRESS_DELAY_MS }: { children: ReactNode; delay?: number }) {
  const { t } = useTranslation('common')
  const [loading, setLoading] = useState(0)
  const [visible, setVisible] = useState(false)
  // The delay lives here, so a chunk that arrives within it unmounts the fallback before the bar was ever drawn.
  useEffect(() => {
    if (loading === 0) { setVisible(false); return }
    const timer = setTimeout(() => setVisible(true), delay)
    return () => clearTimeout(timer)
  }, [loading, delay])
  return (
    <LoadingCount.Provider value={setLoading}>
      {children}
      {visible && (
        <div
          role="progressbar"
          aria-label={t('common:status.working')}
          data-slot="route-progress"
          // Above the sticky top bar (z-30), below dialogs.
          className="pointer-events-none fixed inset-x-0 top-0 z-40 h-0.5"
        >
          {/* Creeps toward ~85% and waits (nothing says how far a chunk is), unlike the sliding bar of
              components/RequestProgressBar ("a write is on the wire"). Reduced motion stands it at its end width. */}
          <div className="h-full w-full origin-left animate-route-progress bg-brand" />
        </div>
      )}
    </LoadingCount.Provider>
  )
}

/** What the shell shows while a route's chunk loads: the page skeleton, and a report to the shell's bar. */
export function RouteFallback() {
  const set = useContext(LoadingCount)
  useEffect(() => {
    set?.(n => n + 1)
    return () => set?.(n => n - 1)
  }, [set])
  return <PageSkeleton />
}
