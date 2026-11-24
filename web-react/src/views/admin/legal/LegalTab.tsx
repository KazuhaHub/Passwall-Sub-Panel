import { useState } from 'react'

import type { AdminLegal, LegalKind } from '@/api/legal'
import { StatusBadge } from '@/components/common/StatusBadge'
import DataCollectionCard from '@/components/DataCollectionCard'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useTranslation } from 'react-i18next'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import { Notice } from '@/components/common/Notice'
import { useAdminT } from '../useAdminT'
import LegalEditorSheet from './LegalEditorSheet'
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
  const t = useAdminT()
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
    <div className="flex max-w-3xl flex-col gap-4">
      <Card>
        <CardHeader><CardTitle>{t('admin:settings.legal.title')}</CardTitle></CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div>
            <Field orientation="horizontal" label={t('admin:settings.legal.enabled')} hint={t('admin:settings.legal.enabled_hint')}>
              <Switch checked={enabled} onCheckedChange={c => onEnabledChange(c)} />
            </Field>
            {consentVersion !== undefined && (
              <p className="mt-1 text-xs text-muted-foreground">{t('admin:settings.legal.consent_version', { version: consentVersion })}</p>
            )}
          </div>
          <Field orientation="horizontal" label={t('admin:settings.legal.disclose_usage')} hint={t('admin:settings.legal.disclose_usage_hint')}>
            <Switch checked={discloseUsage} onCheckedChange={c => onDiscloseUsageChange(c)} />
          </Field>
        </CardContent>
      </Card>

      {failed && (
        <ErrorState compact title={t('admin:settings.legal.load_failed')} retry={() => Promise.all([terms.refetch(), privacy.refetch()])} />
      )}

      {enabled && nothingPublished && (
        <Notice action={<Button variant="outline" size="sm" onClick={() => setEditing('privacy')}>{t('admin:settings.legal.go_edit')}</Button>}>
          {t('admin:settings.legal.empty_alert')}
        </Notice>
      )}

      {!failed && (['terms', 'privacy'] as const).map(kind => (
        <DocumentCard key={kind} kind={kind} data={(kind === 'terms' ? terms : privacy).data}
          onEdit={() => setEditing(kind)} onHistory={() => setHistory(kind)} />
      ))}

      {!failed && (
        <div>
          <p className="mb-1 text-xs text-muted-foreground">{t('admin:settings.legal.collect_note')}</p>
          {collection ? <DataCollectionCard data={collection} /> : <Skeleton className="h-28 rounded-xl" />}
        </div>
      )}

      {editing && <LegalEditorSheet kind={editing} discloseUsage={discloseUsage} onClose={() => setEditing(null)} />}
      {history && <LegalHistorySheet kind={history} onClose={() => setHistory(null)} />}
    </div>
  )
}

function DocumentCard({ kind, data, onEdit, onHistory }: { kind: LegalKind; data: AdminLegal | undefined; onEdit: () => void; onHistory: () => void }) {
  const t = useAdminT()
  const { i18n } = useTranslation()
  const name = t(`auth:legal.kind.${kind}`)
  const date = (iso: string) => new Date(iso).toLocaleDateString(i18n.language, { month: 'long', day: 'numeric' })

  const docs = data?.documents ?? []
  const have = new Set(docs.map(d => d.locale))
  const missing = ['zh-CN', 'en-US'].filter(l => !have.has(l))

  return (
    <section aria-label={name}>
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center gap-2">
        <h3 className="flex-1 text-base leading-snug font-medium">{name}</h3>
        <Button variant="outline" size="sm" onClick={onHistory}>{t('admin:settings.legal.history')}</Button>
        <Button variant="outline" size="sm" onClick={onEdit}>{t('admin:settings.legal.edit')}</Button>
      </CardHeader>
      <CardContent>
        {!data ? <Skeleton className="h-5 w-3/5" /> : (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
            {docs.length > 0 && <span className="text-xs text-muted-foreground">{t('admin:settings.legal.published_label')}</span>}
            {docs.map(d => (
              <StatusBadge key={d.locale} tone="neutral" quiet label={t('admin:settings.legal.locale_chip', { locale: d.locale, version: d.version, date: date(d.published_at) })} />
            ))}
            {missing.map(l => {
              const fb = FALLBACK[l]
              return (
                <span key={l} className="text-xs text-muted-foreground">
                  {have.has(fb) ? t('admin:settings.legal.not_published_fallback', { locale: l, fallback: fb }) : t('admin:settings.legal.not_published', { locale: l })}
                </span>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
    </section>
  )
}
