import { useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { landingColorVar } from './landingColor'
import type { PathTone } from './pathSummary'
import '@/styles/data-tokens.css'

export interface GraphBox {
  id: string
  label: string
  sublabel?: string
  status?: PathTone
  /** Landing node id: draws the colour bar that matches LandingColorBar elsewhere. */
  colorId?: string | number
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
  /** CSS max-height of the scrolling frame. Boxes never shrink: a long column scrolls. */
  maxHeight?: string
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

/**
 * Columns of labelled boxes joined by curved connectors, for questions of the
 * form "which of these goes with which" (entries, landings, exits). Selecting a
 * box keeps every connector through it and dims everything else; selecting it
 * again, or Escape, clears. Boxes are buttons with one tab stop and arrow-key
 * movement (up and down within a column, left and right across columns). The
 * connectors are SVG and hidden from assistive tech: what a box connects to is
 * spoken when it is selected and counted in its name.
 */
export function RelationGraph({ columns, edges, selectedId, onSelect, maxHeight = '32rem', className }: RelationGraphProps) {
  const { t } = useTranslation('common')
  const [own, setOwn] = useState<string | null>(null)
  const selected = selectedId !== undefined ? selectedId : own
  const select = (id: string | null) => { setOwn(id); onSelect?.(id) }

  const layout = useMemo(() => layoutGraph(columns), [columns])
  const labels = useMemo(() => {
    const m: Record<string, string> = {}
    for (const c of columns) for (const b of c.boxes) m[b.id] = b.label
    return m
  }, [columns])
  const drawn = useMemo(() => edges.filter(e => layout.boxes[e.from] && layout.boxes[e.to]), [edges, layout])
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

  const refs = useRef<Record<string, HTMLButtonElement | null>>({})
  const firstId = columns.find(c => c.boxes.length)?.boxes[0]?.id
  const tabStop = selected !== null && layout.boxes[selected] ? selected : firstId

  const onKey = (e: KeyboardEvent, id: string) => {
    const at = layout.boxes[id]
    if (e.key === 'Escape') { if (selected !== null) { e.stopPropagation(); select(null) } return }
    let target: string | undefined
    if (e.key === 'ArrowDown') target = columns[at.col].boxes[at.row + 1]?.id
    else if (e.key === 'ArrowUp') target = columns[at.col].boxes[at.row - 1]?.id
    else if (e.key === 'Home') target = columns[at.col].boxes[0]?.id
    else if (e.key === 'End') target = columns[at.col].boxes.at(-1)?.id
    else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      const next = columns[at.col + (e.key === 'ArrowRight' ? 1 : -1)]
      if (next?.boxes.length) target = next.boxes[Math.min(at.row, next.boxes.length - 1)].id
    } else return
    e.preventDefault()
    if (target) refs.current[target]?.focus()
  }

  const connectedNames = selected === null ? [] : [...related].filter(x => x !== selected).map(x => labels[x])
  const announce = selected === null
    ? ''
    : `${t('ui.diagram.graph_selected', { name: labels[selected] ?? selected, count: connectedNames.length })}${connectedNames.length ? `: ${connectedNames.slice(0, 8).join(', ')}${connectedNames.length > 8 ? '…' : ''}` : ''}`

  return (
    <div
      data-slot="relation-graph"
      role="group"
      aria-label={t('ui.diagram.graph_label')}
      className={cn('overflow-auto rounded-xl border bg-card', className)}
      style={{ maxHeight }}
    >
      <div className="relative" style={{ width: layout.width, height: layout.height }}>
        <svg aria-hidden="true" width={layout.width} height={layout.height} className="pointer-events-none absolute inset-0">
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
                d={edgePath(layout.boxes[e.from], layout.boxes[e.to])}
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
              const r = layout.boxes[b.id]
              const isSel = selected === b.id
              const dim = selected !== null && !related.has(b.id)
              const n = degree[b.id] ?? 0
              return (
                <button
                  key={b.id}
                  ref={el => { refs.current[b.id] = el }}
                  type="button"
                  aria-pressed={isSel}
                  aria-label={`${b.label}${b.sublabel ? ` ${b.sublabel}` : ''}, ${t('ui.diagram.graph_connections', { count: n })}`}
                  data-dim={dim ? 'true' : undefined}
                  tabIndex={b.id === tabStop ? 0 : -1}
                  onClick={() => select(isSel ? null : b.id)}
                  onKeyDown={e => onKey(e, b.id)}
                  style={{ left: r.x, top: r.y, width: r.w, height: r.h }}
                  className={cn(
                    'absolute flex min-w-0 items-center gap-2 rounded-lg border bg-background px-2.5 text-left outline-none transition-[opacity,border-color,box-shadow]',
                    'hover:border-foreground/40 focus-visible:ring-3 focus-visible:ring-ring/50',
                    isSel && 'border-brand ring-2 ring-brand/50',
                    dim && 'opacity-35',
                  )}
                >
                  {b.colorId !== undefined && (
                    <span aria-hidden="true" className="h-6 w-1 shrink-0 rounded-full" style={{ backgroundColor: landingColorVar(b.colorId) }} />
                  )}
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-sm leading-tight font-medium">{b.label}</span>
                    {b.sublabel && <span className="truncate font-mono text-[11px] leading-tight text-muted-foreground">{b.sublabel}</span>}
                  </span>
                  {b.status && (
                    <span
                      aria-hidden="true"
                      className={cn('size-2 shrink-0 rounded-full', b.status === 'success' ? 'bg-success' : b.status === 'warning' ? 'bg-warning' : b.status === 'danger' ? 'bg-destructive' : 'bg-muted-foreground/60')}
                    />
                  )}
                </button>
              )
            })}
          </div>
        ))}
      </div>
      <p role="status" className="sr-only">{announce}</p>
    </div>
  )
}
