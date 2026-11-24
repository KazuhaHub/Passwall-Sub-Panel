import { StatusBadge } from '@/components/common/StatusBadge'
import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { useTranslation } from 'react-i18next'
import type { LegalKind } from '@/api/legal'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import { useAdminT } from '../useAdminT'

/**
 * Every version of one document, newest first. Text is not shown: a version is immutable and the list is
 * for telling which were material changes (the ones that asked users to accept again).
 */
export default function LegalHistoryDialog({ kind, onClose }: { kind: LegalKind; onClose: () => void }) {
  const t = useAdminT()
  const { i18n } = useTranslation()
  const scope = useQueryScope()
  const q = useAdminLegal(scope, kind)
  const doc = t(`auth:legal.kind.${kind}`)
  const versions = [...(q.data?.versions ?? [])].sort((a, b) => b.published_at.localeCompare(a.published_at) || b.id - a.id)

  return (
    <DetailDialog open size="md" onClose={onClose} title={t('admin:settings.legal.history_sheet.title', { doc })}>
      {q.isLoading && <div className="flex flex-col gap-2">{[0, 1, 2].map(i => <Skeleton key={i} className="h-12 rounded-lg" />)}</div>}
      {q.isError && !q.data && <ErrorState compact title={t('admin:settings.legal.load_failed')} retry={() => q.refetch()} />}
      {q.data && versions.length === 0 && <EmptyState compact title={t('admin:settings.legal.history_sheet.empty')} />}
      <ul className="m-0 list-none p-0">
        {versions.map(v => (
          <li key={v.id} className="flex items-center gap-3 border-t py-2.5 first:border-t-0">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{t('admin:settings.legal.history_sheet.row', { locale: v.locale, version: v.version })}</div>
              <div className="text-xs text-muted-foreground">{new Date(v.published_at).toLocaleString(i18n.language)}</div>
            </div>
            {v.consent_bump && <StatusBadge tone="info" label={t('admin:settings.legal.history_sheet.major')} />}
          </li>
        ))}
      </ul>
    </DetailDialog>
  )
}
