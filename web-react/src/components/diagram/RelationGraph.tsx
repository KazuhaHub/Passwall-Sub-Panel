import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { ToneDot } from '@/components/common/StatusDot'
import { TONE_STYLE } from '@/components/common/tones'
import { useSpacedT } from '@/i18n/spacing'
import { cn } from '@/lib/utils'
import { landingColorStyle, type LandingColor } from './landingColor'
import { toneOfPath, type PathTone } from './pathSummary'
import '@/styles/data-tokens.css'

export interface GraphBox {
  id: string
  label: string
  sublabel?: string
  status?: PathTone
  /** For a landing node: the colour bar that matches LandingColorBar elsewhere on the page. */
  landingColor?: LandingColor
}

export interface GraphColumn {
  id: string
  title: string
  boxes: GraphBox[]
}

export interface GraphEdge {
  /** Identity of the line (its line id): two lines can join the same two boxes, and each must stay itself in the picture and in the text. Required, and unique among the edges (see RelationGraph). */
  id: string
  /** The line's name, shown in the lines list next to its path when there are several. */
  label?: string
  from: string
  to: string
  style?: 'solid' | 'dashed'
  status?: PathTone
}

export interface RelationGraphProps {
  /** Two or three columns, left to right (entries, landings, exits). Box ids are unique across all of them. */
  columns: readonly GraphColumn[]
  edges: readonly GraphEdge[]
  /** Controlled selection; leave undefined for the graph to keep its own. */
  selectedId?: string | null
  onSelect?: (id: string | null) => void
  /** CSS max-height of the scrolling canvas. Boxes never shrink: a long column scrolls. */
  maxHeight?: string
  /**
   * `canvas`: columns joined by curves. `list`: the columns stacked, for a phone,
   * where a canvas would put two of three columns off screen. `auto` picks list
   * below the sm breakpoint.
   */
  layout?: 'auto' | 'canvas' | 'list'
  className?: string
}

// Fixed geometry on purpose. With ~30 boxes per column, fitting everything into
// the frame would shrink them past reading; a fixed box that scrolls stays legible.
const BOX_W = 176
const BOX_H = 44
const GAP_X = 96
const GAP_Y = 8
const HEADER_H = 28
const PAD = 8

export interface BoxRect { id: string; col: number; row: number; x: number; y: number; w: number; h: number }

export function layoutGraph(columns: readonly GraphColumn[]): { boxes: Record<string, BoxRect>; width: number; height: number } {
  const boxes: Record<string, BoxRect> = {}
  columns.forEach((c, col) => c.boxes.forEach((b, row) => {
    boxes[b.id] = { id: b.id, col, row, x: PAD + col * (BOX_W + GAP_X), y: PAD + HEADER_H + row * (BOX_H + GAP_Y), w: BOX_W, h: BOX_H }
  }))
  const tallest = Math.max(0, ...columns.map(c => c.boxes.length))
  return {
    boxes,
    width: PAD * 2 + columns.length * BOX_W + Math.max(0, columns.length - 1) * GAP_X,
    height: PAD * 2 + HEADER_H + tallest * BOX_H + Math.max(0, tallest - 1) * GAP_Y,
  }
}

/** A cubic curve from one box's near edge to the other's, horizontal at both ends. */
export function edgePath(a: BoxRect, b: BoxRect, offset = 0): string {
  const forward = a.col <= b.col
  const x1 = forward ? a.x + a.w : a.x
  const x2 = forward ? b.x : b.x + b.w
  // `offset` moves both ends up or down the box edge, so parallel lines between the
  // same two boxes are separate curves.
  const y1 = a.y + a.h / 2 + offset
  const y2 = b.y + b.h / 2 + offset
  const mid = (x1 + x2) / 2
  return `M ${x1} ${y1} C ${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`
}

