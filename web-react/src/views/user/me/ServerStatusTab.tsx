import type { MyNodeStatus, MyRelayStatus } from '@/api/me'
import { StatusDot } from '@/components/common/StatusDot'
import type { Tone } from '@/components/common/tones'
import { EmptyState } from '@/components/feedback/EmptyState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { FactorBadge } from './FactorBadge'
import { useMeT } from './useMeT'

const STATE_TONE: Record<string, Tone> = { ok: 'success', down: 'danger' }
const QUALITY_TONE: Record<string, Tone> = { ok: 'success', degraded: 'warning', down: 'danger' }

/**
 * The coarse link quality of a relay line and its round-trip time. No loss figure, nothing withheld; a field that
 * is absent was not probed and draws nothing.
 */
function RelayQuality({ quality, rttMs }: { quality?: MyRelayStatus['quality']; rttMs?: number }) {
  const { t } = useMeT()
  const label = quality === 'ok' ? t('status.quality_ok') : quality === 'degraded' ? t('status.quality_degraded') : quality === 'down' ? t('status.quality_down') : null
  const rtt = typeof rttMs === 'number' && rttMs >= 0 ? Math.round(rttMs) : null
  if (!label && rtt === null) return null
  return (
    <span
      data-testid="relay-quality" data-quality={quality}
      aria-label={label ? t('status.quality_label', { state: label }) : undefined}
      className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"
    >
      {label && quality && <StatusDot tone={QUALITY_TONE[quality] ?? 'neutral'} label={label} className="text-xs text-foreground" />}
      {rtt !== null && <span className="tabular-nums">{t('status.rtt', { ms: rtt })}</span>}
    </span>
  )
}

/**
 * The caller's own servers: names, region and a coarse status, sanitized server-side. A line's status is its entry's
 * own probe, not an extra health check and not a claim that the whole path works end to end, which the hint says
 * whenever relays are listed.
 */
export function ServerStatusTab({ nodes, loading }: { nodes: MyNodeStatus[] | null; loading: boolean }) {
  const { t } = useMeT()
  if (loading) return <Card><CardContent><CardSkeleton lines={4} /></CardContent></Card>

  const rows = (nodes ?? []).flatMap(n => {
    const direct = n.show_direct !== false
      ? [{ name: n.name, region: n.region, status: n.status, checkedAt: n.checked_at, factor: n.traffic_factor_milli, quality: undefined as MyRelayStatus['quality'], rttMs: undefined as number | undefined }]
      : []
    const relays = (n.relay_statuses ?? []).map((relay, i) => {
      const label = relay.name || t('status.relay_fallback', { index: i + 1 })
      return { name: relay.only_line_name ? label : `${relay.node_name ?? n.name} ${label}`, region: n.region, status: relay.status, checkedAt: relay.checked_at, factor: relay.traffic_factor_milli, quality: relay.quality, rttMs: relay.rtt_ms }
    })
    return [...direct, ...relays]
  })
  if (rows.length === 0) return <Card><CardContent><EmptyState compact title={t('status.empty')} /></CardContent></Card>

  const ok = rows.filter(r => r.status === 'ok').length
  const down = rows.filter(r => r.status === 'down').length
  const hasRelays = (nodes ?? []).some(n => (n.relay_statuses ?? []).length > 0)
  const checkedAt = rows.find(r => r.checkedAt)?.checkedAt
  const word = (s: MyNodeStatus['status']) => (s === 'ok' ? t('status.ok') : s === 'down' ? t('status.down') : t('status.unknown'))

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-baseline gap-x-3">
          {t('status.title')}
          <span className={cn('text-sm font-normal tabular-nums', down > 0 ? 'text-destructive' : 'text-muted-foreground')}>{t('status.summary', { ok, total: rows.length })}</span>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="divide-y">
          {rows.map((row, i) => (
            <li key={`${row.name}-${i}`} className="flex items-center gap-3 py-2.5 first:pt-0">
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate font-medium" title={row.name}>{row.name}</span>
                  <FactorBadge milli={row.factor} />
                </div>
                {row.region && <div className="truncate text-xs text-muted-foreground" title={row.region}>{row.region}</div>}
              </div>
              <RelayQuality quality={row.quality} rttMs={row.rttMs} />
              <StatusDot tone={STATE_TONE[row.status] ?? 'neutral'} label={word(row.status)} className="shrink-0" />
            </li>
          ))}
        </ul>
        {hasRelays && <p className="mt-3 text-xs text-muted-foreground">{t('status.relay_hint')}</p>}
        {checkedAt && <p className="mt-1 text-xs text-muted-foreground">{t('status.checked_at', { time: new Date(checkedAt).toLocaleString() })}</p>}
      </CardContent>
    </Card>
  )
}
