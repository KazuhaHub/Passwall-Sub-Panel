import { useState } from 'react'
import { Link } from 'react-router'
import { isAxiosError } from 'axios'

import type { FlagRecord } from '@/api/riskCenter'
import { IconButton } from '@/components/common/IconButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import { EmptyState } from '@/components/feedback/EmptyState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { IconChevronDown, IconChevronUp } from '@/lib/icons'
import { useFlagRecords } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { FLAG_SOURCES, flagEventKey, flagSourceKey, flagText } from '@/utils/riskCenter'
import { levelTone, RecordDetail } from '../RecordsTab'
import { recordsSearch } from '../riskParams'
import { useRiskT } from '../useRiskT'
import RecentLogins from './RecentLogins'

/** The newest records the drawer lists; the rest are one link away, on the Records page, rather than
 *  paged inside a side panel. */
const RECORDS_SHOWN = 50

/**
 * One record in two lines: when (short, the exact panel time in the title, as the queue's 最近变化 — a
 * dual-timezone string would take the line), which source and what changed; then the reason at the time.
 * Its evidence opens under it, drawn as the Records page draws it.
 */
function Entry({ rec }: { rec: FlagRecord }) {
  const { t, i18n } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const [open, setOpen] = useState(false)
  const hasParams = rec.params !== null && rec.params !== undefined
  const label = open ? t('admin:risk_center.flags.evidence_hide') : t('admin:risk_center.flags.evidence_show')
  return (
    <li data-testid="timeline-entry" className="flex flex-col gap-1 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span data-testid="timeline-time" title={formatMsDualTz(rec.at_ms, panelTz)} className="text-[13px] whitespace-nowrap">
          {rec.at_ms > 0 ? formatRelativeTimeShort(Date.now() - rec.at_ms, t) : '—'}
        </span>
        <Badge variant="outline">{t(flagSourceKey(rec.source), { defaultValue: rec.source })}</Badge>
        <StatusBadge tone={levelTone(rec.level)} label={t(flagEventKey(rec.event), { defaultValue: rec.event })} />
        {hasParams && (
          <IconButton label={label} size="icon-xs" className="ml-auto" aria-expanded={open} onClick={() => setOpen(v => !v)}>
            {open ? <IconChevronUp /> : <IconChevronDown />}
          </IconButton>
        )}
      </div>
      {/* The reason AT THE TIME, from the numbers stored with the record. */}
      <p className="text-[13px] text-muted-foreground">{flagText(rec, t, i18n.language)}</p>
      {open && hasParams && (
        <div className="rounded-md bg-muted/50 px-3 py-1">
          <RecordDetail rec={rec} sentence={false} />
        </div>
      )}
    </li>
  )
}

/**
 * 时间线: the account's flag records, newest first, then its recent panel sign-ins, and an exact link to
 * the whole auth log for it (by id: a search by name would also match other accounts whose names
 * contain it).
 *
 * The records are a list made for a side panel, not the Records page's table: two lines an entry, and
 * one filter, the source. Dates, levels, changes and paging are the Records page's, one link away,
 * filtered to this account. The filter lives as long as the drawer; the page's URL is not its to write.
 */
export default function TimelineTab({ userId, upn }: { userId: number; upn: string }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const [source, setSource] = useState('')
  const { data, isPending, error } = useFlagRecords(scope, {
    page: 1, page_size: RECORDS_SHOWN, user_id: userId, ...(source ? { source } : {}),
  })
  const rows = data?.items ?? []
  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''
  const records = `/admin/risk?${recordsSearch(new URLSearchParams({ tab: 'records' }), { user_id: userId })}`
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="w-44">
          <NativeSelect aria-label={t('admin:risk_center.flags.filter_source')} value={source} onChange={e => setSource(e.target.value)}>
            <option value="">{t('admin:risk_center.live.opt_all')}</option>
            {FLAG_SOURCES.map(s => <option key={s} value={s}>{t(flagSourceKey(s))}</option>)}
          </NativeSelect>
        </div>
        <Button variant="link" size="sm" render={<Link to={records} />}>{t('admin:risk_center.drawer.open_records')}</Button>
      </div>
      <div>
        {isPending && <CardSkeleton lines={3} />}
        {err && <p className="text-[13px] text-destructive">{t('admin:risk_center.load_failed', { error: err })}</p>}
        {!isPending && !err && rows.length === 0 && <EmptyState compact title={t('admin:risk_center.flags.empty')} />}
        {rows.length > 0 && <ul className="divide-y">{rows.map(r => <Entry key={r.id} rec={r} />)}</ul>}
        {data && data.total > rows.length && (
          <p className="pt-1 text-xs text-muted-foreground">
            {t('admin:risk_center.drawer.records_capped', { shown: rows.length, total: data.total })}
          </p>
        )}
      </div>
      <div>
        <h3 className="mb-1 text-sm font-medium">{t('admin:risk_center.drawer.logins_title')}</h3>
        <RecentLogins userId={userId} />
      </div>
      <Button variant="link" size="sm" className="self-start"
        render={<Link to={`/admin/logs?tab=auth&user_id=${userId}&upn=${encodeURIComponent(upn)}`} />}>
        {t('admin:risk_center.drawer.open_auth_logs')}
      </Button>
    </div>
  )
}
