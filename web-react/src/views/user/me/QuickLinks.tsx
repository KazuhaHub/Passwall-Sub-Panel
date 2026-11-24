import type { QuickLink } from '@/api/me'
import { pushSnack } from '@/components/feedback/toasts'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { isHttpUrl } from './format'
import { QuickLinkIcon } from '@/components/QuickLinkIcon'
import { useMeT } from './useMeT'

type Open = (l: Pick<QuickLink, 'url' | 'new_window'>) => void

/** The ungrouped links first, then the groups in the order they first appear. */
function groupLinks(links: QuickLink[]): { name: string; links: QuickLink[] }[] {
  const byGroup = new Map<string, QuickLink[]>()
  for (const l of links) {
    const g = (l.group || '').trim()
    byGroup.set(g, [...(byGroup.get(g) ?? []), l])
  }
  const names = [...byGroup.keys()].sort((a, b) => (a === '' ? -1 : b === '' ? 1 : 0))
  return names.map(name => ({ name, links: byGroup.get(name)! }))
}

function LinkTile({ link, onOpen }: { link: QuickLink; onOpen: Open }) {
  const hl = link.highlight
  return (
    <button
      type="button" onClick={() => onOpen(link)}
      className={cn(
        // Centre the row when it is only a label; top-align when a description gives the tile a second line, so the icon
        // lines up with the label and not with the middle of two lines.
        'flex w-full min-w-0 gap-3 rounded-lg p-3 text-left outline-none ring-1 transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
        link.description ? 'items-start' : 'items-center',
        hl ? 'bg-brand/10 ring-brand/40 hover:bg-brand/15' : 'bg-card ring-foreground/10 hover:bg-muted',
      )}
    >
      <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
        {/* A tile with no icon still gets a glyph, so a mixed grid stays aligned. */}
        <QuickLinkIcon icon={link.icon || 'mui:Link'} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium" title={link.label}>{link.label}</span>
        {link.description && <span className="mt-0.5 block text-xs text-muted-foreground">{link.description}</span>}
      </span>
    </button>
  )
}

/**
 * The admin's shortcuts. They are web addresses typed into settings, so only http and https are opened: a
 * javascript: or data: link in a hostile or mistyped setting would otherwise run on click.
 */
export function QuickLinks({ links }: { links: QuickLink[] }) {
  const { t } = useMeT()
  if (links.length === 0) return null
  function open(l: Pick<QuickLink, 'url' | 'new_window'>) {
    if (!isHttpUrl(l.url)) { pushSnack(t('links.invalid_url'), 'error'); return }
    if (l.new_window) window.open(l.url, '_blank', 'noopener,noreferrer')
    else window.location.href = l.url
  }
  const groups = groupLinks(links)
  return (
    <Card>
      <CardHeader><CardTitle>{t('links.title')}</CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-4">
        {groups.map(g => (
          <section key={g.name || '__ungrouped'} aria-label={g.name || undefined} className="flex flex-col gap-2">
            {g.name && (
              <div className="flex items-center gap-2 text-xs font-semibold text-muted-foreground">
                <span>{g.name}</span><span className="h-px flex-1 bg-border" />
              </div>
            )}
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {g.links.map((l, i) => <LinkTile key={`${l.url}-${i}`} link={l} onOpen={open} />)}
            </div>
          </section>
        ))}
      </CardContent>
    </Card>
  )
}
