import { Suspense, useEffect } from 'react'
import AppRouter from '@/router/AppRouter'
import RequestProgressBar from '@/components/RequestProgressBar'
import { useTranslation } from 'react-i18next'

import { SUPPORTED_LANGUAGES } from '@/i18n'
import ToastHost from '@/components/feedback/ToastHost'
import ConfirmHost from '@/components/ConfirmHost'
import ErrorBoundary from '@/components/ErrorBoundary'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { QuerySessionProvider } from '@/query/QuerySessionProvider'
import { router } from '@/router'
import { useAppearanceSync } from '@/theme/htmlAppearance'

export default function App() {
  const { i18n } = useTranslation()

  // Keep <html lang> on the active language (an unknown code falls back to zh-CN, the base).
  useEffect(() => {
    const handler = (lng: string) => {
      document.documentElement.lang = SUPPORTED_LANGUAGES.includes(lng) ? lng : 'zh-CN'
    }
    handler(i18n.resolvedLanguage ?? 'zh-CN')
    i18n.on('languageChanged', handler)
    return () => { i18n.off('languageChanged', handler) }
  }, [i18n])

  // Mode (with 'auto' following the OS live), colour and density onto <html>.
  useAppearanceSync()

  return (
    <>
      {/* ToastHost / ConfirmHost stay OUTSIDE the boundary so a
          render crash inside a route can still surface as a toast or
          confirm dialog if anything in cleanup tries to push one. */}
      <ErrorBoundary>
        {/* Outside Suspense on purpose: the client must survive a lazy route
            suspending, or every route transition would start from an empty
            cache. */}
        <QuerySessionProvider>
          <Suspense fallback={<RouteFallback />}>
            <AppRouter router={router} />
          </Suspense>
        </QuerySessionProvider>
      </ErrorBoundary>
      <RequestProgressBar />
      <ToastHost />
      <ConfirmHost />
    </>
  )
}

function RouteFallback() {
  return <div className="min-h-svh bg-background"><PageSkeleton /></div>
}
