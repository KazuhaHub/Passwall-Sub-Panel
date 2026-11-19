import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useSpacedT } from '@/lib/cjkSpacing'
import { cn } from '@/lib/utils'
import { landingColorStyle, type LandingColor } from './landingColor'
import type { PathTone } from './pathSummary'
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
export function edgePath(a: BoxRect, b: BoxRect): string {
  const forward = a.col <= b.col
  const x1 = forward ? a.x + a.w : a.x
  const x2 = forward ? b.x : b.x + b.w
  const y1 = a.y + a.h / 2
  const y2 = b.y + b.h / 2
  const mid = (x1 + x2) / 2
  return `M ${x1} ${y1} C ${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`
}

const STROKE: Record<PathTone, string> = {
  success: 'text-success', warning: 'text-warning', danger: 'text-destructive', neutral: 'text-muted-foreground',
}
const DOT: Record<PathTone, string> = {
  success: 'bg-success', warning: 'bg-warning', danger: 'bg-destructive', neutral: 'bg-muted-foreground/60',
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
 * and each entry moves the selection to the other end. It never truncates, and it
 * is what a phone reads, because on a phone the layout IS the list: columns
 * stacked, no canvas.
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
  const degree = useMemo(() => {
    const d: Record<string, number> = {}
    for (const e of drawn) { d[e.from] = (d[e.from] ?? 0) + 1; d[e.to] = (d[e.to] ?? 0) + 1 }
    return d
  }, [drawn])

  const touching = (e: GraphEdge) => selected !== null && (e.from === selected || e.to === selected)
  const related = useMemo(() => {
    const s = new Set<string>()
    if (selected === null) return s
    s.add(selected)
    for (const e of drawn) {
      if (e.from === selected) s.add(e.to)
      if (e.to === selected) s.add(e.from)
    }
    return s
  }, [drawn, selected])
  const selectedEdges = selected === null ? [] : drawn.filter(touching)

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
    ? t('ui.diagram.graph_selected', { name: selectedBox.label, count: selectedEdges.length })
    : ''

  const statusWord = (s: PathTone | undefined) => (s === 'danger' ? t('ui.diagram.status_danger') : s === 'warning' ? t('ui.diagram.status_warning') : '')

  const connections = (
    <section
      role="region"
      aria-label={`${t('ui.diagram.graph_label')} ${t('ui.diagram.graph_connections_suffix')}`}
      tabIndex={0}
      className="rounded-lg border bg-muted/30 px-3 py-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {selectedBox ? (
        <>
          <h3 className="mb-1 text-xs font-medium text-muted-foreground">{t('ui.diagram.graph_lines', { name: selectedBox.label })}</h3>
          {selectedEdges.length === 0 ? (
            <p className="text-muted-foreground">{t('ui.diagram.graph_lines_empty', { name: selectedBox.label })}</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {selectedEdges.map((e, i) => {
                const otherId = e.from === selected ? e.to : e.from
                const other = boxById[otherId]
                const problem = statusWord(e.status)
                return (
                  <li key={`${e.from}>${e.to}>${i}`} className="flex flex-wrap items-center gap-x-2">
                    <button
                      type="button"
                      aria-label={t('ui.diagram.graph_pick', { name: other.label })}
                      onClick={() => select(otherId)}
                      className="rounded-md px-1 py-0.5 text-left underline-offset-2 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
                    >
                      {boxById[e.from].label} → {boxById[e.to].label}
                    </button>
                    <span className="text-xs text-muted-foreground">
                      {t(e.style === 'dashed' ? 'ui.diagram.edge_dashed' : 'ui.diagram.edge_solid')}
                      {problem && <span className="font-medium text-foreground">{`，${problem}`}</span>}
                    </span>
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
        aria-label={`${b.label}${b.sublabel ? ` ${b.sublabel}` : ''}, ${t('ui.diagram.graph_connections', { count: degree[b.id] ?? 0 })}`}
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
          dim && 'opacity-35',
        )}
      >
        {b.landingColor && <span aria-hidden="true" className="h-6 w-1 shrink-0 rounded-full" style={landingColorStyle(b.landingColor)} />}
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm leading-tight font-medium">{b.label}</span>
          {b.sublabel && <span className="truncate font-mono text-[11px] leading-tight text-muted-foreground">{b.sublabel}</span>}
        </span>
        {b.status && <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', DOT[b.status])} />}
      </button>
    )
  }

  if (asList) {
    return (
      <div data-slot="relation-graph" data-layout="list" role="group" aria-label={t('ui.diagram.graph_label')} className={cn('flex flex-col gap-3', className)}>
        {connections}
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
            {drawn.map((e, i) => {
              const tone = e.status ?? 'neutral'
              const hot = touching(e)
              const dim = selected !== null && !hot
              return (
                <path
                  key={`${e.from}>${e.to}>${i}`}
                  data-edge=""
                  data-style={e.style ?? 'solid'}
                  data-tone={tone}
                  data-dim={dim ? 'true' : undefined}
                  d={edgePath(layoutInfo.boxes[e.from], layoutInfo.boxes[e.to])}
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={hot ? 2.5 : 1.5}
                  strokeDasharray={e.style === 'dashed' ? '6 4' : undefined}
                  strokeLinecap="round"
                  className={cn(STROKE[tone], 'transition-opacity')}
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
