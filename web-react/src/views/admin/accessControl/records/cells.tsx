import { useTranslation } from 'react-i18next'

import type { DestHitItem } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { cn } from '@/lib/utils'

export const R = 'admin:access_control.records.'

/** The destination as a hit names it: host and port. */
export const destOf = (h: Pick<DestHitItem, 'dest' | 'port'>) => (h.port > 0 ? `${h.dest}:${h.port}` : h.dest)

/** What every cell needs to name an account, a rule or an exit server; built once by the tab. */
export interface Namers {
  num: Intl.NumberFormat
  userName: (h: Pick<DestHitItem, 'user_id' | 'upn'>) => string
  ruleName: (h: Pick<DestHitItem, 'source' | 'source_name' | 'source_kind'>) => string | null
  exitLabel: (h: Pick<DestHitItem, 'exit_panel_id' | 'exit_panel_name'>) => string
  onOpenUser: (id: number) => void
  onPickSource: (source: string) => void
}

/** The action a hit records, in the closed table's words: a block, or what a report-only rule would have blocked. */
export function ActionBadge({ action }: { action: DestHitItem['action'] }) {
  const { t } = useTranslation(['admin'])
  return action === 'observe'
    ? <AccessBadge kind="would_deny" label={t(`${R}rule.would_block`)} testId="hit-action" />
    : <AccessBadge kind="block" label={t(`${R}rule.blocked`)} testId="hit-action" />
}

const LINK = 'rounded-sm text-left text-[13px] [overflow-wrap:anywhere] outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50'

export function RuleLink({ hit, ruleName, onPickSource }: { hit: DestHitItem } & Pick<Namers, 'ruleName' | 'onPickSource'>) {
  const { t } = useTranslation(['admin'])
  const name = ruleName(hit)
  if (name === null) {
    return <span className="text-[13px] text-muted-foreground italic">{t(`${R}rule.${hit.source.startsWith('s') ? 'deleted_source_rule' : 'deleted_policy'}`)}</span>
  }
  return (
    <button type="button" className={LINK} onClick={() => onPickSource(hit.source)} aria-label={t(`${R}rule.filter_by`, { name })}>
      {name}
    </button>
  )
}

export function UserCell({ hit, userName, onOpenUser }: { hit: Pick<DestHitItem, 'user_id' | 'upn'> } & Pick<Namers, 'userName' | 'onOpenUser'>) {
  if (!hit.upn) return <span className="text-[13px] text-muted-foreground italic">{userName(hit)}</span>
  return (
    <button type="button" className={LINK} onClick={() => onOpenUser(hit.user_id)} aria-label={hit.upn}>
      {hit.upn}
    </button>
  )
}

/** One ellipsed line; the whole destination is in `title`. */
export function Dest({ hit, className }: { hit: DestHitItem; className?: string }) {
  const text = destOf(hit)
  return <span data-testid="hit-dest" title={text} className={cn('block min-w-0 truncate font-mono text-[13px]', className)}>{text}</span>
}
