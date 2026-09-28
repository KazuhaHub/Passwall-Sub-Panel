import type { ReactNode } from 'react'
import { Navigate, useSearchParams } from 'react-router'
import { useCan } from '@/utils/permissions'

// THE OLD LINKS TO THE LOCATION AND RISK TABS STILL LAND WHERE THEY ARE ANSWERED.
//
// Both tabs used to live on the Logs page as /admin/logs?tab=geo|risk, and
// those links survive in bookmarks, browser history and anything copied out of
// the panel before the move. The Logs page's tab parser no longer knows either
// value and would quietly fall back to subscription logs — an answer to "who
// is sharing?" that is a list of fetches. So the route sends them on, straight
// to the risk center tab that answers the question now: the attention queue,
// narrowed to the location detector for `geo`. Straight there rather than to
// the risk center's own `?tab=geo`, which would redirect a second time.
//
// Admins only. An operator cannot open the risk center (every read behind it
// is adminGroup), and sending one there would bounce them on to the dashboard;
// the Logs page they asked for is the better landing, and its parser shows
// them the subscription logs they can read.
//
// It wraps the lazy LogsView instead of being a second lazy page, so the
// redirect costs no chunk, and the check lives in the router rather than in
// LogsView, which stays a page about logs.
const TARGETS: Record<string, string> = {
  geo: '/admin/risk?tab=queue&source=geo',
  risk: '/admin/risk?tab=queue',
}

export default function LogsRoute({ children }: { children: ReactNode }) {
  const [params] = useSearchParams()
  const canView = useCan('risk.view')
  const tab = params.get('tab') ?? ''
  const target = Object.hasOwn(TARGETS, tab) ? TARGETS[tab] : undefined
  if (canView && target) return <Navigate to={target} replace />
  return <>{children}</>
}
