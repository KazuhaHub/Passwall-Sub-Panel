import { useMemo } from 'react'
import type { NodeSetupPlan } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { TONE_STYLE } from '@/components/common/tones'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { Field } from '@/components/form/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { IconError, IconInfo } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { CDN_DEFAULT_PORT } from '../lines/lineModel'
import { useNodesT } from '../shared/useNodesT'
import { reviewPaths } from './reviewPaths'
import type { SetupRelay } from './setupModel'

export interface ReviewStepProps {
  plan: NodeSetupPlan
  entries: SetupRelay[]
  direct: boolean
  peers: Server[]
  landingPort: number
  routeCount: number
  editingLoopbacks: Record<number, number>
  onLoopback: (server: number, port: number) => void
  editingLinkPorts: Record<string, number>
  onLinkPort: (link: string, port: number) => void
  changed: boolean
  landingName: string
  /** The default exit server's name; null = the node leaves through its own server. */
  exitName: string | null
}

/**
 * Step 4: what the plan will create. Each line is drawn as its path with the ports the plan chose written on
 * it, and the table lists every binding with the two port kinds that can be edited (a shared port's loopback
 * and a transit tunnel's port). An edit does not change the plan by itself: the wizard asks for a new preview.
 */
export function ReviewStep({
  plan, entries, direct, peers, landingPort, routeCount, editingLoopbacks, onLoopback, editingLinkPorts, onLinkPort, changed, landingName, exitName,
}: ReviewStepProps) {
  const t = useNodesT()
  const peer = (id: number) => peers.find(p => p.id === id)?.name ?? `#${id}`
  const labels = useMemo(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const paths = useMemo(() => reviewPaths({
    plan, entries, direct, peers, landingName, landingPort, exitName, labels, loopbackLabel: port => t('nodes.setup.hop_loopback', { port }),
  }), [plan, entries, direct, peers, landingName, landingPort, exitName, labels, t])

  return (
    <div className="flex flex-col gap-4">
      {changed && (
        <Alert><IconInfo /><AlertDescription>{t('nodes.setup.review_changes')}</AlertDescription></Alert>
      )}
      <p className="text-sm">{t(direct ? 'nodes.setup.direct_on' : 'nodes.setup.direct_off')}</p>
      {routeCount > 0 && <p className="text-sm">{t('nodes.setup.routes_count', { count: routeCount })}</p>}
      {plan.warnings?.map((warning, i) => (
        <Alert key={i} className={TONE_STYLE.warning.line}><IconError className={TONE_STYLE.warning.ink} /><AlertDescription>{warning}</AlertDescription></Alert>
      ))}
      <section className="flex flex-col gap-3" aria-label={t('nodes.setup.paths')}>
        {paths.map(path => (
          <div key={path.key} data-testid="review-path" className="flex flex-col gap-2 rounded-lg border p-3">
            <p className="text-sm font-medium">{t(`nodes.setup.kind.${path.kind}`)}{path.name && <span className="font-normal text-muted-foreground"> · {path.name}</span>}</p>
            <PathDiagram nodes={path.nodes} edges={path.edges} size="md" />
          </div>
        ))}
      </section>
      <Table aria-label={t('nodes.setup.review')}>
        <TableHeader>
          <TableRow>
            <TableHead>{t('nodes.setup.server')}</TableHead>
            <TableHead>{t('nodes.setup.binding')}</TableHead>
            <TableHead>{t('nodes.setup.sni')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {[...plan.bindings].sort((a, b) => a.panel_id - b.panel_id).map((b, i) => {
            const link = b.kind === 'link' && !b.reused ? `${b.peer_id}/${b.panel_id}` : null
            return (
              <TableRow key={i}>
                <TableCell className="align-top">{peer(b.panel_id)}</TableCell>
                <TableCell className="align-top whitespace-normal">
                  <div>{t(`nodes.setup.kind.${b.kind}`)}{b.name && ` · ${b.name}`}{b.peer_id ? ` · ${peer(b.peer_id)}` : ''}</div>
                  <div className="text-muted-foreground">
                    {b.address && `${b.address} · `}{b.listen || t('nodes.setup.all_addresses')} · {b.port}
                    {b.reused && ` · ${t('nodes.setup.reused')}`}
                  </div>
                  {b.front_id ? <div className="text-muted-foreground">{t('nodes.setup.shared')}</div> : null}
                  {b.loopback ? (
                    <Field className="mt-2 max-w-48" label={t('nodes.setup.loopback')}>
                      <Input type="number" inputMode="numeric" value={editingLoopbacks[b.panel_id] ?? b.loopback}
                        onChange={e => onLoopback(b.panel_id, Number(e.target.value) || 0)} />
                    </Field>
                  ) : null}
                  {link && (
                    <Field className="mt-2 max-w-48" label={t('nodes.setup.link_port')}>
                      <Input type="number" inputMode="numeric" value={editingLinkPorts[link] ?? b.port}
                        onChange={e => onLinkPort(link, Number(e.target.value) || 0)} />
                    </Field>
                  )}
                </TableCell>
                <TableCell className={cn('align-top whitespace-normal')}>
                  {b.sni?.join(', ') || '—'}
                  {b.target && <div className="text-muted-foreground">{b.target}</div>}
                </TableCell>
              </TableRow>
            )
          })}
          {entries.filter(e => !e.line.entry_panel_id).map(({ key, line }) => (
            <TableRow key={key}>
              <TableCell className="align-top">{t(line.mode === 'cdn' ? 'nodes.relay.entry_cdn' : 'nodes.relay.entry_external')}</TableCell>
              <TableCell className="align-top whitespace-normal">{line.address || line.domain} · {line.port || (line.mode === 'cdn' ? CDN_DEFAULT_PORT : landingPort)}</TableCell>
              <TableCell className="align-top">{line.sni || line.domain || '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
