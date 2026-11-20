import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { CopyText } from '@/components/common/CopyButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { DetailSheet } from '@/components/data/DetailSheet'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import type { LandingColor } from '@/components/diagram/landingColor'
import { CONFIG_SYNC_KEY, CONFIG_SYNC_STATES, type ConfigSyncState } from '../../configSync'
import { formatDualTz } from '@/utils/datetime'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { statusDetailKey, statusOf } from '../shared/statusOf'
import { useNodesT } from '../shared/useNodesT'
import { StatusCell } from './cells'
import type { ListRow } from './types'

/**
 * The right-hand sheet of a line, in VIEW mode (design 2.6): the path, the state in words and the facts. Editing
 * is a separate action (a legacy editor until N2), never an editable form behind a click that only meant to look.
 * It shows the node-level facts a relay row does not repeat (config push, observed listener), so nothing is hidden,
 * only not repeated on every row.
 */
/** The stored state when it is one the page knows, else "never captured" (an absent field is that too). */
function configState(raw: string | undefined): ConfigSyncState {
  const v = raw ?? ''
  return (CONFIG_SYNC_STATES as readonly string[]).includes(v) ? (v as ConfigSyncState) : ''
}

export function LineSheet({ item, siblings, colors, labels, tz, menu, canEdit, nodeHref, onEdit, onSelect, onClose }: {
  item: ListRow | null
  /** The node's other lines, in list order. */
  siblings: readonly ListRow[]
  colors: Map<string, LandingColor>
  labels: PathLabels
  tz: string
  menu: MoreMenuItem[]
  canEdit: boolean
  nodeHref?: string
  onEdit: () => void
  onSelect: (id: string) => void
  onClose: () => void
}) {
  const t = useNodesT()
  const row = item?.row
  const { nodes, edges } = item && row
    ? pathOf(row, { colors, labels, exit: item.exitName ? { name: item.exitName } : null, ends: true, node: item.node })
    : { nodes: [], edges: [] }
  const status = item && row ? statusOf(row, item.node) : null
  const detailKey = status ? statusDetailKey(status.id) : null
  const direct = row?.line_id === 'direct'
  const Row = ({ label, children }: { label: string; children: React.ReactNode }) => (
    <><dt className="text-muted-foreground">{label}</dt><dd className="min-w-0 break-words">{children}</dd></>
  )
  const node = item?.node
  const probe = row?.probe
  const observed = node && (node.observed_protocol || (node.observed_port ?? 0) > 0)

  return (
    <DetailSheet
      open={item !== null}
      onClose={onClose}
      title={row?.name ?? ''}
      status={item && <StatusCell item={item} />}
      actions={item && (
        <>
          {canEdit && <Button size="sm" onClick={onEdit}>{t(direct ? 'nodes.action.edit_direct' : 'nodes.action.edit_line')}</Button>}
          {menu.length > 0 && <MoreMenu items={menu} label={t('nodes.action.row_menu', { name: row?.name ?? '' })} />}
        </>
      )}
    >
      {item && row && (
        <div className="flex flex-col gap-4">
          <PathDiagram nodes={nodes} edges={edges} />
          {detailKey && <p className="text-sm text-muted-foreground" data-testid="status-detail">{t(detailKey)}</p>}
          {row.withheld === 'source_guard' && row.source_withheld && <p className="text-sm text-muted-foreground">{t(`nodes.source.withheld.${row.source_withheld}`, { defaultValue: '' })}</p>}
          {row.withheld === 'cdn_unfit' && (row.cdn_issues ?? []).length > 0 && (
            <ul className="list-disc pl-5 text-sm text-muted-foreground">
              {(row.cdn_issues ?? []).map((i, n) => <li key={`${i.code}-${n}`}>{t(`nodes.cdn.issue.${i.code}`, { ...(i.params ?? {}), defaultValue: i.code })}</li>)}
            </ul>
          )}
          <dl className="grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-2 text-sm">
            <Row label={t('nodes.sheet.type')}>{t(`nodes.type.${row.line_type ?? 'direct'}`)}</Row>
            {row.address && <Row label={t('nodes.col.address')}><CopyText value={row.port ? `${row.address}:${row.port}` : row.address} /></Row>}
            {row.entry && <Row label={t('nodes.sheet.entry')}>{row.entry.panel_name}</Row>}
            <Row label={t('nodes.sheet.landing')}>
              {nodeHref ? <Link to={nodeHref} className="underline-offset-2 hover:underline">{row.landing?.name}</Link> : row.landing?.name}
              {row.landing?.panel_name ? <span className="text-muted-foreground"> · {row.landing.panel_name}</span> : null}
            </Row>
            {row.traffic_factor_milli !== undefined && row.traffic_factor_milli !== 1000 && <Row label={t('nodes.col.factor')}>×{row.traffic_factor_milli / 1000}</Row>}
            {(row.landing?.region || (node?.tags ?? []).length > 0) && (
              <Row label={t('nodes.sheet.region_tags')}>{[row.landing?.region, ...(node?.tags ?? [])].filter(Boolean).join(' · ')}</Row>
            )}
            {probe && probe.state !== 'unknown' && (
              <Row label={t('nodes.sheet.quality')}>{t(`nodes.probe.state_${probe.state}`)} · {t('nodes.probe.rtt', { ms: Math.round(probe.median_rtt_ms) })} · {t('nodes.probe.loss', { percent: Math.round(probe.loss_percent) })}</Row>
            )}
            <Row label={t('nodes.sheet.health')}>
              {t(`nodes.health.${row.health?.state && row.health.state !== 'unknown' ? row.health.state : 'unknown'}`)}
              {row.health?.checked_at ? <span className="text-muted-foreground"> · {formatDualTz(row.health.checked_at, tz)}</span> : null}
              {row.health?.detail ? <span className="block text-muted-foreground">{row.health.detail}</span> : null}
            </Row>
          </dl>
          <section className="flex flex-col gap-2 border-t pt-3">
            <h3 className="text-xs font-medium text-muted-foreground">{t('nodes.sheet.node_section')}</h3>
            <dl className="grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-2 text-sm">
              {node && <Row label={t('nodes.sheet.config_sync')}>{t(`nodes.config_sync.${CONFIG_SYNC_KEY[configState(node.config_sync_state)]}`)}</Row>}
              {node && observed && (
                <Row label={t('nodes.sheet.listener')}>
                  {node.desired_protocol || node.protocol}:{node.desired_port} → {node.observed_protocol || '—'}:{node.observed_port || '—'}
                </Row>
              )}
              {siblings.length > 0 && (
                <Row label={t('nodes.sheet.other_lines')}>
                  <span className="flex flex-col items-start gap-1">
                    {siblings.map(s => <button key={s.id} type="button" className="text-left underline-offset-2 hover:underline" onClick={() => onSelect(s.id)}>{s.row.name}</button>)}
                  </span>
                </Row>
              )}
            </dl>
          </section>
        </div>
      )}
    </DetailSheet>
  )
}
