import type { ComponentProps } from 'react'
import { RouterProvider } from 'react-router/dom'

// NAVIGATION COMMITS AT ONCE, NOT INSIDE A TRANSITION.
//
// RouterProvider wraps every navigation in React.startTransition unless told
// otherwise, and a transition keeps the OLD screen while the new one suspends.
// Every view is React.lazy, so the first visit to a page suspends on its chunk:
// on a slow network the click did nothing visible — no highlight, no spinner —
// until the chunk arrived, and operators took the panel for broken. The layouts'
// own Suspense fallbacks exist for exactly this wait and were never shown.
//
// Committing synchronously costs nothing on a warm cache: a lazy component whose
// chunk has loaded renders without suspending, and no view suspends on data (all
// server state goes through the query cache, which never throws a promise).
export default function AppRouter({ router }: { router: ComponentProps<typeof RouterProvider>['router'] }) {
  return <RouterProvider router={router} useTransitions={false} />
}
