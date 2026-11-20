import type { RouteLink } from '@/api/routes'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { ProbeChip } from '../../lines/ProbeChip'
import { useNodesT } from '../../shared/useNodesT'

const TONE: Record<string, Tone> = { ready: 'success', not_ready: 'warning', missing: 'danger', gone: 'danger' }

/**
 * The tunnel from the node's server to an exit, in words: the server is gone, missing, not ready (which end has not
 * applied it) or ready, plus the measured quality. in_applied is the exit's end and out_applied the entry's.
 */
export function RouteLinkStatus({ link, testId }: { link: RouteLink; testId: string }) {
  const t = useNodesT()
  const K = 'nodes.routes.link.'
  const state = link.gone ? 'gone' : !link.exists ? 'missing' : link.ready ? 'ready' : 'not_ready'
  return (
    <div data-testid={testId} data-state={state} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
      <StatusBadge tone={TONE[state]} label={t(`${K}${state}`)} />
      {link.exists && (
        <span data-testid={`${testId}-ends`} className="text-muted-foreground">
          {t(`${K}in_end`)}: {t(link.in_applied ? `${K}applied` : `${K}waiting`)} · {t(`${K}out_end`)}: {t(link.out_applied ? `${K}applied` : `${K}waiting`)}
        </span>
      )}
      {link.exists && link.ports_differ && (
        <span data-testid={`${testId}-ports`} className="text-warning-ink">{t(`${K}ports_differ`, { inPort: link.in_port, outPort: link.out_port })}</span>
      )}
      {link.quality && <ProbeChip probe={link.quality} testId={`${testId}-quality`} />}
    </div>
  )
}
