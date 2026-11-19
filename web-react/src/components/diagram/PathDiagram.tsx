import { Fragment, useEffect, useRef, useState, type ComponentType } from 'react'
import {
  ChevronRightIcon, CloudIcon, ExternalLinkIcon, GlobeIcon, LogOutIcon, MapPinIcon, ServerIcon, UserRoundIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { landingColorVar } from './landingColor'
import { describePath, edgeText, type PathEdge, type PathNode, type PathNodeKind, type PathTone } from './pathSummary'
import '@/styles/data-tokens.css'

export type { PathEdge, PathNode, PathNodeKind, PathTone } from './pathSummary'

const ICONS: Record<PathNodeKind, ComponentType<{ className?: string }>> = {
  user: UserRoundIcon,
  entry: ServerIcon,
  cdn: CloudIcon,
  external: ExternalLinkIcon,
  landing: MapPinIcon,
  exit: LogOutIcon,
  internet: GlobeIcon,
}

// Whole class strings, because Tailwind only emits classes it can read in the source.
const TONE: Record<PathTone, { line: string; text: string; dot: string }> = {
  success: { line: 'border-success', text: 'text-success', dot: 'bg-success' },
  warning: { line: 'border-warning', text: 'text-warning', dot: 'bg-warning' },
  danger: { line: 'border-destructive', text: 'text-destructive', dot: 'bg-destructive' },
  neutral: { line: 'border-muted-foreground/50', text: 'text-muted-foreground', dot: 'bg-muted-foreground/60' },
}

export interface PathDiagramProps {
  nodes: readonly PathNode[]
  /** edges[i] joins nodes[i] and nodes[i + 1]; a missing one is drawn as a plain solid edge. */
  edges?: readonly PathEdge[]
  /** `sm` fits one table row (the path cell); `md` is the detail-page diagram. */
  size?: 'sm' | 'md'
  /**
   * `auto` lays `md` out in a row when it fits and in a column when it does not,
   * measured on the diagram's own box (contain-inline-size keeps its content from
   * widening that box, which would make it always "fit"), so it is right inside a narrow sheet as
   * well as on a phone. `sm` is always one line.
   */
  orientation?: 'auto' | 'horizontal' | 'vertical'
  /** Replaces the generated sentence that is the diagram's accessible name. */
  summary?: string
  className?: string
}

// How wide a row of md nodes needs to be: node box + edge, per hop.
const NODE_W = 96
const EDGE_W = 48

function useFits(needed: number, enabled: boolean) {
  const ref = useRef<HTMLDivElement>(null)
  const [fits, setFits] = useState(true)
  useEffect(() => {
    const el = ref.current
    if (!enabled || !el || typeof ResizeObserver === 'undefined') return
    const measure = () => { const w = el.clientWidth; setFits(w === 0 || w >= needed) }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [needed, enabled])
  return [ref, fits] as const
}

function StatusDot({ tone, className }: { tone: PathTone; className?: string }) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-full', TONE[tone].dot, className)} />
}

/**
 * The picture of "how traffic gets there" (plan 3.6): nodes joined by edges,
 * solid for 端口转发 and dashed for 加密隧道, with a status colour and a short
 * label (a latency, "等待生效") on each edge. HTML and CSS borders rather than
 * an SVG canvas, so it reflows, follows the text size and takes its colours
 * from the theme variables in light and dark.
 *
 * It is a single image to assistive tech whose name is a generated sentence
 * (see describePath); the picture never carries information the text does not.
 * Every edge also has a `title`, so a hover or a copy gets the words too.
 */
export function PathDiagram({ nodes, edges = [], size = 'md', orientation = 'auto', summary, className }: PathDiagramProps) {
  const { t } = useTranslation('common')
  const needed = nodes.length * NODE_W + Math.max(0, nodes.length - 1) * EDGE_W
  const [ref, fits] = useFits(needed, size === 'md' && orientation === 'auto')
  const vertical = size === 'md' && (orientation === 'vertical' || (orientation === 'auto' && !fits))
  const sentence = summary ?? describePath(nodes, edges, t)
  const edgeAt = (i: number): PathEdge => edges[i] ?? { style: 'solid' }

  return (
    <div
      ref={ref}
      role="img"
      aria-label={sentence}
      data-slot="path-diagram"
      data-size={size}
      data-orientation={vertical ? 'vertical' : 'horizontal'}
      className={cn(size === 'sm' ? 'inline-flex max-w-full min-w-0 align-middle' : 'w-full min-w-0 contain-inline-size', className)}
    >
      <div
        aria-hidden="true"
        className={cn(
          size === 'sm'
            ? 'flex min-w-0 items-center gap-1 text-xs leading-none whitespace-nowrap'
            : vertical ? 'flex flex-col items-stretch' : 'flex items-start justify-center',
        )}
      >
        {nodes.map((node, i) => (
          <Fragment key={node.id}>
            {size === 'sm'
              ? <SmallNode node={node} end={i === 0 || i === nodes.length - 1} />
              : <FullNode node={node} vertical={vertical} />}
            {i < nodes.length - 1 && (
              size === 'sm'
                ? <SmallEdge edge={edgeAt(i)} />
                : <FullEdge edge={edgeAt(i)} vertical={vertical} />
            )}
          </Fragment>
        ))}
      </div>
    </div>
  )
}

function NodeIcon({ node, className }: { node: PathNode; className?: string }) {
  const Icon = ICONS[node.kind]
  return <Icon className={className} />
}

// ---- sm: inside a table row --------------------------------------------------

function SmallNode({ node, end }: { node: PathNode; end: boolean }) {
  // The origin and the internet are icon-only: they are the same on every row, so
  // spelling them out would spend the row's width on words that tell nothing.
  const iconOnly = end && (node.kind === 'user' || node.kind === 'internet')
  return (
    <span
      data-node={node.id}
      title={node.label}
      className="inline-flex min-w-0 items-center gap-1"
    >
      <NodeIcon node={node} className="size-3.5 shrink-0 text-muted-foreground" />
      {!iconOnly && (
        <>
          <span className="max-w-[9rem] truncate font-medium text-foreground">{node.label}</span>
          {node.sublabel && <span className="font-mono text-[10px] text-muted-foreground">{node.sublabel}</span>}
        </>
      )}
      {node.status && <StatusDot tone={node.status} />}
    </span>
  )
}

function SmallEdge({ edge }: { edge: PathEdge }) {
  const { t } = useTranslation('common')
  const tone = edge.status ?? 'neutral'
  const word = edgeText(edge, t)
  return (
    <span
      data-edge={edge.style}
      data-tone={tone}
      title={word || undefined}
      className="inline-flex shrink-0 items-center gap-0.5"
    >
      {edge.label && (
        <span className="inline-flex items-center gap-0.5 text-[10px] text-foreground">
          {edge.status && <StatusDot tone={tone} className="size-1.5" />}
          {edge.label}
        </span>
      )}
      <span className={cn('w-4 border-t-2', edge.style === 'dashed' ? 'border-dashed' : 'border-solid', TONE[tone].line)} />
      <ChevronRightIcon className={cn('-ml-2 size-3', TONE[tone].text)} />
    </span>
  )
}

// ---- md: the detail-page diagram ---------------------------------------------

function FullNode({ node, vertical }: { node: PathNode; vertical: boolean }) {
  const ring = node.colorId !== undefined
  return (
    <div
      data-node={node.id}
      title={node.label}
      className={cn('flex min-w-0 shrink-0 gap-2', vertical ? 'w-full flex-row items-center' : 'w-24 flex-col items-center text-center')}
    >
      <span
        className="relative grid size-9 shrink-0 place-items-center rounded-full border bg-card"
        style={ring ? { borderColor: landingColorVar(node.colorId!), borderWidth: 2 } : undefined}
      >
        <NodeIcon node={node} className="size-4 text-foreground" />
        {node.status && <StatusDot tone={node.status} className="absolute -top-0.5 -right-0.5 size-2.5 ring-2 ring-background" />}
      </span>
      <span className={cn('flex min-w-0 flex-col', vertical ? 'items-start' : 'w-full items-center')}>
        <span className="w-full truncate text-sm font-medium">{node.label}</span>
        {node.sublabel && <span className="w-full truncate font-mono text-xs text-muted-foreground">{node.sublabel}</span>}
      </span>
    </div>
  )
}

function FullEdge({ edge, vertical }: { edge: PathEdge; vertical: boolean }) {
  const { t } = useTranslation('common')
  const tone = edge.status ?? 'neutral'
  const word = edgeText(edge, t)
  const dashed = edge.style === 'dashed'
  const label = edge.label && (
    <span className="inline-flex items-center gap-1 text-xs text-foreground">
      {edge.status && <StatusDot tone={tone} className="size-1.5" />}
      {edge.label}
    </span>
  )
  if (vertical) {
    return (
      <div data-edge={edge.style} data-tone={tone} title={word || undefined} className="flex h-9 items-center gap-2 pl-[1.0625rem]">
        <span className="flex h-full flex-col items-center">
          <span className={cn('flex-1 border-l-2', dashed ? 'border-dashed' : 'border-solid', TONE[tone].line)} />
          <ChevronRightIcon className={cn('-mt-1.5 size-3.5 rotate-90', TONE[tone].text)} />
        </span>
        {label}
      </div>
    )
  }
  return (
    <div
      data-edge={edge.style}
      data-tone={tone}
      title={word || undefined}
      // 1.125rem is half the 36px icon: the line meets the icons at their middle, and the label sits above it.
      className="flex min-w-14 flex-1 flex-col items-center gap-0.5 px-1 pt-0.5"
    >
      <span className="min-h-4 text-center leading-4">{label}</span>
      <span className="flex w-full items-center">
        <span className={cn('flex-1 border-t-2', dashed ? 'border-dashed' : 'border-solid', TONE[tone].line)} />
        <ChevronRightIcon className={cn('-ml-2 size-3.5', TONE[tone].text)} />
      </span>
    </div>
  )
}
