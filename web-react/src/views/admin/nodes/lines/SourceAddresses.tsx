import { Button } from '@/components/ui/button'
import { Field } from '@/components/form/Field'
import { LazyCodeEditor } from '@/components/LazyCodeEditor'
import { InfoTip } from '@/components/common/HelpTip'
import type { CloudflareRanges, LineSourceGuard } from '@/api/sourceGuard'
import type { RelayLine } from '@/api/types'
import { useNodesT } from '../shared/useNodesT'
import { useTranslation } from 'react-i18next'

/**
 * Source addresses of a forward, external or CDN line (ADR 0044): the administrator's own list, the set that is in
 * force, and what the landing refused. The effective set and the refusals are what the panel says of the SAVED
 * line; a line that is not saved yet has none, and the field still works without them.
 */
export function SourceAddresses({ line, text, onText, guard, canGuard, cannotGuardReason, cloudflare, onAdd, busy }: {
  line: Pick<RelayLine, 'entry_panel_id' | 'mode'>
  /** The addresses as typed, one per line. */
  text: string
  onText: (text: string) => void
  guard?: LineSourceGuard
  /** Whether the landing can check sources; absent while unknown. */
  canGuard?: boolean
  cannotGuardReason?: 'server' | 'listener'
  cloudflare?: CloudflareRanges
  /** Adds a refused address to the saved line. */
  onAdd: (ip: string) => void
  busy?: boolean
}) {
  const t = useNodesT()
  const { i18n } = useTranslation()
  const managed = (line.entry_panel_id ?? 0) > 0
  const cdn = line.mode === 'cdn'
  const hint = cdn ? t('nodes.source.hint_cdn') : managed ? t('nodes.source.hint_managed') : t('nodes.source.hint_external')
  const time = (iso?: string) => (iso ? new Date(iso).toLocaleString(i18n.language) : '')
  const own = text.trim() !== ''
  // A CDN line that names no addresses of its own uses Cloudflare's; otherwise the set is what the panel works out for the saved line.
  const effective = cdn && !own ? (cloudflare?.cidrs ?? guard?.effective_cidrs ?? []) : (guard?.effective_cidrs ?? [])
  return (
    <div data-testid="source-addresses" className="flex flex-col gap-2">
      <Field label={t('nodes.source.title')} hint={hint}>
        <LazyCodeEditor language="plain" tabMovesFocus minRows={2} maxRows={8} placeholder={t('nodes.source.placeholder')} value={text} onChange={onText} />
      </Field>
      <div className="flex items-center gap-1 text-xs text-muted-foreground">
        <span>{t('nodes.source.effective')}</span>
        <InfoTip text={t('nodes.source.hint')} />
      </div>
      <div data-testid="source-effective" className="font-mono text-xs break-all text-muted-foreground">
        {effective.length > 0 ? effective.join('  ') : t('nodes.source.effective_unknown')}
      </div>
      {cdn && !own && cloudflare && (
        <p data-testid="source-cloudflare" className="text-xs text-muted-foreground">
          {cloudflare.source === 'fetched' ? t('nodes.source.cloudflare_fetched', { time: time(cloudflare.fetched_at) }) : t('nodes.source.cloudflare_builtin', { date: cloudflare.builtin_date })}
          {!cloudflare.auto_fetch && ` · ${t('nodes.source.cloudflare_auto_off')}`}
        </p>
      )}
      {canGuard === false && <p data-testid="source-cannot-guard" className="text-xs text-muted-foreground">{t(cannotGuardReason === 'listener' ? 'nodes.source.cannot_guard_listener' : 'nodes.source.cannot_guard')}</p>}
      {guard?.withheld && <p data-testid="source-withheld" className="text-xs text-destructive">{t(`nodes.source.withheld.${guard.withheld}`)}</p>}
      {(guard?.refused ?? []).map(r => (
        <div key={r.ip} data-testid="source-refused" className="flex flex-wrap items-center gap-2">
          <p className="min-w-48 flex-1 text-xs text-destructive">
            {t(r.kind === 'wrong_line' ? 'nodes.source.refused_wrong_line' : 'nodes.source.refused', { ip: r.ip, count: r.count, time: time(r.last_seen) })}
          </p>
          {r.kind !== 'wrong_line' && r.addable && <Button variant="outline" size="sm" disabled={busy} onClick={() => onAdd(r.ip)}>{t('nodes.source.add')}</Button>}
        </div>
      ))}
    </div>
  )
}
