import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PageSkeleton } from '@/components/feedback/LoadingState'

/** How long a route may take before the bar appears; a navigation faster than this shows nothing extra. */
export const ROUTE_PROGRESS_DELAY_MS = 150

/**
 * A thin bar across the top of the window while a route's code is loading.
 *
 * It lives INSIDE the shell's Suspense fallback, so "the fallback is mounted" is exactly "a route
 * chunk is still loading" (navigation commits synchronously, see router/AppRouter): there is no
 * store or router subscription to keep in step. The delay lives here, so a chunk that arrives
 * within it unmounts the bar before it was ever drawn and a warm navigation does not flash.
 *
 * The bar creeps toward ~85% and waits (nothing says how far a chunk is), unlike the sliding bar
 * of components/RequestProgressBar, which means "a write is on the wire". Under reduced motion
 * the creep is skipped globally (styles/ui.css) and the bar simply stands at its end width.
 */
export function RouteLoadingBar({ delay = ROUTE_PROGRESS_DELAY_MS }: { delay?: number }) {
  const { t } = useTranslation('common')
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const timer = setTimeout(() => setVisible(true), delay)
    return () => clearTimeout(timer)
  }, [delay])
  if (!visible) return null
  return (
    <div
      role="progressbar"
      aria-label={t('common:status.working')}
      data-slot="route-progress"
      className="pointer-events-none fixed inset-x-0 top-0 z-40 h-0.5"
    >
      <div className="h-full w-full origin-left animate-route-progress bg-brand" />
    </div>
  )
}

/** What the shell shows while a route's chunk loads: the bar (after its delay) over the page skeleton. */
export function RouteFallback() {
  return (
    <>
      <RouteLoadingBar />
      <PageSkeleton />
    </>
  )
}
