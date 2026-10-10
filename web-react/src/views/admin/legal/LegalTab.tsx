import { useEffect, useState } from 'react'
import { Alert, Box, Button, Card, Chip, FormControlLabel, Skeleton, Stack, Switch, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getLegalCollection, getLegalLatest, type DataCollection, type LegalAdminDocument, type LegalKind, type LegalPublication } from '@/api/legal'
import DataCollectionCard from '@/components/DataCollectionCard'
import { SUPPORTED_LANGUAGES, isBuiltinLanguage } from '@/i18n'
import LegalEditorDialog from './LegalEditorDialog'
import LegalHistorySheet from './LegalHistorySheet'

export default function LegalTab({ enabled, consentVersion, onEnabled, onPublished }: {
  enabled: boolean; consentVersion: number; onEnabled: (value: boolean) => void; onPublished: (result: LegalPublication) => void
}) {
  const { t } = useTranslation('admin')
  const md = useTheme().palette.md
  const languages = ['zh-CN', 'en-US', ...SUPPORTED_LANGUAGES.filter(lang => !isBuiltinLanguage(lang))]
  const languagesKey = languages.join(',')
  const [documents, setDocuments] = useState<Record<string, LegalAdminDocument | null>>({})
  const [collection, setCollection] = useState<DataCollection | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [collectionFailed, setCollectionFailed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [collectionRetry, setCollectionRetry] = useState(0)
  const [editor, setEditor] = useState<LegalKind | null>(null)
  const [history, setHistory] = useState<LegalKind | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setFailed(false)
    const identities = (['terms', 'privacy'] as const).flatMap(kind => languagesKey.split(',').map(locale => ({ kind, locale })))
    void Promise.all(identities.map(async ({ kind, locale }) => [`${kind}:${locale}`, await getLegalLatest(kind, locale, controller.signal)] as const)).then(entries => {
      if (!controller.signal.aborted) setDocuments(Object.fromEntries(entries))
    }).catch(() => { if (!controller.signal.aborted) setFailed(true) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [languagesKey, retry])
  useEffect(() => {
    const controller = new AbortController()
    setCollectionFailed(false)
    void getLegalCollection(controller.signal).then(data => { if (!controller.signal.aborted) setCollection(data) }).catch(() => { if (!controller.signal.aborted) { setCollection(null); setCollectionFailed(true) } })
    return () => controller.abort()
  }, [collectionRetry])
  function published(result: LegalPublication) {
    setDocuments(old => ({ ...old, [`${result.document.kind}:${result.document.locale}`]: result.document }))
    onPublished(result)
  }
  // The global version also covers documents in a language whose uploaded
  // interface pack has since been removed from the editor's option list.
  const anyPublished = consentVersion > 0 || Object.values(documents).some(Boolean)
  return <Stack spacing={2}>
    <Card sx={{ p: { xs: 2, sm: 3 }, bgcolor: md.surfaceContainerLow }}>
      <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', gap: 1 }}>
        <Typography component="h2" variant="h6">{t('legal.title')}</Typography>
        <FormControlLabel label={t('legal.enabled')} control={<Switch checked={enabled} onChange={event => onEnabled(event.target.checked)} />} />
      </Stack>
      <Typography variant="body2" color="text.secondary" sx={{ my: 1 }}>{t('legal.enabled_hint')}</Typography>
      <Typography variant="caption">{t('legal.consent_version', { version: consentVersion })}</Typography>
      {enabled && !loading && !failed && !anyPublished && <Alert severity="warning" sx={{ mt: 2 }} action={<Button onClick={() => setEditor('terms')}>{t('legal.go_edit')}</Button>}>{t('legal.enabled_empty')}</Alert>}
    </Card>
    {failed && <Alert severity="error" action={<Button onClick={() => setRetry(value => value + 1)}>{t('legal.retry')}</Button>}>{t('legal.load_failed')}</Alert>}
    {(['terms', 'privacy'] as const).map(kind => <Card key={kind} sx={{ p: { xs: 2, sm: 3 }, bgcolor: md.surfaceContainerLow }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} sx={{ justifyContent: 'space-between', gap: 1 }}>
        <Typography component="h2" variant="h6">{t(`legal.${kind}`)}</Typography>
        <Stack direction="row" spacing={1}><Button onClick={() => setHistory(kind)} sx={{ minHeight: 44 }}>{t('legal.history')}</Button><Button variant="outlined" onClick={() => setEditor(kind)} sx={{ minHeight: 44 }}>{t('legal.edit')}</Button></Stack>
      </Stack>
      <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', mt: 1 }}>
        {loading ? <Skeleton width="100%" height={40} /> : !failed && languages.map(locale => {
          const doc = documents[`${kind}:${locale}`]
          const fallback = (locale === 'zh-TW' ? ['zh-CN', 'en-US'] : ['en-US', 'zh-CN']).find(lang => documents[`${kind}:${lang}`])
          return doc ? <Chip key={locale} label={`${locale} v${doc.version} · ${new Date(doc.published_at).toLocaleDateString()}`} /> : <Typography key={locale} variant="body2" color="text.secondary">{fallback ? t('legal.unpublished_fallback', { locale, fallback }) : t('legal.locale_unpublished', { locale })}</Typography>
        })}
      </Stack>
    </Card>)}
    {collection ? <DataCollectionCard data={collection} /> : collectionFailed ? <Alert severity="error" action={<Button onClick={() => setCollectionRetry(value => value + 1)}>{t('legal.retry')}</Button>}>{t('legal.collection_failed')}</Alert> : <Box role="status" aria-label={t('legal.loading')}><Skeleton height={200} /></Box>}
    {editor && <LegalEditorDialog kind={editor} languages={languages} collection={collection} onCollectionRetry={() => setCollectionRetry(value => value + 1)} onClose={() => setEditor(null)} onPublished={published} />}
    {history && <LegalHistorySheet kind={history} collection={collection} onClose={() => setHistory(null)} />}
  </Stack>
}