/** The text for a non-normal box status, or '' (healthy and neutral are not spoken). */
function boxStatus(status: PathTone | undefined, t: (k: string) => string): string {
  return status === 'danger' || status === 'warning' || status === 'pending' ? t(`ui.diagram.status_${status}`) : ''
}

/** Most paths enumerated per box (and per direction); beyond it the list is cut without a marker. */
const MAX_PATHS = 200

export interface GraphPath {
  /** Box ids from where the line enters to where it leaves. */
  nodes: string[]
  /** The line that joins each pair of neighbours in `nodes`: parallel lines are separate paths. */
  edges: GraphEdge[]
}

/**
 * Every path through `id`: each way back to where traffic enters, joined to each way
 * on to where it leaves, following the edges' direction. Walked over EDGES, not
 * boxes, so two lines joining the same pair of boxes are two paths. A box with no
 * edges has no paths. Capped, so a densely wired graph cannot make the list run away.
 */
export function pathsThrough(id: string, edges: readonly GraphEdge[]): GraphPath[] {
  const out = new Map<string, GraphEdge[]>()
  const into = new Map<string, GraphEdge[]>()
  for (const e of edges) {
    out.set(e.from, [...(out.get(e.from) ?? []), e])
    into.set(e.to, [...(into.get(e.to) ?? []), e])
  }
  const chains = (start: string, next: Map<string, GraphEdge[]>, far: (e: GraphEdge) => string): GraphPath[] => {
    const done: GraphPath[] = []
    const walk = (node: string, trail: GraphPath) => {
      if (done.length >= MAX_PATHS) return
      const more = (next.get(node) ?? []).filter(e => !trail.nodes.includes(far(e)))
      if (more.length === 0) { done.push(trail); return }
      for (const e of more) walk(far(e), { nodes: [...trail.nodes, far(e)], edges: [...trail.edges, e] })
    }
    walk(start, { nodes: [start], edges: [] })
    return done
  }
  const forward = chains(id, out, e => e.to)
  const backward = chains(id, into, e => e.from).map(c => ({ nodes: c.nodes.slice().reverse(), edges: c.edges.slice().reverse() }))
  const found: GraphPath[] = []
  for (const b of backward) for (const f of forward) {
    if (b.nodes.length === 1 && f.nodes.length === 1) continue
    found.push({ nodes: [...b.nodes, ...f.nodes.slice(1)], edges: [...b.edges, ...f.edges] })
    if (found.length >= MAX_PATHS) return found
  }
  return found
}

/**
 * Columns of labelled boxes joined by curved connectors, for questions of the
 * form "which of these goes with which" (entries, landings, exits). Selecting a
 * box keeps every connector through it and dims everything else; selecting it
 * again, or Escape, clears.
 *
 * TEXT EQUIVALENT. The connectors are SVG and hidden from assistive tech, so a
 * persistent, focusable "connections" list sits with the graph: it names every
 * connection of the selected box (both ends, the kind, a problem if there is one)
 * and each entry moves the selection to the other end. It is what a phone reads,
 * because on a phone the layout IS the list: columns stacked, no canvas.
 *
 * SIZE LIMITS. There is NO cap on boxes or edges: every box is drawn at a fixed size
 * and the canvas scrolls (the list layout scrolls with the page), so nothing is dropped
 * or summarised however many there are. The only cap is on PATHS: the connections of
 * one box, and the count in its name, stop at MAX_PATHS (200). Past that the list is cut
 * silently, with no "and more" row, so the counts are a lower bound for a graph that
 * dense. Work grows with the number of paths per box times the number of boxes (the
 * per-box counts are computed up front), so keep graphs to the hundreds of edges.
 * Edges naming a box that is not in `columns` are dropped without a message.
 *
 * IDS. `GraphEdge.id` is required and MUST be unique among edges, and `GraphBox.id`
 * unique across all columns. Edge ids key the lines list, decide which connectors are
 * highlighted and where parallel lines are offset from each other: two edges sharing
 * an id are highlighted together, drawn on top of each other and collide as React keys.
 * `columns` and `edges` should be stable references (memoised), since the layout and
 * path work is cached on their identity.
 *
 * Boxes are buttons with one tab stop and arrow-key movement (up and down within
 * a column, left and right across columns); Enter or Space selects.
 */
