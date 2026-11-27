import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestPolicyRef } from '@/api/accessControl'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

const LINK = 'min-w-0 truncate text-left underline-offset-2 outline-none hover:underline focus-visible:underline'

/** The "N policies" cell: a button that opens the list of who uses the list, each a link to its editor. */
export default function UsedByPopover({ usedBy, onOpenPolicy, onOpenAllowlist }: {
  usedBy: DestPolicyRef[]
  onOpenPolicy: (id: number) => void
  /** An allowlist group that uses the list: its card on the allowlist tab. */
  onOpenAllowlist: (groupId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const [open, setOpen] = useState(false)
  const policies = usedBy.filter(u => u.kind === 'policy')
  const groups = usedBy.filter(u => u.kind === 'group')
  // A node's route has no page of its own here: it is named, not linked.
  const routes = usedBy.filter(u => u.kind === 'route')
  if (usedBy.length === 0) return <span>{t('admin:access_control.lists.usage.unused')}</span>
  const summary = [
    policies.length > 0 ? t('admin:access_control.lists.usage.policies', { count: policies.length }) : null,
    groups.length > 0 ? t('admin:access_control.lists.usage.groups', { count: groups.length }) : null,
    routes.length > 0 ? t('admin:access_control.lists.usage.routes', { count: routes.length }) : null,
  ].filter(Boolean).join(' · ')
  const go = (open: () => void) => { setOpen(false); open() }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger render={<button type="button" className="max-w-full truncate text-left underline-offset-2 hover:underline focus-visible:underline" />}>
        {summary}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto max-w-80 min-w-48">
        <ul className="m-0 grid list-none gap-1 p-0">
          {policies.map(u => (
            <li key={`${u.kind}:${u.id}`} className="flex min-w-0">
              <button type="button" className={LINK} title={u.name || String(u.id)} onClick={() => go(() => onOpenPolicy(u.id))}>{u.name || String(u.id)}</button>
            </li>
          ))}
          {groups.map(u => (
            <li key={`${u.kind}:${u.id}`} className="flex min-w-0">
              <button type="button" className={LINK} title={u.name || String(u.id)} onClick={() => go(() => onOpenAllowlist(u.id))}>{u.name || String(u.id)}</button>
            </li>
          ))}
          {routes.map(u => (
            <li key={`${u.kind}:${u.id}`} data-testid="used-by-route" className="truncate">{u.name || String(u.id)}</li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  )
}
