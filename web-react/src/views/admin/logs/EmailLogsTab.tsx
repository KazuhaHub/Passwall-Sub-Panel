import { useMemo } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { clearEmailLogs, purgeEmailLogs, type EmailLog, type EmailLogFilter } from '@/api/emailLogs'
import { emailLogQuery } from '@/query/logs'
import { useQueryScope } from '@/query/useQueryScope'
import { LogList } from './LogList'
import { MaintenanceMenu } from './MaintenanceMenu'
import { RetentionPopover } from './RetentionPopover'
import { DetailList, DetailRow, FIT, RecordCard, TimeCell, plainColumns, useAdminT } from './shared'
import { useLogView } from './useLogView'

const who = (r: EmailLog) => r.user_upn || `#${r.user_id}`

/** The notifications the mailer actually sent: confirms a renewal or expiry notice went out. */
export default function EmailLogsTab() {
  const t = useAdminT()
  const scope = useQueryScope()
  const view = useLogView()
  const filter = useMemo<EmailLogFilter>(() => ({ page: view.page, page_size: view.pageSize, search: view.q || undefined }), [view.page, view.pageSize, view.q])
  const query = useQuery({ ...emailLogQuery(scope, filter), placeholderData: keepPreviousData })

  const columns = useMemo<ColumnDef<EmailLog, any>[]>(() => plainColumns<EmailLog>([
    { id: 'at', header: t('logs.email_table.at'), meta: { label: t('logs.email_table.at'), hideable: false, className: 'w-44' },
      cell: c => <TimeCell value={c.row.original.sent_at} /> },
    { id: 'user', header: t('logs.email_table.user'), meta: { label: t('logs.email_table.user'), className: `${FIT} w-[24%] font-medium` },
      cell: c => <span title={who(c.row.original)}>{who(c.row.original)}</span> },
    { id: 'to', header: t('logs.email_table.to'), meta: { label: t('logs.email_table.to'), className: `${FIT} w-[34%]` },
      cell: c => <span title={c.row.original.to_email}>{c.row.original.to_email}</span> },
    { id: 'kind', header: t('logs.email_table.kind'), meta: { label: t('logs.email_table.kind'), className: 'whitespace-nowrap' },
      cell: c => <span title={c.row.original.kind} className="inline-block max-w-56 truncate align-bottom">{c.row.original.kind}</span> },
    { id: 'id', header: t('logs.email_table.id'), meta: { label: t('logs.email_table.id'), defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: c => String(c.row.original.id) },
  ]), [t])

  return (
    <LogList<EmailLog>
      tableId="admin.logs.email"
      label={t('logs.tab_email')}
      view={view}
      query={query}
      columns={columns}
      getId={r => r.id}
      searchPlaceholder={t('logs.filter.email_search')}
      onClearAll={() => view.setQ('')}
      actions={<>
        <RetentionPopover field="mail_sent_retention_days" label={t('logs.retention_label_email')} />
        <MaintenanceMenu kind="email" clear={clearEmailLogs} purge={purgeEmailLogs} onDone={() => query.refetch()} />
      </>}
      emptyTitle={t('logs.empty')}
      emptyDescription={t('logs.empty_email')}
      card={r => <RecordCard title={who(r)} lines={[r.to_email, r.kind, <TimeCell key="t" value={r.sent_at} />]} />}
      detail={r => ({
        title: `${t('logs.tab_email')} #${r.id}`,
        body: (
          <DetailList>
            <DetailRow label={t('logs.email_table.user')}>{who(r)}</DetailRow>
            <DetailRow label={t('logs.email_table.to')} mono>{r.to_email}</DetailRow>
            <DetailRow label={t('logs.email_table.kind')}>{r.kind}</DetailRow>
            <DetailRow label={t('logs.email_table.window')} mono>{r.window_key || '—'}</DetailRow>
            <DetailRow label={t('logs.email_table.at')}><TimeCell value={r.sent_at} /></DetailRow>
          </DetailList>
        ),
      })}
    />
  )
}
