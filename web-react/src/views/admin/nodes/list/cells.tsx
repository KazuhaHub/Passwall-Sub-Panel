import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { CopyText } from '@/components/common/CopyButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import type { LandingColor } from '@/components/diagram/landingColor'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { statusLabelKey, statusOf } from '../shared/statusOf'
import { useNodesT } from '../shared/useNodesT'
import type { ListRow } from './types'

export function StatusCell({ item }: { item: ListRow }) {
  const t = useNodesT()
  const s = statusOf(item.row, item.node)
  const label = t(statusLabelKey(s.id))
  return s.quiet ? <StatusDot tone={s.tone} label={label} hideLabel /> : <div className="flex max-w-full min-w-0" title={label}><StatusBadge tone={s.tone} label={label} className="min-w-0 max-w-full" /></div>
}

/** The path of a row, as a link to its node for an administrator. A table row has no room for the ends. */
export function PathCell({ item, colors, labels, nodeHref }: { item: ListRow; colors: Map<string, LandingColor>; labels: PathLabels; nodeHref?: string }) {
  const t = useNodesT()
  const { nodes, edges } = pathOf(item.row, { colors, labels, exit: item.exitName ? { name: item.exitName } : null, node: item.node })
  const titleOf = nodes.map(n => [n.label, n.sublabel].filter(Boolean).join(' ')).filter(Boolean).join(' → ')
  const diagram = <PathDiagram nodes={nodes} edges={edges} size="sm" />
  return nodeHref
    ? <Link to={nodeHref} title={`${titleOf}\n${t('nodes.action.open_node')}`} className="flex w-full min-w-0 rounded-sm outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">{diagram}</Link>
    : <span className="flex w-full min-w-0" title={titleOf}>{diagram}</span>
}

export function NameCell({ item, colors, hint }: { item: ListRow; colors: Map<string, LandingColor>; hint?: string }) {
  const color = colors.get(String(item.row.node_id))
  return (
    <div className="flex min-w-0 items-center gap-2">
      <LandingColorBar color={color} className="h-5 self-center transition-[width] group-data-[related=true]/row:w-2" />
      <span className="min-w-0 truncate font-medium" title={hint ?? item.row.name}>{item.row.name}</span>
    </div>
  )
}

export function AddressCell({ row }: { row: ListRow['row'] }) {
  if (!row.address) return <span className="text-muted-foreground">—</span>
  const text = row.port ? `${row.address}:${row.port}` : row.address
  return <CopyText value={text} />
}

export function FactorCell({ row }: { row: ListRow['row'] }) {
  const milli = row.traffic_factor_milli
  if (milli === undefined || milli === 1000) return null
  return <span className="tabular-nums">×{milli / 1000}</span>
}

export function TagsCell({ item }: { item: ListRow }) {
  const tags = item.node?.tags ?? []
  if (!tags.length) return <span className="text-muted-foreground">—</span>
  return (
    <span className="inline-flex flex-wrap gap-1">
      {tags.slice(0, 3).map(tag => <Badge key={tag} variant="secondary">{tag}</Badge>)}
      {tags.length > 3 && <span className="text-xs text-muted-foreground">+{tags.length - 3}</span>}
    </span>
  )
}

/**
 * The row's on/off switch, so a line or a node is turned on or off without opening a menu. It is the same action the
 * row's menu offers (and gated the same way, since it is read from that menu): on a relay row the LINE's switch, on a
 * direct row the NODE's (the direct line is the node). A row whose menu offers neither shows nothing.
 */
export function SwitchCell({ item, items }: { item: ListRow; items: MoreMenuItem[] }) {
  const t = useNodesT()
  const row = item.row
  const direct = row.line_id === 'direct'
  const action = items.find(i => i.id === (direct ? 'toggle-node' : 'toggle-line'))
  const on = direct ? row.landing?.enabled !== false : row.enabled !== false
  // Off from the click until the row's own value changes (the list reads it back after the save), so a double click
  // sends one request, not two. A save that fails leaves the value as it was: the switch comes back after a while.
  const [pendingFrom, setPendingFrom] = useState<boolean | null>(null)
  useEffect(() => { if (pendingFrom !== null && pendingFrom !== on) setPendingFrom(null) }, [on, pendingFrom])
  useEffect(() => {
    if (pendingFrom === null) return
    const timer = setTimeout(() => setPendingFrom(null), 8000)
    return () => clearTimeout(timer)
  }, [pendingFrom])
  if (!action?.onSelect) return null
  return (
    <Switch size="sm" checked={on} disabled={action.disabled || pendingFrom !== null} aria-busy={pendingFrom !== null || undefined}
      aria-label={t(direct ? 'nodes.action.node_switch' : 'nodes.action.line_switch', { name: row.name })}
      onCheckedChange={next => { if (next !== on && pendingFrom === null) { setPendingFrom(on); action.onSelect?.() } }} onClick={e => e.stopPropagation()} />
  )
}

export function RowMenu({ items, name }: { items: MoreMenuItem[]; name: string }) {
  const t = useNodesT()
  if (items.length === 0) return null
  return <MoreMenu items={items} size="icon-xs" label={t('nodes.action.row_menu', { name })} />
}

/** A separator row, drawn across the whole row: a rule with its name, then what it applies to. */
export function SeparatorRowCell({ name, enabled, mode, nodeCount, menu }: { name: string; enabled: boolean; mode?: string; nodeCount: number; menu: MoreMenuItem[] }) {
  const t = useNodesT()
  return (
    <div className="flex min-w-0 items-center gap-3">
      <span className="min-w-0 truncate text-xs font-medium tracking-wide text-foreground/70" title={name}>— {name} —</span>
      <span aria-hidden className="h-px flex-1 bg-border" />
      <Badge variant="outline">{mode === 'node_bound' ? t('nodes.separator.node_bound', { count: nodeCount }) : t('nodes.separator.global')}</Badge>
      {!enabled && <Badge variant="outline">{t('nodes.separator.disabled')}</Badge>}
      <RowMenu items={menu} name={name} />
    </div>
  )
}
