import { useId } from 'react'
import type { Server } from '@/api/servers'
import type { Node } from '@/api/types'
import { StatusDot } from '@/components/common/StatusDot'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { OptionCards } from '@/components/form/OptionCards'
import { PathDiagram, type PathEdge, type PathNode } from '@/components/diagram/PathDiagram'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { NativeSelect } from '@/components/ui/native-select'
import type { TransitLink } from '@/api/nodes'
import { useNodesT } from '../shared/useNodesT'
import { ProbeChip } from '../lines/ProbeChip'
import { LinkQuality } from './LinkQuality'
import { TunnelFields } from './TunnelFields'

export type ExitMode = 'local' | 'remote'

/** The picture on one of the two cards: this node, where it leaves from, the internet. */
function ExitPicture({ node, exitName }: { node: Node; exitName?: string }) {
  const nodes: PathNode[] = [{ id: 'l', kind: 'landing', label: node.display_name }, ...(exitName !== undefined ? [{ id: 'x', kind: 'exit' as const, label: exitName || undefined }] : []), { id: 'i', kind: 'internet' }]
  const edges: PathEdge[] = exitName !== undefined ? [{ style: 'dashed' }, { style: 'solid', name: '' }] : [{ style: 'solid', name: '' }]
  return <PathDiagram nodes={nodes} edges={edges} size="sm" />
}

/**
 * The default exit (design 4.3): where this node's traffic leaves when no route says otherwise. Two cards, the
 * server select only for the second, then, once an exit is chosen, whether its tunnel runs, how good it is and the
 * tunnel's own settings (shared by every node between the two servers, so they sit in a collapsed section).
 * Presentation only: the tab owns the state and the save.
 */
export function DefaultExit({ node, exits, native, mode, exitId, onMode, onExit, savedExit, ready, error, link, linkError, onLink }: {
  node: Node
  /** The Passwall Node servers other than the node's own. */
  exits: readonly Server[]
  /** The node's own server is a Passwall Node: only then can traffic leave elsewhere. */
  native: boolean
  mode: ExitMode
  exitId: number
  onMode: (mode: ExitMode) => void
  onExit: (id: number) => void
  savedExit: number
  ready: boolean
  /** A problem with the choice, shown on the select. */
  error?: string
  link: TransitLink | null
  linkError: boolean
  onLink: (patch: Partial<TransitLink>) => void
}) {
  const t = useNodesT()
  const labelId = useId()
  const noExits = exits.length === 0
  const remoteOff = !native || noExits
  const exitName = exits.find(s => s.id === exitId)?.name
  const shown = mode === 'remote' && exitId > 0 && exitId === savedExit
  return (
    <section className="flex flex-col gap-4" aria-labelledby={labelId}>
      <h2 id={labelId} className="text-sm font-medium">{t('nodes.exit.default')}</h2>
      {!native && <Alert><AlertDescription>{t('nodes.exit.not_native')}</AlertDescription></Alert>}
      <OptionCards<ExitMode> value={mode} onChange={onMode} aria-labelledby={labelId} columns={2} options={[
        { value: 'local', title: t('nodes.exit.local'), description: t('nodes.egress.local_hint'), diagram: <ExitPicture node={node} />, disabled: !native, disabledReason: !native ? t('nodes.exit.not_native') : undefined },
        { value: 'remote', title: t('nodes.exit.remote'), description: t('nodes.egress.tunnel_hint'), diagram: <ExitPicture node={node} exitName={exitName ?? ''} />, disabled: remoteOff,
          disabledReason: remoteOff ? t(native ? 'nodes.exit.no_other_server' : 'nodes.exit.not_native') : undefined },
      ]} />
      {mode === 'remote' && !remoteOff && (
        <div data-testid="egress-section" className="flex flex-col gap-3">
          <Field label={t('nodes.exit.server')} required error={error}>
            <NativeSelect id="exit-server" value={exitId} onChange={e => onExit(Number(e.target.value) || 0)}>
              <option value={0}>{t('nodes.exit.pick_server')}</option>
              {exits.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </NativeSelect>
          </Field>
          {shown && (
            <div className="flex flex-wrap items-center gap-3">
              <span data-testid="egress-status"><StatusDot tone={ready ? 'success' : 'neutral'} label={t(ready ? 'nodes.egress.ready' : 'nodes.egress.pending')} className="text-xs" /></span>
              <ProbeChip probe={node.egress_probe} withheld={node.egress_withheld} testId="egress-probe" />
            </div>
          )}
          {exitId > 0 && linkError && <p className="text-xs text-destructive">{t('nodes.egress.load_failed')}</p>}
          {exitId > 0 && link && (
            <>
              <LinkQuality entry={node.panel_id} exit={exitId} />
              <FormSection key={exitId} title={t('nodes.exit.tunnel_settings')} description={t('nodes.egress.shared_hint')} advanced sectionKey="node-exit-tunnel">
                <div className="flex flex-col gap-3"><TunnelFields link={link} onChange={onLink} /></div>
              </FormSection>
            </>
          )}
        </div>
      )}
    </section>
  )
}