export function RelationGraph({ columns, edges, selectedId, onSelect, maxHeight = '32rem', layout = 'auto', className }: RelationGraphProps) {
  const { t } = useSpacedT()
  const narrow = useMediaQuery('(max-width: 639px)')
  const asList = layout === 'list' || (layout === 'auto' && narrow)
  const [own, setOwn] = useState<string | null>(null)
  const selected = selectedId !== undefined ? selectedId : own
  const select = (id: string | null) => { setOwn(id); onSelect?.(id) }

  const layoutInfo = useMemo(() => layoutGraph(columns), [columns])
  const boxById = useMemo(() => {
    const m: Record<string, GraphBox> = {}
    for (const c of columns) for (const b of c.boxes) m[b.id] = b
    return m
  }, [columns])
  const drawn = useMemo(() => edges.filter(e => layoutInfo.boxes[e.from] && layoutInfo.boxes[e.to]), [edges, layoutInfo])

  // WHO IS AFFECTED. Selecting a box lights every path through it, in both
  // directions: forward to where its traffic ends up (an entry reaches its landings
  // and their exits) and back to what feeds it (an exit reaches its landings and
  // entries). "This server went down: who does it hurt?" is answered by the paths,
  // not by the direct neighbours.
  const paths = useMemo(() => (selected === null ? [] : pathsThrough(selected, drawn)), [selected, drawn])
  const hotNodes = useMemo(() => new Set(paths.flatMap(p => p.nodes)), [paths])
  const hotEdgeIds = useMemo(() => new Set(paths.flatMap(p => p.edges.map(e => e.id))), [paths])
  const touching = (e: GraphEdge) => hotEdgeIds.has(e.id)
  const related = hotNodes
  // ONE definition of "lines through a box" for its name and for the announcement:
  // the number of paths through it (an end-to-end line each).
  const throughCount = useMemo(() => {
    const m: Record<string, number> = {}
    for (const c of columns) for (const b of c.boxes) m[b.id] = pathsThrough(b.id, drawn).length
    return m
  }, [columns, drawn])
  // Where each of several lines between the same two boxes is drawn, so they are visibly apart.
  const offsetOf = useMemo(() => {
    const groups = new Map<string, GraphEdge[]>()
    for (const e of drawn) groups.set(`${e.from}>${e.to}`, [...(groups.get(`${e.from}>${e.to}`) ?? []), e])
    const m: Record<string, number> = {}
    for (const g of groups.values()) g.forEach((e, i) => { m[e.id] = g.length === 1 ? 0 : (i - (g.length - 1) / 2) * Math.min(10, 28 / (g.length - 1)) })
    return m
  }, [drawn])

  const refs = useRef<Record<string, HTMLButtonElement | null>>({})
  const firstId = columns.find(c => c.boxes.length)?.boxes[0]?.id
  const tabStop = selected !== null && layoutInfo.boxes[selected] ? selected : firstId

  const onKey = (e: KeyboardEvent, id: string) => {
    const at = layoutInfo.boxes[id]
    if (e.key === 'Escape') { if (selected !== null) { e.stopPropagation(); select(null) } return }
    let target: string | undefined
    if (e.key === 'ArrowDown') target = columns[at.col].boxes[at.row + 1]?.id
    else if (e.key === 'ArrowUp') target = columns[at.col].boxes[at.row - 1]?.id
    else if (e.key === 'Home') target = columns[at.col].boxes[0]?.id
    else if (e.key === 'End') target = columns[at.col].boxes.at(-1)?.id
    else if ((e.key === 'ArrowRight' || e.key === 'ArrowLeft') && !asList) {
      const next = columns[at.col + (e.key === 'ArrowRight' ? 1 : -1)]
      if (next?.boxes.length) target = next.boxes[Math.min(at.row, next.boxes.length - 1)].id
    } else return
    e.preventDefault()
    if (target) refs.current[target]?.focus()
  }

  const selectedBox = selected !== null ? boxById[selected] : undefined
  const announce = selectedBox
    ? t('ui.diagram.graph_selected', { name: selectedBox.label, count: throughCount[selectedBox.id] ?? 0 })
    : ''

  const connections = (
    <section
      role="region"
      aria-label={`${t('ui.diagram.graph_label')} ${t('ui.diagram.graph_lines_suffix')}`}
      tabIndex={0}
      className="rounded-lg border bg-muted/30 px-3 py-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {selectedBox ? (
        <>
          <h3 className="mb-1 text-xs font-medium text-muted-foreground">{t('ui.diagram.graph_lines', { name: selectedBox.label })}</h3>
          {paths.length === 0 ? (
            <p className="text-muted-foreground">{t('ui.diagram.graph_lines_empty', { name: selectedBox.label })}</p>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {paths.map(path => {
                const tunnel = path.edges.some(e => e.style === 'dashed')
                const problems = [...new Set(path.edges.map(e => e.status).filter((x): x is 'danger' | 'warning' | 'pending' => x === 'danger' || x === 'warning' || x === 'pending'))]
                const names = path.edges.map(e => e.label).filter((x): x is string => !!x)
                return (
                  <li key={path.edges.map(e => e.id).join('>')} className="flex flex-wrap items-center gap-x-1 gap-y-0.5">
                    {path.nodes.map((id, i) => (
                      <span key={`${i}-${id}`} className="inline-flex items-center gap-1">
                        {i > 0 && <span aria-hidden="true" className="text-muted-foreground">→</span>}
                        <button
                          type="button"
                          aria-current={id === selected ? 'true' : undefined}
                          onClick={() => select(id)}
                          className={cn('rounded-md px-1 py-0.5 text-left underline-offset-2 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50', id === selected && 'font-semibold')}
                        >
                          {boxById[id].label}
                        </button>
                      </span>
                    ))}
                    {(names.length > 0 || tunnel || problems.length > 0) && (
                      <span className="ml-1 inline-flex flex-wrap gap-x-2 text-xs text-foreground/70">
                        {names.map(n => <span key={n} className="font-medium text-foreground">{n}</span>)}
                        {tunnel && <span>{t('ui.diagram.edge_dashed')}</span>}
                        {problems.map(p => <span key={p} className="font-medium text-foreground">{t(`ui.diagram.status_${p}`)}</span>)}
                      </span>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </>
      ) : (
        <p className="text-muted-foreground">{t('ui.diagram.graph_lines_none')}</p>
      )}
    </section>
  )

  const boxButton = (b: GraphBox, style: CSSProperties | undefined) => {
    const isSel = selected === b.id
    const dim = selected !== null && !related.has(b.id)
    return (
      <button
        key={b.id}
        ref={el => { refs.current[b.id] = el }}
        type="button"
        aria-pressed={isSel}
        aria-label={[b.label, b.sublabel, boxStatus(b.status, t), t('ui.diagram.graph_through', { count: throughCount[b.id] ?? 0 })].filter(Boolean).join(', ')}
        data-dim={dim ? 'true' : undefined}
        tabIndex={b.id === tabStop ? 0 : -1}
        onClick={() => select(isSel ? null : b.id)}
        onKeyDown={e => onKey(e, b.id)}
        style={style}
        className={cn(
          'flex min-w-0 items-center gap-2 rounded-lg border bg-background px-2.5 text-left outline-none transition-[opacity,border-color,box-shadow]',
          'hover:border-foreground/40 focus-visible:ring-3 focus-visible:ring-ring/50',
          style ? 'absolute' : 'h-11 w-full',
          isSel && 'border-brand ring-2 ring-brand/50',
          // Not-related boxes are softened with colour and a dashed edge, never with opacity:
          // opacity took their text to an unreadable grey in dark mode.
          dim && 'border-dashed bg-muted/50 text-foreground/70 [&_.text-muted-foreground]:text-foreground/70',
        )}
      >
        {b.landingColor && <span aria-hidden="true" className="h-6 w-1 shrink-0 rounded-full" style={landingColorStyle(b.landingColor)} />}
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm leading-tight font-medium">{b.label}</span>
          {b.sublabel && <span className="truncate font-mono text-[11px] leading-tight text-muted-foreground">{b.sublabel}</span>}
        </span>
        {b.status && <ToneDot tone={toneOfPath(b.status)} />}
      </button>
    )
  }

  if (asList) {
    return (
      <div data-slot="relation-graph" data-layout="list" role="group" aria-label={t('ui.diagram.graph_label')} className={cn('flex flex-col gap-3', className)}>
        {/* Sticky: a box deep in a long column is tapped far below the top, and its
            connections must stay in view next to it. */}
        <div className="sticky top-2 z-10 bg-background/95 backdrop-blur supports-backdrop-filter:bg-background/80">{connections}</div>
        {columns.map(c => (
          <section key={c.id} role="region" aria-label={c.title} className="flex flex-col gap-2">
            <h3 className="text-xs font-medium text-muted-foreground">{c.title} <span className="tabular-nums">{c.boxes.length}</span></h3>
            <div className="flex flex-col gap-2">{c.boxes.map(b => boxButton(b, undefined))}</div>
          </section>
        ))}
        <p role="status" className="sr-only">{announce}</p>
      </div>
    )
  }

  return (
    <div data-slot="relation-graph" data-layout="canvas" role="group" aria-label={t('ui.diagram.graph_label')} className={cn('flex flex-col gap-2', className)}>
      <div className="overflow-auto rounded-xl border bg-card" style={{ maxHeight }}>
        <div className="relative" style={{ width: layoutInfo.width, height: layoutInfo.height }}>
          <svg aria-hidden="true" width={layoutInfo.width} height={layoutInfo.height} className="pointer-events-none absolute inset-0">
            {drawn.map(e => {
              const tone = e.status ?? 'neutral'
              const hot = touching(e)
              const dim = selected !== null && !hot
              return (
                <path
                  key={e.id}
                  data-edge=""
                  data-edge-id={e.id}
                  data-style={e.style ?? 'solid'}
                  data-tone={tone}
                  data-dim={dim ? 'true' : undefined}
                  d={edgePath(layoutInfo.boxes[e.from], layoutInfo.boxes[e.to], offsetOf[e.id] ?? 0)}
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={hot ? 2.5 : 1.5}
                  strokeDasharray={e.style === 'dashed' ? '6 4' : undefined}
                  strokeLinecap="round"
                  className={cn(TONE_STYLE[toneOfPath(tone)].ink, 'transition-opacity')}
                  opacity={dim ? 0.1 : hot ? 1 : 0.55}
                />
              )
            })}
          </svg>
          {columns.map((c, col) => (
            <div key={c.id}>
              <div
                className="absolute truncate text-xs font-medium text-muted-foreground"
                style={{ left: PAD + col * (BOX_W + GAP_X), top: PAD, width: BOX_W, height: HEADER_H - 4 }}
              >
                {c.title} <span className="tabular-nums">{c.boxes.length}</span>
              </div>
              {c.boxes.map(b => {
                const r = layoutInfo.boxes[b.id]
                return boxButton(b, { left: r.x, top: r.y, width: r.w, height: r.h })
              })}
            </div>
          ))}
        </div>
      </div>
      {connections}
      <p role="status" className="sr-only">{announce}</p>
    </div>
  )
}
