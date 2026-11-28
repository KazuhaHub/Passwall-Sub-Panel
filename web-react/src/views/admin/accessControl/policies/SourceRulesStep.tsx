import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'

import { AccessBadge } from '@/components/access/AccessBadge'
import { Button } from '@/components/ui/button'
import { useDestSourceRules } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { RailStep } from './PolicyRail'

const P = 'admin:access_control.policies.rail.'
const nodeInbound = (id: number) => `/admin/nodes/${id}?tab=inbound`

/**
 * Step 0 of the rail: the per-inbound source rules (deny these countries or networks). They are checked before every policy
 * below and not even an exempt account gets past them, so they are listed here, but they are written in each node's Inbound tab.
 * Only the rules in force or reporting are listed; each row opens the node's tab where it is edited.
 */
export default function SourceRulesStep({ nodeName, onlyWhenActive = false, last = false }: { nodeName: (id: number) => string | undefined; onlyWhenActive?: boolean; last?: boolean }) {
  const { t } = useTranslation(['admin'])
  const rules = useDestSourceRules(useQueryScope())
  // A failed or missing read draws nothing: the policies below are the page, this step is a pointer.
  if (!rules.data) return null
  const items = rules.data.items.filter(r => r.state !== 'off' && (r.countries.length > 0 || r.cidrs.length > 0))
  // Beside the template gallery (no policies yet) the step is only worth its room when a rule is actually in force.
  if (onlyWhenActive && items.length === 0) return null
  return (
    <RailStep n={0} last={last}
      title={items.length > 0 ? t(`${P}source`, { count: items.length }) : t(`${P}source_none`)}
      caption={t(`${P}source_caption`)}
      actions={items.length === 0 ? <Button size="sm" variant="outline" render={<Link to="/admin/nodes" />}>{t(`${P}source_open_nodes`)}</Button> : undefined}>
      {items.length > 0
        ? (
          <>
            <ul className="grid gap-1.5">
              {items.map(r => {
                const name = nodeName(r.node_id) ?? t(`${P}source_unknown_node`, { id: r.node_id })
                return (
                  <li key={r.node_id}>
                    <Link to={nodeInbound(r.node_id)} aria-label={t(`${P}source_open_aria`, { name })} data-testid="source-rule-row"
                      className="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-muted px-3 py-1.5 text-sm outline-none hover:bg-muted/70 focus-visible:ring-3 focus-visible:ring-ring/50">
                      <span className="min-w-0 flex-1 font-medium [overflow-wrap:anywhere]">{name}</span>
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {[
                          r.countries.length > 0 ? t(`${P}source_countries`, { count: r.countries.length }) : '',
                          r.cidrs.length > 0 ? t(`${P}source_cidrs`, { count: r.cidrs.length }) : '',
                        ].filter(Boolean).join(' · ')}
                      </span>
                      <AccessBadge kind={r.state === 'report' ? 'would_deny' : 'block'} label={t(`${P}source_state_${r.state}`)} />
                    </Link>
                  </li>
                )
              })}
            </ul>
            <p className="mt-2 text-xs text-muted-foreground">{t(`${P}source_where`)}</p>
          </>
        )
        : <p className="text-sm text-muted-foreground">{t(`${P}source_empty`)}</p>}
    </RailStep>
  )
}
