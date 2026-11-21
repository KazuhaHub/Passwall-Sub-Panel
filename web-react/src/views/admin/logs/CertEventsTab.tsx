import { useMemo } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import type { CertEvent } from '@/api/certs'
import { StatusBadge } from '@/components/common/StatusBadge'
import { certEventsQuery } from '@/query/certs'
import { useQueryScope } from '@/query/useQueryScope'
import { LogList, type ListQuery } from './LogList'
import { CodeSection, DetailList, DetailRow, FIT, RecordCard, TimeCell, plainColumns, useAdminT } from './shared'
import { useLogView } from './useLogView'

function ResultBadge({ ok }: { ok: boolean }) {
  const t = useAdminT()
  return <StatusBadge tone={ok ? 'success' : 'danger'} label={t(ok ? 'logs.cert_result.ok' : 'logs.cert_result.fail')} />
}

/**
 * The Logs page's "Certificates" tab: the issuance and renewal activity log. Deploy is not shown here,
 * it is a node sync task and lives with the Sync tasks.
 */
export default function CertEventsTab() {
  const t = useAdminT()
  const scope = useQueryScope()
  const view = useLogView()
  const events = useQuery({ ...certEventsQuery(scope, view.page, view.pageSize), placeholderData: keepPreviousData })
  // The certificate API names its rows `events`; the list takes the shape every log has.
  const query: ListQuery<CertEvent> = {
    data: events.data && { items: events.data.events, total: events.data.total },
    isPending: events.isPending, isError: events.isError, error: events.error, isFetching: events.isFetching,
    refetch: () => events.refetch(),
  }
  const kindLabel = (k: string) => t(`logs.cert_kind.${k}`, { defaultValue: k })

  const columns = useMemo<ColumnDef<CertEvent, any>[]>(() => plainColumns<CertEvent>([
    { id: 'at', header: t('logs.cert_table.at'), meta: { label: t('logs.cert_table.at'), hideable: false, className: 'w-44' },
      cell: c => <TimeCell value={c.row.original.created_at} /> },
    { id: 'cert', header: t('logs.cert_table.cert'), meta: { label: t('logs.cert_table.cert'), className: `${FIT} w-[24%] font-medium` },
      cell: c => <span title={c.row.original.cert_name}>{c.row.original.cert_name || `#${c.row.original.cert_id}`}</span> },
    { id: 'kind', header: t('logs.cert_table.kind'), meta: { label: t('logs.cert_table.kind'), className: 'w-24' },
      cell: c => t(`logs.cert_kind.${c.row.original.kind}`, { defaultValue: c.row.original.kind }) },
    { id: 'result', header: t('logs.cert_table.result'), meta: { label: t('logs.cert_table.result'), className: 'w-28' },
      cell: c => <ResultBadge ok={c.row.original.success} /> },
    { id: 'message', header: t('logs.cert_table.message'), meta: { label: t('logs.cert_table.message'), className: `${FIT} w-[40%] font-mono text-xs text-muted-foreground` },
      cell: c => <span title={c.row.original.message}>{c.row.original.message || '—'}</span> },
  ]), [t])

  return (
    <LogList<CertEvent>
      label={t('logs.tab_certs')}
      view={view}
      query={query}
      columns={columns}
      getId={r => r.id}
      onClearAll={() => undefined}
      emptyTitle={t('logs.empty')}
      emptyDescription={t('logs.empty_certs')}
      card={r => <RecordCard title={r.cert_name || `#${r.cert_id}`} status={<ResultBadge ok={r.success} />}
        lines={[kindLabel(r.kind), <TimeCell key="t" value={r.created_at} />, r.message]} />}
      detail={r => ({
        title: `${t('logs.tab_certs')} #${r.id}`,
        status: <ResultBadge ok={r.success} />,
        body: (
          <>
            <DetailList>
              <DetailRow label={t('logs.cert_table.cert')}>{r.cert_name || `#${r.cert_id}`}</DetailRow>
              <DetailRow label={t('logs.cert_table.kind')}>{kindLabel(r.kind)}</DetailRow>
              <DetailRow label={t('logs.cert_table.at')}><TimeCell value={r.created_at} /></DetailRow>
            </DetailList>
            {r.message && <CodeSection title={t('logs.cert_table.message')} text={r.message} name={t('logs.cert_table.message')} />}
          </>
        ),
      })}
    />
  )
}
