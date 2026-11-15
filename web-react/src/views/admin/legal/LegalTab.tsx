import { useState } from 'react'
import { Alert, Box, Button, Card, Chip, FormControlLabel, Skeleton, Switch, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { AdminLegal, LegalKind } from '@/api/legal'
import DataCollectionCard from '@/components/DataCollectionCard'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import LegalEditorDialog from './LegalEditorDialog'
import LegalHistorySheet from './LegalHistorySheet'

interface Props {
  /** The settings draft's switches: this tab shows and edits them, the page's
   *  save bar writes them. Documents are written by the publish dialog alone. */
  enabled: boolean
  onEnabledChange: (v: boolean) => void
  discloseUsage: boolean
  onDiscloseUsageChange: (v: boolean) => void
}

const FALLBACK: Record<string, string> = { 'zh-CN': 'en-US', 'en-US': 'zh-CN' }

/** Settings > Privacy and terms (S21). */
export default function LegalTab({ enabled, onEnabledChange, discloseUsage, onDiscloseUsageChange }: Props) {
  const md = useTheme().palette.md
  const { t } = useTranslation('admin')
  const scope = useQueryScope()
  const terms = useAdminLegal(scope, 'terms')
  const privacy = useAdminLegal(scope, 'privacy')
  const [editing, setEditing] = useState<LegalKind | null>(null)
  const [history, setHistory] = useState<LegalKind | null>(null)

  const failed = (terms.isError || privacy.isError) && !(terms.data && privacy.data)
  const loaded = !!terms.data && !!privacy.data
  const nothingPublished = loaded && terms.data!.documents.length === 0 && privacy.data!.documents.length === 0
  const consentVersion = privacy.data?.consent_version ?? terms.data?.consent_version
  const collection = privacy.data?.data_collection ?? terms.data?.data_collection

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, maxWidth: 880 }}>
      <Card sx={{ p: 2.5, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
        <Typography sx={{ fontWeight: 600, fontSize: 14, mb: 1, color: md.onSurface }}>{t('settings.legal.title')}</Typography>
        <FormControlLabel label={t('settings.legal.enabled')} sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
          control={<Switch checked={enabled} onChange={(_, c) => onEnabledChange(c)} />} />
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('settings.legal.enabled_hint')}</Typography>
        {consentVersion !== undefined && (
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.5 }}>{t('settings.legal.consent_version', { version: consentVersion })}</Typography>
        )}
        <Box sx={{ mt: 1.5 }}>
          <FormControlLabel label={t('settings.legal.disclose_usage')} sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
            control={<Switch checked={discloseUsage} onChange={(_, c) => onDiscloseUsageChange(c)} />} />
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('settings.legal.disclose_usage_hint')}</Typography>
        </Box>
      </Card>

      {failed && <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => { void terms.refetch(); void privacy.refetch() }}>{t('settings.legal.retry')}</Button>}>{t('settings.legal.load_failed')}</Alert>}

      {enabled && nothingPublished && (
        <Alert severity="warning" action={<Button color="inherit" size="small" onClick={() => setEditing('privacy')}>{t('settings.legal.go_edit')}</Button>}>
          {t('settings.legal.empty_alert')}
        </Alert>
      )}

      {!failed && (['terms', 'privacy'] as const).map(kind => (
        <DocumentCard key={kind} kind={kind} data={(kind === 'terms' ? terms : privacy).data}
          onEdit={() => setEditing(kind)} onHistory={() => setHistory(kind)} />
      ))}

      {!failed && (
        <Box>
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{t('settings.legal.collect_note')}</Typography>
          {collection
            ? <DataCollectionCard data={collection} />
            : <Skeleton variant="rounded" height={120} />}
        </Box>
      )}

      {editing && <LegalEditorDialog kind={editing} onClose={() => setEditing(null)} />}
      {history && <LegalHistorySheet kind={history} onClose={() => setHistory(null)} />}
    </Box>
  )
}

function DocumentCard({ kind, data, onEdit, onHistory }: { kind: LegalKind; data: AdminLegal | undefined; onEdit: () => void; onHistory: () => void }) {
  const md = useTheme().palette.md
  const { t, i18n } = useTranslation('admin')
  const name = t(`auth:legal.kind.${kind}`)
  const date = (iso: string) => new Date(iso).toLocaleDateString(i18n.language, { month: 'long', day: 'numeric' })

  const docs = data?.documents ?? []
  const have = new Set(docs.map(d => d.locale))
  const missing = ['zh-CN', 'en-US'].filter(l => !have.has(l))

  return (
    <Card component="section" aria-label={name} sx={{ p: 2.5, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography component="h3" sx={{ flex: 1, fontWeight: 600, fontSize: 14, color: md.onSurface }}>{name}</Typography>
        <Button size="small" onClick={onHistory}>{t('settings.legal.history')}</Button>
        <Button size="small" variant="outlined" onClick={onEdit}>{t('settings.legal.edit')}</Button>
      </Box>
      {!data ? <Skeleton variant="text" width="60%" /> : (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1, mt: 1 }}>
          {docs.length > 0 && <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('settings.legal.published_label')}</Typography>}
          {docs.map(d => (
            <Chip key={d.locale} size="small" label={t('settings.legal.locale_chip', { locale: d.locale, version: d.version, date: date(d.published_at) })} />
          ))}
          {missing.map(l => {
            const fb = FALLBACK[l]
            return (
              <Typography key={l} sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {have.has(fb)
                  ? t('settings.legal.not_published_fallback', { locale: l, fallback: fb })
                  : t('settings.legal.not_published', { locale: l })}
              </Typography>
            )
          })}
        </Box>
      )}
    </Card>
  )
}
