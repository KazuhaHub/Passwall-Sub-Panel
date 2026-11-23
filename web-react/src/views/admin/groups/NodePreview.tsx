import { Link } from 'react-router'

import type { GroupNodePreview } from '@/api/groups'
import type { DraftPreviewState } from '@/hooks/useDraftPreview'
import { cn } from '@/lib/utils'
import { useGroupsT } from './useGroupsT'

/** More than this many matched nodes are cut off with "and N more", so a group that matches everything stays readable. */
const SHOWN = 12

/**
 * What the drafted filter reaches (ADR 0042 §1), live: how many nodes and lines match, each matched node as a
 * link to its page, the nodes the allowlist keeps out, and, for a group that has members, what the save changes
 * for them. Nothing while the first answer is pending, so an empty state never flashes. "No node" is warning
 * words (those members get an empty subscription); a failed or malformed read is "cannot be determined", never
 * an empty answer.
 */
export function NodePreview({ state, editing }: { state: DraftPreviewState<GroupNodePreview>; editing: boolean }) {
  const t = useGroupsT()
  const names = (items: string[]) => items.join(t('admin:nodes.line_labels.group_separator'))
  const data = state.status === 'ready' || state.status === 'loading' ? state.data : undefined

  let summary: string | null = null
  let tone: 'normal' | 'bad' | 'muted' = 'muted'
  if (state.status === 'error') summary = t('admin:groups.preview.unknown')
  else if (data) {
    if (data.nodes.length === 0) { summary = t('admin:groups.preview.none'); tone = 'bad' }
    else {
      summary = t('admin:groups.preview.nodes', {
        count: data.nodes.length,
        visible: data.nodes.reduce((n, x) => n + x.visible_lines, 0),
        total: data.nodes.reduce((n, x) => n + x.total_lines, 0),
      })
      tone = 'normal'
    }
  } else if (state.status === 'loading') summary = t('admin:groups.preview.loading')

  if (summary === null) return null
  const shown = data?.nodes.slice(0, SHOWN) ?? []
  const rest = (data?.nodes.length ?? 0) - shown.length
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3" aria-live="polite">
      <p data-testid="group-node-preview" className={cn('text-sm', tone === 'bad' ? 'font-medium text-destructive' : tone === 'normal' ? 'text-foreground' : 'text-muted-foreground')}>{summary}</p>
      {shown.length > 0 && (
        <ul className="flex flex-wrap gap-1.5">
          {shown.map(n => (
            <li key={n.id}>
              <Link to={`/admin/nodes/${n.id}`} title={t('admin:groups.preview.node_lines', { visible: n.visible_lines, total: n.total_lines })}
                className="inline-flex max-w-56 items-center gap-1.5 rounded-md border bg-background px-2 py-0.5 text-xs outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
                <span className="truncate">{n.name}</span>
                <span className="shrink-0 text-muted-foreground tabular-nums">{n.visible_lines}/{n.total_lines}</span>
              </Link>
            </li>
          ))}
          {rest > 0 && <li className="self-center text-xs text-muted-foreground">{t('admin:groups.preview.more_nodes', { count: rest })}</li>}
        </ul>
      )}
      {state.status === 'ready' && data && data.excluded.length > 0 && (
        <p data-testid="group-node-excluded" className="text-xs text-muted-foreground">
          {t('admin:groups.preview.excluded_allowlist', { names: names(data.excluded.map(n => n.name)) })}
        </p>
      )}
      {state.status === 'ready' && data && editing && data.members > 0 && (data.added.length > 0 || data.removed.length > 0) && (
        <p data-testid="group-node-change" className="text-xs font-medium text-destructive">
          {t('admin:groups.preview.change', { members: data.members, added: data.added.length, removed: data.removed.length })}
        </p>
      )}
    </div>
  )
}
