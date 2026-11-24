import { useEffect, useState } from 'react'
import { Link as RouterLink, Navigate, useNavigate, useParams, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { getPublicLegal, isLegalKind, type LegalKind, type PublicLegalDocument } from '@/api/legal'
import LegalDocument from '@/components/LegalDocument'
import { LanguageMenu } from '@/components/shell/LanguageMenu'
import { BrandLogo } from '@/components/shell/BrandMark'
import { Button, buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TooltipProvider } from '@/components/ui/tooltip'
import { currentLanguage, isBuiltinLanguage, setLanguage } from '@/i18n'
import { useBrand, useSiteBootstrap } from '@/router/shellBootstrap'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FormAlert } from '@/views/auth/parts'

type State =
  | { status: 'loading' }
  | { status: 'ok'; doc: PublicLegalDocument }
  | { status: 'off' }
  | { status: 'error' }

/** /legal/terms and /legal/privacy: public, outside the sign-in guard. */
export default function LegalView() {
  const { kind } = useParams()
  if (!isLegalKind(kind)) return <Navigate to="/" replace />
  return <LegalPage kind={kind} />
}

function LegalPage({ kind }: { kind: LegalKind }) {
  const { t, i18n } = useTranslation('auth')
  const { t: tLang } = useTranslation('language')
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const brand = useBrand()
  useSiteBootstrap()
  const lang = params.get('lang') || currentLanguage()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const ctl = new AbortController()
    setState({ status: 'loading' })
    getPublicLegal(kind, lang, { signal: ctl.signal })
      .then(doc => setState(doc ? { status: 'ok', doc } : { status: 'off' }))
      .catch(() => { if (!ctl.signal.aborted) setState({ status: 'error' }) })
    return () => ctl.abort()
  }, [kind, lang, attempt])

  function goBack() {
    // Opened from a page of this app there is somewhere to go back to; opened
    // from a bare link there is not, and the login page is the sensible start.
    if ((window.history.state as { idx?: number } | null)?.idx) navigate(-1)
    else navigate('/login')
  }

  function changeLanguage(next: string) {
    setLanguage(next)
    setParams(prev => { const p = new URLSearchParams(prev); p.set('lang', next); return p }, { replace: true })
  }

  const other: LegalKind = kind === 'terms' ? 'privacy' : 'terms'
  const langName = (code: string) => isBuiltinLanguage(code) ? tLang(code) : code

  // A reading page, not a card: the document runs the full column, with the same header
  // (logo, name, language) as the other pages outside the shell.
  return (
    <TooltipProvider delay={300}>
      <div className="min-h-svh bg-background text-foreground">
        <header className="flex items-center gap-3 px-4 py-3 sm:px-6">
          <BrandLogo brand={brand} />
          <span className="flex-1 truncate text-base font-medium">{brand.name}</span>
          <LanguageMenu value={i18n.language} onChange={changeLanguage} />
        </header>

        <main className="mx-auto max-w-3xl px-4 pt-4 pb-16 sm:pt-8">
          {state.status === 'loading' && (
            <div role="status" aria-busy="true" className="space-y-2">
              <span className="sr-only">{t('legal.kind.' + kind)}</span>
              <Skeleton className="h-8 w-1/2" />
              {[0, 1, 2, 3, 4, 5].map(i => <Skeleton key={i} className="h-4 w-full" />)}
            </div>
          )}

          {state.status === 'off' && (
            <div className="flex flex-col items-center gap-4 rounded-xl border border-dashed px-6 py-12 text-center">
              <p>{t('legal.not_enabled')}</p>
              <Button variant="outline" onClick={goBack}>{t('legal.back')}</Button>
            </div>
          )}

          {state.status === 'error' && (
            <div className="flex flex-col items-start gap-3">
              <FormAlert tone="error">{t('legal.load_failed')}</FormAlert>
              <AsyncButton variant="outline" onClick={() => setAttempt(n => n + 1)}>{t('legal.retry')}</AsyncButton>
            </div>
          )}

          {state.status === 'ok' && (
            <>
              <h1 className="text-2xl font-semibold sm:text-3xl">{t(`legal.kind.${kind}`)}</h1>
              <p className="mt-1 text-xs text-muted-foreground">
                {t('legal.published', {
                  version: state.doc.version,
                  date: new Date(state.doc.published_at).toLocaleDateString(state.doc.locale, { year: 'numeric', month: 'long', day: 'numeric' }),
                })}
              </p>
              {state.doc.fallback_from && (
                <p className="mt-1 text-xs text-muted-foreground">
                  {t('legal.fallback_notice', { lang: langName(state.doc.locale) })}
                </p>
              )}
              <div className="mt-4">
                <LegalDocument content={state.doc.content} dataCollection={state.doc.data_collection} />
              </div>
            </>
          )}

          <div className="mt-8 flex flex-wrap items-center gap-2 border-t pt-4">
            <Button variant="outline" className="min-h-11 sm:min-h-0" onClick={goBack}>{t('legal.back_and_other')}</Button>
            {/* A real link, not a button rendered as one: it goes to another page. */}
            <RouterLink to={`/legal/${other}`} className={buttonVariants({ variant: 'outline', className: 'min-h-11 sm:min-h-0' })}>
              {t(`legal.kind.${other}`)}
            </RouterLink>
          </div>
        </main>
      </div>
    </TooltipProvider>
  )
}
