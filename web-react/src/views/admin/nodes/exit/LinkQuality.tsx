import { pickLinkProbe, probeQualityOf } from '@/api/transitProbe'
import { useTransitProbes } from '@/query/transitProbe'
import { useQueryScope } from '@/query/useQueryScope'
import { ProbeChip } from '../lines/ProbeChip'
import { useNodesT } from '../shared/useNodesT'

/**
 * The quality of one tunnel (entry to exit server): the end-to-end result when there is one, else the transport
 * result. A failed load or a link nobody has probed yet shows an unobtrusive dash, never an error: the quality is
 * context for a setting, not something the person must act on.
 */
export function LinkQuality({ entry, exit }: { entry: number; exit: number }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const q = useTransitProbes(scope, entry, { enabled: entry > 0 && exit > 0 })
  const item = q.data ? pickLinkProbe(q.data, entry, exit) : undefined
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span>{t('nodes.probe.quality')}</span>
      {item
        ? <ProbeChip probe={probeQualityOf(item)} testId="link-quality" />
        : <span data-testid="link-quality-none" aria-label={t('nodes.probe.no_link_data')}>—</span>}
    </div>
  )
}
