import { useMemo } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import type { SubLog, SubLogFilter } from '@/api/subLogs'
import { clearSubLogs, purgeSubLogs } from '@/api/subLogs'
import { subLogsQuery } from '@/query/logs'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { formatRegion } from '@/utils/geo'
import { LogList } from './LogList'
import { MaintenanceMenu } from './MaintenanceMenu'
import { RetentionPopover } from './RetentionPopover'
import { CodeSection, DetailList, DetailRow, FIT, FilterChip, IpCell, RecordCard, TimeCell, plainColumns, useAdminT } from './shared'
import type { UserFilter } from './userFilter'
import { useLogView } from './useLogView'

const who = (r: SubLog) => r.user_upn || `#${r.user_id}`
const device = (r: SubLog) => r.device_label || (r.device_id4 ? `#${r.device_id4}` : '')

/** Who fetched which subscription, from where, with what client. */
export default function SubLogsTab({ user }: { user: UserFilter }) {
  const t = useAdminT()
  const admin = useCan('config.write')
  const scope = useQueryScope()
  const view = useLogView()
  const filter = useMemo<SubLogFilter>(() => ({
    page: view.page, page_size: view.pageSize, search: view.q || undefined, user_id: user.id ?? undefined,
  }), [view.page, view.pageSize, view.q, user.id])
  const query = useQuery({ ...subLogsQuery(scope, filter), placeholderData: keepPreviousData })

  const columns = useMemo<ColumnDef<SubLog, any>[]>(() => plainColumns<SubLog>([
    { id: 'at', header: t('logs.sub_table.at'), meta: { label: t('logs.sub_table.at'), hideable: false, className: 'w-44' },
      cell: c => <TimeCell value={c.row.original.accessed_at} /> },
    { id: 'user', header: t('logs.sub_table.user'), meta: { label: t('logs.sub_table.user'), className: `${FIT} w-[18%] font-medium` },
      cell: c => <span title={who(c.row.original)}>{who(c.row.original)}</span> },
    { id: 'ip', header: t('logs.sub_table.ip'), meta: { label: t('logs.sub_table.ip'), className: `${FIT} w-[18%]` },
      cell: c => <IpCell ip={c.row.original.ip} region={c.row.original.region} /> },
    { id: 'client', header: t('logs.sub_table.client_type'), meta: { label: t('logs.sub_table.client_type'), className: 'w-28' },
      cell: c => c.row.original.client_type },
    { id: 'ua', header: t('logs.sub_table.ua'), meta: { label: t('logs.sub_table.ua'), className: `${FIT} w-[30%] text-muted-foreground` },
      cell: c => <span title={c.row.original.ua}>{c.row.original.ua}</span> },
    // Admin only, as the API is: the server adds the device fields for admins alone, so an operator
    // offered the column would see it always empty.
    ...(admin ? [{ id: 'device', header: () => <span title={t('logs.sub_table.device_hint')}>{t('logs.sub_table.device')}</span>,
      meta: { label: t('logs.sub_table.device'), className: `${FIT} w-[14%] text-muted-foreground` },
      // The label, else the digest's 4-character prefix; a fetch that declared nothing says so rather
      // than leaving a blank that reads as "not loaded".
      cell: (c: { row: { original: SubLog } }) => device(c.row.original) || '—' } as ColumnDef<SubLog, any>] : []),
    { id: 'id', header: t('logs.sub_table.id'), meta: { label: t('logs.sub_table.id'), defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: c => String(c.row.original.id) },
  ]), [t, admin])

  return (
    <LogList<SubLog>
      label={t('logs.tab_sub')}
      view={view}
      query={query}
      columns={columns}
      getId={r => r.id}
      searchPlaceholder={t('logs.filter.sub_search')}
      chips={user.id !== null && <FilterChip label={t('logs.filter.user_chip', { upn: user.upn || `#${user.id}` })} removeLabel={t('logs.filter.user_remove')} onRemove={user.clear} />}
      narrowed={user.id !== null}
      onClearAll={() => { view.setQ(''); user.clear() }}
      actions={<>
        <RetentionPopover field="sub_log_retention_days" label={t('logs.retention_label')} />
        <MaintenanceMenu kind="sub" clear={clearSubLogs} purge={purgeSubLogs} onDone={() => query.refetch()} />
      </>}
      emptyTitle={t('logs.empty')}
      emptyDescription={t('logs.empty_sub')}
      card={r => <RecordCard title={who(r)} status={<span className="shrink-0 text-xs">{r.client_type}</span>}
        lines={[<TimeCell key="t" value={r.accessed_at} />, r.ip, formatRegion(r.region)]} />}
      detail={r => ({
        title: `${t('logs.tab_sub')} #${r.id}`,
        body: (
          <>
            <DetailList>
              <DetailRow label={t('logs.sub_table.user')}>{who(r)}</DetailRow>
              <DetailRow label={t('logs.sub_table.at')}><TimeCell value={r.accessed_at} /></DetailRow>
              <DetailRow label="IP" mono>{r.ip}</DetailRow>
              <DetailRow label={t('logs.detail.region')} hidden={!formatRegion(r.region)}>{formatRegion(r.region)}</DetailRow>
              <DetailRow label={t('logs.sub_table.client_type')}>{r.client_type}</DetailRow>
              <DetailRow label={t('logs.sub_table.device')} hidden={!admin}>{device(r) || '—'}</DetailRow>
            </DetailList>
            <CodeSection title={t('logs.sub_table.ua')} text={r.ua || '—'} name={t('logs.sub_table.ua')} />
          </>
        ),
      })}
    />
  )
}
