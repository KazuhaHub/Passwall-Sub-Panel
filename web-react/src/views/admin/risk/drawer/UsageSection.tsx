import { useState } from 'react'
import { useNavigate } from 'react-router'

import type { DestUsage, DestUsageNode } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { HelpTip } from '@/components/common/HelpTip'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertAction, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { useDestSettings, useDestUserUsage } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useRiskT } from '../useRiskT'

const D = 'admin:access_control.drawer.'

/** The longest window offered: a week, held to the usage retention. */
const MAX_WINDOW_DAYS = 7

/**
 * One account's per-site usage (S16, tier B). Nothing is read until the
 * administrator asks: every read is a row in the admin audit log, so a page that
 * fetched it on open would leave one for every drawer glanced at. Choosing
 * another range or node is another deliberate read. The window is 24 hours or
 * min(7, retention) days; the retention is a setting and the words interpolate it.
 *
 * `available` is whether any node the account has a client on records usage now:
 * when it is false, "no usage" would say nothing, so the section says that and
 * offers no read.
 */
export default function UsageSection({ userId, available, nodes }: {
  userId: number
  available: boolean | null
  nodes: DestUsageNode[] | null
}) {
  const { t, i18n } = useRiskT()
  const scope = useQueryScope()
  const navigate = useNavigate()
  const settings = useDestSettings(scope)
  const read = useDestUserUsage(scope)
  const [range, setRange] = useState('24h')
  const [panelId, setPanelId] = useState(0)
  const [asked, setAsked] = useState(false)
  const num = new Intl.NumberFormat(i18n.language)

  // An answer the server cannot give (null) draws nothing: there is no claim to make either way.
  if (available === null) return null

  const days = Math.min(MAX_WINDOW_DAYS, settings.data?.effective.dest_usage_retention_days ?? 1)
  const ranges = ['24h', ...(days > 1 ? [`${days}d`] : [])]
  // One click is one read (one audit row): nothing starts while one is in flight.
  const run = async (r: string, p: number) => {
    if (read.isPending) return
    setAsked(true)
    try { await read.mutateAsync({ userId, range: r, panelId: p || undefined }) } catch { /* shown by read.isError */ }
  }
  const requestedDays = range === '24h' ? 1 : parseInt(range, 10)
  const answer: DestUsage | undefined = read.data

  const head = (
    <div className="flex items-center gap-1">
      <h3 className="text-sm font-medium">{t(`${D}usage_title`)}</h3>
      <HelpTip text={t(`${D}usage_help`)} label={t(`${D}usage_help_label`)} />
    </div>
  )

  if (available === false) {
    return (
      <div data-testid="access-usage" className="mt-2 flex flex-col gap-2">
        {head}
        <div className="flex flex-wrap items-center gap-2 rounded-lg bg-muted p-3">
          <p className="min-w-0 flex-1 text-sm">{t(`${D}usage_not_recorded`)}</p>
          <Button variant="outline" size="sm" onClick={() => void navigate('/admin/access-control?sheet=nodes')}>{t(`${D}usage_nodes`)}</Button>
        </div>
      </div>
    )
  }

  return (
    <div data-testid="access-usage" data-user={userId} className="mt-2 flex flex-col gap-2">
      {head}
      <p className="text-sm text-muted-foreground">{t(`${D}usage_audit`)}</p>
      <div className="flex flex-wrap items-center gap-2">
        <SegmentedControl value={range} aria-label={t(`${D}usage_range_label`)} disabled={read.isPending}
          options={ranges.map(r => ({ value: r, label: r === '24h' ? t(`${D}usage_range_24h`) : t(`${D}usage_range_days`, { count: parseInt(r, 10) }) }))}
          onChange={v => { setRange(v); if (asked) void run(v, panelId) }} />
        {(nodes?.length ?? 0) > 1 && (
          <div className="w-44">
            <NativeSelect aria-label={t(`${D}usage_node_label`)} value={panelId} disabled={read.isPending}
              onChange={e => { const p = Number(e.target.value); setPanelId(p); if (asked) void run(range, p) }}>
              <option value={0}>{t(`${D}usage_node_all`)}</option>
              {nodes!.map(n => <option key={n.panel_id} value={n.panel_id}>{n.name}</option>)}
            </NativeSelect>
          </div>
        )}
        <AsyncButton size="sm" variant="outline" onClick={() => run(range, panelId)}>{t(`${D}usage_view`)}</AsyncButton>
      </div>
      {read.isError && (
        <Alert variant="destructive">
          <AlertDescription>{t(`${D}usage_load_failed`)}</AlertDescription>
          <AlertAction><Button variant="outline" size="sm" disabled={read.isPending} onClick={() => void run(range, panelId)}>{t(`${D}retry`)}</Button></AlertAction>
        </Alert>
      )}
      {answer && !read.isError && (
        <div className="flex flex-col gap-1.5">
          {answer.effective_days < requestedDays && (
            <p className="text-sm text-muted-foreground">{t(`${D}usage_window`, { days: answer.effective_days })}</p>
          )}
          {answer.total === 0
            ? <p className="text-sm text-muted-foreground">{t(`${D}usage_none`)}</p>
            : (
              <>
                <div className="flex justify-between px-3 text-xs text-muted-foreground">
                  <span>{t(`${D}usage_col_site`)}</span><span>{t(`${D}usage_col_count`)}</span>
                </div>
                {answer.items.map(it => (
                  <div key={it.site} data-testid="usage-site" className="flex justify-between gap-2 rounded-lg border p-3">
                    <span className="min-w-0 text-sm [overflow-wrap:anywhere]">{it.site}</span>
                    <span className="font-semibold tabular-nums">{num.format(it.count)}</span>
                  </div>
                ))}
                {answer.others.sites > 0 && (
                  <div data-testid="usage-others" className="flex justify-between gap-2 px-3 text-sm text-muted-foreground">
                    <span>{t(`${D}usage_others`, { sites: num.format(answer.others.sites) })}</span>
                    <span className="tabular-nums">{num.format(answer.others.count)}</span>
                  </div>
                )}
                <p className="px-3 text-sm">{t(`${D}usage_total`, { n: num.format(answer.total) })}</p>
              </>
            )}
          {answer.nodes.length > 0 && (
            <p className="text-xs text-muted-foreground">{t(`${D}usage_from`, { nodes: answer.nodes.map(n => n.name).join(t(`${D}hits_list_separator`)) })}</p>
          )}
          {answer.dropped_in_range > 0 && <p className="text-sm">{t(`${D}usage_dropped`, { n: num.format(answer.dropped_in_range) })}</p>}
        </div>
      )}
    </div>
  )
}
