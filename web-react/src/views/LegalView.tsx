import { useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Skeleton, Typography, useTheme } from '@mui/material'
import { Link as RouterLink, Navigate, useNavigate, useParams, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { getPublicLegal, isLegalKind, type LegalKind, type PublicLegalDocument } from '@/api/legal'
import BrandLogo from '@/components/BrandLogo'
import LanguageMenu from '@/components/LanguageMenu'
import LegalDocument from '@/components/LegalDocument'
import { currentLanguage, isBuiltinLanguage, setLanguage } from '@/i18n'
import { useSiteStore } from '@/stores/site'
import type { AppLanguage } from '@/theme'

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
  const md = useTheme().palette.md
  const { t, i18n } = useTranslation('auth')
  const { t: tLang } = useTranslation('language')
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const site = useSiteStore()
  const lang = params.get('lang') || currentLanguage()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => { void site.load() }, [site])

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

  function changeLanguage(next: AppLanguage) {
    setLanguage(next)
    setParams(prev => { const p = new URLSearchParams(prev); p.set('lang', next); return p }, { replace: true })
  }

  const other: LegalKind = kind === 'terms' ? 'privacy' : 'terms'
  const langName = (code: string) => isBuiltinLanguage(code) ? tLang(code) : code

  return (
    <Box sx={{ position: 'fixed', inset: 0, overflowY: 'auto', bgcolor: md.surface, color: md.onSurface }}>
      <Box component="header" sx={{ display: 'flex', alignItems: 'center', gap: 1.5, px: { xs: 2, sm: 3 }, py: 1.5 }}>
        <BrandLogo height={36} />
        <Typography sx={{ fontSize: 17, fontWeight: 500, flex: 1 }}>{site.appTitle || site.siteTitle}</Typography>
        <LanguageMenu value={i18n.language as AppLanguage} onChange={changeLanguage} />
      </Box>

      <Box component="main" sx={{ maxWidth: 720, mx: 'auto', px: 2, pt: { xs: 2, sm: 4 }, pb: 6 }}>
        {state.status === 'loading' && (
          <Box aria-busy="true">
            <Skeleton variant="text" sx={{ fontSize: 28, width: '50%' }} />
            {[0, 1, 2, 3, 4, 5].map(i => <Skeleton key={i} variant="text" />)}
          </Box>
        )}

        {state.status === 'off' && (
          <Card sx={{ p: 4, textAlign: 'center', bgcolor: md.surfaceContainerLow }}>
            <Typography sx={{ mb: 2 }}>{t('legal.not_enabled')}</Typography>
            <Button variant="outlined" onClick={goBack}>{t('legal.back')}</Button>
          </Card>
        )}

        {state.status === 'error' && (
          <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => setAttempt(n => n + 1)}>{t('legal.retry')}</Button>}>
            {t('legal.load_failed')}
          </Alert>
        )}

        {state.status === 'ok' && (
          <>
            <Typography component="h1" sx={{ fontSize: { xs: 24, sm: 28 }, fontWeight: 600 }}>{t(`legal.kind.${kind}`)}</Typography>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.5 }}>
              {t('legal.published', {
                version: state.doc.version,
                date: new Date(state.doc.published_at).toLocaleDateString(state.doc.locale, { year: 'numeric', month: 'long', day: 'numeric' }),
              })}
            </Typography>
            {state.doc.fallback_from && (
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.5 }}>
                {t('legal.fallback_notice', { lang: langName(state.doc.locale) })}
              </Typography>
            )}
            <Box sx={{ mt: 2 }}>
              <LegalDocument content={state.doc.content} dataCollection={state.doc.data_collection} />
            </Box>
          </>
        )}

        <Box sx={{ mt: 4, pt: 2, borderTop: `1px solid ${md.outlineVariant}`, display: 'flex', flexWrap: 'wrap', gap: 1, alignItems: 'center' }}>
          <Button onClick={goBack} sx={{ minHeight: 44 }}>{t('legal.back_and_other')}</Button>
          <Button component={RouterLink} to={`/legal/${other}`} sx={{ minHeight: 44 }}>{t(`legal.kind.${other}`)}</Button>
        </Box>
      </Box>
    </Box>
  )
}
