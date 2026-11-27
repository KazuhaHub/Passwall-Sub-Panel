import { useMemo } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { clearAudit, type AuditEntry, type AuditFilter } from '@/api/audit'
import { auditLogQuery } from '@/query/logs'
import { useQueryScope } from '@/query/useQueryScope'
import { formatRegion } from '@/utils/geo'
import { LogList } from './LogList'
import { MaintenanceMenu } from './MaintenanceMenu'
import { CodeSection, DetailList, DetailRow, FIT, IpCell, RecordCard, TimeCell, formatJson, plainColumns, useAdminT } from './shared'
import { useLogView } from './useLogView'

/** What an administrator or operator changed: who, what, on which object, from where. */
export default function AuditLogsTab() {
  const t = useAdminT()
  const scope = useQueryScope()
  const view = useLogView()
  const filter = useMemo<AuditFilter>(() => ({ page: view.page, page_size: view.pageSize, search: view.q || undefined }), [view.page, view.pageSize, view.q])
  const query = useQuery({ ...auditLogQuery(scope, filter), placeholderData: keepPreviousData })

  const columns = useMemo<ColumnDef<AuditEntry, any>[]>(() => plainColumns<AuditEntry>([
    { id: 'at', header: t('logs.audit_table.at'), meta: { label: t('logs.audit_table.at'), hideable: false, className: 'w-44' },
      cell: c => <TimeCell value={c.row.original.at} /> },
    { id: 'actor', header: t('logs.audit_table.actor'), meta: { label: t('logs.audit_table.actor'), className: `${FIT} w-[16%] font-medium` },
      cell: c => <span title={c.row.original.actor}>{c.row.original.actor}</span> },
    { id: 'action', header: t('logs.audit_table.action'), meta: { label: t('logs.audit_table.action'), className: 'font-mono text-[0.8125rem] whitespace-nowrap' },
      cell: c => <span title={c.row.original.action}>{c.row.original.action}</span> },
    { id: 'target', header: t('logs.audit_table.target'), meta: { label: t('logs.audit_table.target'), className: `${FIT} w-[26%]` },
      cell: c => <span title={c.row.original.target}>{c.row.original.target}</span> },
    { id: 'ip', header: t('logs.audit_table.ip'), meta: { label: t('logs.audit_table.ip'), className: 'whitespace-nowrap' },
      cell: c => <IpCell ip={c.row.original.ip} region={c.row.original.region} /> },
    { id: 'id', header: t('logs.audit_table.id'), meta: { label: t('logs.audit_table.id'), defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: c => String(c.row.original.id) },
  ]), [t])

  return (
    <LogList<AuditEntry>
      tableId="admin.logs.audit"
      label={t('logs.tab_audit')}
      view={view}
      query={query}
      columns={columns}
      getId={r => r.id}
      searchPlaceholder={t('logs.filter.audit_search')}
      onClearAll={() => view.setQ('')}
      actions={<MaintenanceMenu kind="audit" clear={clearAudit} onDone={() => query.refetch()} />}
      emptyTitle={t('logs.empty')}
      emptyDescription={t('logs.empty_audit')}
      card={r => <RecordCard title={r.action} lines={[`${r.actor} · ${r.target}`, <TimeCell key="t" value={r.at} />]} />}
      detail={r => ({
        title: `${t('logs.tab_audit')} #${r.id}`,
        body: (
          <>
            <DetailList>
              <DetailRow label={t('logs.audit_table.actor')}>{r.actor}</DetailRow>
              <DetailRow label={t('logs.audit_table.action')} mono>{r.action}</DetailRow>
              <DetailRow label={t('logs.audit_table.target')}>{r.target || '—'}</DetailRow>
              <DetailRow label={t('logs.audit_table.at')}><TimeCell value={r.at} /></DetailRow>
              <DetailRow label="IP" mono>{r.ip}</DetailRow>
              <DetailRow label={t('logs.detail.region')} hidden={!formatRegion(r.region)}>{formatRegion(r.region)}</DetailRow>
            </DetailList>
            {r.before_json && <CodeSection title={t('logs.detail.before')} text={formatJson(r.before_json)} name={t('logs.detail.before')} />}
            {r.after_json && <CodeSection title={t('logs.detail.after')} text={formatJson(r.after_json)} name={t('logs.detail.after')} />}
          </>
        ),
      })}
    />
  )
}
