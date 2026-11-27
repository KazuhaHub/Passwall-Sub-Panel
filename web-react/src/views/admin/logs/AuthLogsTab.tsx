import { useMemo } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import type { AuthEvent, AuthEventFilter, AuthMethod, AuthOutcome } from '@/api/authEvents'
import { StatusBadge } from '@/components/common/StatusBadge'
import { useUrlStates } from '@/hooks/useUrlState'
import { authLogQuery } from '@/query/logs'
import { useQueryScope } from '@/query/useQueryScope'
import { formatRegion } from '@/utils/geo'
import { LogList } from './LogList'
import { CodeSection, DetailList, DetailRow, FIT, FilterChip, IpCell, RecordCard, TimeCell, choiceCodec, plainColumns, singleFacet, useAdminT } from './shared'
import type { UserFilter } from './userFilter'
import { useLogView } from './useLogView'

const METHODS = ['local', 'saml', 'oidc'] as const
const OUTCOMES = ['success', 'failure'] as const

/** A success is a quiet dot; a failure is the one thing in the list that is not. */
function OutcomeBadge({ outcome }: { outcome: AuthOutcome }) {
  const t = useAdminT()
  return <StatusBadge tone={outcome === 'success' ? 'success' : 'danger'} label={t(`logs.auth_outcome.${outcome}`)} />
}

/** Sign-ins across local, SAML and OIDC, successes and failures. */
export default function AuthLogsTab({ user }: { user: UserFilter }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const view = useLogView()
  // The two facets are in the URL with the keyword, so a filtered list is a link.
  const [f, setF] = useUrlStates({ method: choiceCodec(METHODS), outcome: choiceCodec(OUTCOMES) })
  const filter = useMemo<AuthEventFilter>(() => ({
    page: view.page, page_size: view.pageSize, search: view.q || undefined,
    method: f.method || undefined, outcome: f.outcome || undefined, user_id: user.id ?? undefined,
  }), [view.page, view.pageSize, view.q, f.method, f.outcome, user.id])
  const query = useQuery({ ...authLogQuery(scope, filter), placeholderData: keepPreviousData })

  const columns = useMemo<ColumnDef<AuthEvent, any>[]>(() => plainColumns<AuthEvent>([
    { id: 'at', header: t('logs.auth_table.at'), meta: { label: t('logs.auth_table.at'), hideable: false, className: 'w-44' },
      cell: c => <TimeCell value={c.row.original.at} /> },
    { id: 'upn', header: t('logs.auth_table.upn'), meta: { label: t('logs.auth_table.upn'), className: `${FIT} w-[22%] font-medium` },
      cell: c => <span title={c.row.original.ua || ''}>{c.row.original.upn || '—'}</span> },
    { id: 'method', header: t('logs.auth_table.method'), meta: { label: t('logs.auth_table.method'), className: 'w-24 uppercase' },
      cell: c => c.row.original.method },
    { id: 'outcome', header: t('logs.auth_table.outcome'), meta: { label: t('logs.auth_table.outcome'), className: 'w-28' },
      cell: c => <OutcomeBadge outcome={c.row.original.outcome} /> },
    { id: 'ip', header: t('logs.auth_table.ip'), meta: { label: t('logs.auth_table.ip'), className: 'whitespace-nowrap' },
      cell: c => <IpCell ip={c.row.original.ip} region={c.row.original.region} /> },
    { id: 'reason', header: t('logs.auth_table.reason'), meta: { label: t('logs.auth_table.reason'), className: `${FIT} w-[24%] text-muted-foreground` },
      cell: c => <span title={c.row.original.reason || ''}>{c.row.original.reason || '—'}</span> },
  ]), [t])

  const method = singleFacet<AuthMethod>(f.method, v => { setF({ method: v }); view.setUrl({ page: 1 }) })
  const outcome = singleFacet<AuthOutcome>(f.outcome, v => { setF({ outcome: v }); view.setUrl({ page: 1 }) })

  return (
    <LogList<AuthEvent>
      tableId="admin.logs.auth"
      label={t('logs.tab_auth')}
      view={view}
      query={query}
      columns={columns}
      getId={r => r.id}
      searchPlaceholder={t('logs.filter.auth_search')}
      facets={[
        { id: 'method', label: t('logs.auth_table.method'), options: METHODS.map(m => ({ value: m, label: t(`logs.auth_method.${m}`) })), ...method },
        { id: 'outcome', label: t('logs.auth_table.outcome'), options: OUTCOMES.map(o => ({ value: o, label: t(`logs.auth_outcome.${o}`) })), ...outcome },
      ]}
      chips={user.id !== null && <FilterChip label={t('logs.filter.user_chip', { upn: user.upn || `#${user.id}` })} removeLabel={t('logs.filter.user_remove')} onRemove={user.clear} />}
      narrowed={user.id !== null || f.method !== '' || f.outcome !== ''}
      onClearAll={() => { view.setUrl({ q: '', page: 1 }); setF({ method: '', outcome: '' }); user.clear() }}
      emptyTitle={t('logs.empty')}
      emptyDescription={t('logs.empty_auth')}
      card={r => <RecordCard title={r.upn || '—'} status={<OutcomeBadge outcome={r.outcome} />}
        lines={[<TimeCell key="t" value={r.at} />, `${r.method.toUpperCase()} · ${r.ip}`, r.reason]} />}
      detail={r => ({
        title: `${t('logs.tab_auth')} #${r.id}`,
        status: <OutcomeBadge outcome={r.outcome} />,
        body: (
          <>
            <DetailList>
              <DetailRow label={t('logs.auth_table.upn')}>{r.upn || '—'}</DetailRow>
              <DetailRow label={t('logs.auth_table.at')}><TimeCell value={r.at} /></DetailRow>
              <DetailRow label={t('logs.auth_table.method')}>{t(`logs.auth_method.${r.method}`)}</DetailRow>
              <DetailRow label={t('logs.auth_table.reason')} hidden={!r.reason}>{r.reason}</DetailRow>
              <DetailRow label="IP" mono>{r.ip}</DetailRow>
              <DetailRow label={t('logs.detail.region')} hidden={!formatRegion(r.region)}>{formatRegion(r.region)}</DetailRow>
            </DetailList>
            {r.ua && <CodeSection title={t('logs.sub_table.ua')} text={r.ua} name={t('logs.sub_table.ua')} />}
          </>
        ),
      })}
    />
  )
}
