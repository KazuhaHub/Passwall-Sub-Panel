import { useEffect, useState } from 'react'
import { Alert, Box, Button, Link, Skeleton, Typography, useTheme } from '@mui/material'
import { Link as RouterLink, useNavigate, useParams, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { getLegalDocument, type LegalKind, type LegalPublicDocument } from '@/api/legal'
import { currentLanguage, setLanguage } from '@/i18n'
import { useSiteStore } from '@/stores/site'
import BrandLogo from '@/components/BrandLogo'
import LanguageMenu from '@/components/LanguageMenu'
import LegalDocument from '@/components/LegalDocument'

export default function LegalView() {
  const { kind } = useParams()
  const [params, setParams] = useSearchParams()
  const { t, i18n } = useTranslation('auth')
  const navigate = useNavigate()
  const md = useTheme().palette.md
  const siteTitle = useSiteStore(s => s.siteTitle)
  const loadSite = useSiteStore(s => s.load)
  const lang = params.get('lang') || currentLanguage()
  const [state, setState] = useState<'loading' | 'ready' | 'absent' | 'error'>('loading')
  const [document, setDocument] = useState<LegalPublicDocument | null>(null)
  const [retry, setRetry] = useState(0)
  useEffect(() => { void loadSite() }, [loadSite])
  useEffect(() => {
    if (kind !== 'terms' && kind !== 'privacy') { setState('absent'); return }
    const controller = new AbortController()
    setDocument(null)
    setState('loading')
    getLegalDocument(kind, lang, controller.signal).then(doc => {
      if (!controller.signal.aborted) { setDocument(doc); setState('ready') }
    }).catch((error: { response?: { status?: number } }) => {
      if (!controller.signal.aborted) setState(error.response?.status === 404 ? 'absent' : 'error')
    })
    return () => controller.abort()
  }, [kind, lang, retry])
  const back = () => typeof window.history.state?.idx === 'number' && window.history.state.idx > 0 ? navigate(-1) : navigate('/login')
  const other: LegalKind = kind === 'terms' ? 'privacy' : 'terms'
  const title = t(kind === 'terms' ? 'legal.terms' : 'legal.privacy')
  return <Box sx={{ minHeight: '100dvh', bgcolor: md.surface, color: md.onSurface, px: 2, pb: 4 }}>
    <Box component="header" sx={{ maxWidth: 960, mx: 'auto', display: 'flex', alignItems: 'center', gap: 1.5, py: 2 }}>
      <BrandLogo height={32} /><Typography sx={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{siteTitle}</Typography>
      <Box sx={{ '& button': { minWidth: 44, minHeight: 44 } }}><LanguageMenu value={lang} onChange={next => {
        void setLanguage(next)
        setParams(previous => { const search = new URLSearchParams(previous); search.set('lang', next); return search })
      }} /></Box>
    </Box>
    <Box component="main" sx={{ maxWidth: 720, mx: 'auto', pt: { xs: 2, sm: 4 } }}>
      {state === 'loading' ? <Box role="status" aria-label={t('legal.loading')}>
        <Skeleton variant="text" width="50%" height={48} />
        {Array.from({ length: 6 }, (_, i) => <Skeleton key={i} variant="text" height={30} width={i === 5 ? '70%' : '100%'} />)}
      </Box> : state === 'absent' ? <Box sx={{ p: 3, textAlign: 'center', bgcolor: md.surfaceContainerLow, borderRadius: 3 }}>
        <Typography component="h1" variant="h6">{t('legal.unavailable')}</Typography>
        <Button onClick={back} sx={{ mt: 2, minHeight: 44 }}>{t('legal.back')}</Button>
      </Box> : state === 'error' ? <Alert severity="error" action={<Button onClick={() => setRetry(x => x + 1)} sx={{ minHeight: 44 }}>{t('legal.retry')}</Button>}>{t('legal.load_failed')}</Alert> : document && <>
        <Typography component="h1" sx={{ fontSize: { xs: 24, sm: 28 }, fontWeight: 600 }}>{title}</Typography>
        <Typography variant="caption" component="p" sx={{ color: md.onSurfaceVariant, mt: 1 }}>{t('legal.version_date', { version: document.version, date: new Date(document.published_at).toLocaleDateString(i18n.language) })}</Typography>
        {document.fallback_from && <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 1 }}>{t('legal.fallback', { lang: document.locale })}</Typography>}
        <LegalDocument document={document} />
        <Box component="footer" sx={{ display: 'flex', flexWrap: 'wrap', gap: 2, alignItems: 'center', borderTop: `1px solid ${md.outlineVariant}`, mt: 4, pt: 2 }}>
          <Button onClick={back} sx={{ minHeight: 44 }}>{t('legal.back')}</Button>
          <Link component={RouterLink} to={`/legal/${other}?lang=${encodeURIComponent(lang)}`} sx={{ minHeight: 44, display: 'inline-flex', alignItems: 'center' }}>{t(`legal.${other}`)}</Link>
        </Box>
      </>}
    </Box>
  </Box>
}
