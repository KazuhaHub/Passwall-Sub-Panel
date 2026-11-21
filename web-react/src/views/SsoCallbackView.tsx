import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { IconSpinner } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth'

export default function SsoCallbackView() {
  const { t } = useTranslation('auth')
  const auth = useAuthStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [error, setError] = useState<string>('')

  useEffect(() => {
    let cancelled = false
    async function run() {
      try {
        await auth.loginSSO()
        if (cancelled) return
        navigate(params.get('next') || '/user/me', { replace: true })
      } catch (e) {
        if (cancelled) return
        // The exchange is single-use — it spends the cookies the ACS set — so
        // asking a second time is answered 401 even though the session is real.
        // This page can be mounted twice (a remount, a reload, two tabs), and the
        // one that loses the race must not strand a signed-in person on an error
        // screen showing the server's own text. Read the state rather than the
        // closure: the winning instance has already updated it by now.
        if (useAuthStore.getState().hasToken) {
          navigate(params.get('next') || '/user/me', { replace: true })
          return
        }
        const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error
          ?? t('sso_callback_failed')
        setError(msg)
      }
    }
    void run()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // No card and no menus: this page lasts a moment and nobody is meant to act on it, so it
  // is a centred status line. A failure keeps the server's own text, as before.
  return (
    <div className="grid min-h-svh place-items-center bg-background p-4 text-foreground">
      {error
        ? (
          <div className="flex max-w-sm flex-col items-center gap-4 text-center">
            <p role="alert" className="text-sm text-destructive">{error}</p>
            <Button variant="outline" onClick={() => navigate('/login', { replace: true })}>{t('back_to_login')}</Button>
          </div>
        )
        : (
          <div role="status" className="flex flex-col items-center gap-3">
            <IconSpinner aria-hidden className="size-7 animate-spin text-muted-foreground" />
            <p className="text-sm text-muted-foreground">{t('sso_callback_progress')}</p>
          </div>
        )}
    </div>
  )
}
