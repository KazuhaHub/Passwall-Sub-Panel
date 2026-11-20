import type { TFunction } from 'i18next'
import { targetOf, type DraftRoute } from './routeDraft'

const SHOWN = 3
const entriesOf = (text: string) => text.split(/[\n,，;；]+/).map(s => s.trim()).filter(Boolean)

/** What a route matches, as short pieces of text: list names, typed domains, CIDRs, the ports and the network. */
export function matchPieces(t: TFunction, row: DraftRoute, listNames: ReadonlyMap<number, string>): string[] {
  const typed = [...entriesOf(row.domains), ...entriesOf(row.cidrs)]
  const pieces = [
    ...row.listIds.map(id => listNames.get(id) ?? `#${id}`),
    ...typed.slice(0, SHOWN),
    ...(typed.length > SHOWN ? [t('nodes.routes.path.more', { count: typed.length - SHOWN }) as string] : []),
  ]
  const ports = row.ports.replace(/\s+/g, '')
  if (ports) pieces.push(t('nodes.routes.path.ports', { ports }) as string)
  if (row.network) pieces.push(row.network.toUpperCase())
  return pieces
}

/**
 * One line of where a route sends traffic: what it matches, an arrow, and where it leaves (this server, or the
 * target server). Built from the draft, so it follows what is typed before anything is saved.
 */
export function RoutePath({ t, row, listNames, serverName }: {
  t: TFunction
  row: DraftRoute
  listNames: ReadonlyMap<number, string>
  serverName: (panelId: number) => string
}) {
  const pieces = matchPieces(t, row, listNames)
  const target = targetOf(row.target)
  const to = !target ? t('nodes.routes.path.unset') : target.kind === 'local' ? t('nodes.routes.exit.local') : serverName(target.panel_id)
  return (
    <p data-testid="route-path" className="flex flex-wrap items-baseline gap-x-1.5 text-xs text-muted-foreground">
      <span className="min-w-0 break-words">{pieces.length > 0 ? pieces.join(' · ') : t('nodes.routes.path.nothing')}</span>
      <span aria-hidden="true">→</span>
      <span className="font-medium text-foreground">{to}</span>
    </p>
  )
}
