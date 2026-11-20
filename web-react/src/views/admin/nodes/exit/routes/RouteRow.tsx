import type { DestList } from '@/api/accessControl'
import type { RouteView } from '@/api/routes'
import { InfoTip } from '@/components/common/HelpTip'
import { IconButton } from '@/components/common/IconButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Field } from '@/components/form/Field'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { IconArrowDown, IconArrowUp, IconDelete } from '@/lib/icons'
import { useNodesT } from '../../shared/useNodesT'
import { RouteLinkStatus } from './RouteLinkStatus'
import { RoutePath } from './RoutePath'
import { targetKey, type DraftIssue, type DraftRoute } from './routeDraft'
import { issueText, noteText, type RouteLabeler } from './routeErrors'

export interface TargetOption { key: string; label: string }

/**
 * One route of the list: its name, the line of its path, what it matches, where it leaves, and what the server
 * said about it (the tunnel, the compiled entries, notes). Controlled: the section owns the draft.
 */
export function RouteRow({ index, count, row, info, issues, blocked, dirty, lists, targets, serverName, labelOf, onPatch, onMove, onRemove }: {
  index: number
  count: number
  row: DraftRoute
  /** What the server holds or the dry run answered for this route; none while it is new and unchecked. */
  info?: RouteView
  issues: DraftIssue[]
  blocked: boolean
  dirty: boolean
  lists: DestList[]
  targets: TargetOption[]
  serverName: (panelId: number) => string
  labelOf: RouteLabeler
  onPatch: (patch: Partial<DraftRoute>) => void
  onMove: (delta: -1 | 1) => void
  onRemove: () => void
}) {
  const t = useNodesT()
  const K = 'nodes.routes.'
  const listNames = new Map(lists.map(l => [l.id, l.name]))
  const toggleList = (id: number, on: boolean) => onPatch({ listIds: on ? [...row.listIds, id] : row.listIds.filter(x => x !== id) })
  return (
    <div data-testid="route-row" className="flex flex-col gap-3 rounded-lg border p-3">
      <div className="flex items-start gap-2">
        <span className="mt-7 min-w-7 text-sm font-medium">#{index + 1}</span>
        <Field className="flex-1" label={t(`${K}name`)}>
          <Input value={row.name} disabled={blocked} onChange={e => onPatch({ name: e.target.value })} />
        </Field>
        <div className="mt-6 flex">
          <IconButton label={t(`${K}move_up`)} size="icon-sm" data-testid={`route-up-${index}`} disabled={blocked || index === 0} onClick={() => onMove(-1)}><IconArrowUp /></IconButton>
          <IconButton label={t(`${K}move_down`)} size="icon-sm" data-testid={`route-down-${index}`} disabled={blocked || index === count - 1} onClick={() => onMove(1)}><IconArrowDown /></IconButton>
          <IconButton label={t(`${K}remove`)} size="icon-sm" data-testid={`route-remove-${index}`} disabled={blocked} onClick={onRemove}><IconDelete /></IconButton>
        </div>
      </div>
      <RoutePath t={t} row={row} listNames={listNames} serverName={serverName} />

      <Field label={t(`${K}lists`)} hint={t(`${K}lists_hint`)}>
        {p => (
          <div role="group" aria-labelledby={p.labelId} className="flex max-h-44 flex-col gap-0.5 overflow-auto rounded-lg border p-1.5">
            {lists.map(l => (
              <label key={l.id} className="flex items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-muted">
                <Checkbox disabled={blocked} checked={row.listIds.includes(l.id)} onCheckedChange={v => toggleList(l.id, v === true)} />
                <span>{t(`${K}list_option`, { name: l.name, kind: t(`admin:access_control.policies.list_kind.${l.kind}`), entries: l.entry_count })}</span>
              </label>
            ))}
          </div>
        )}
      </Field>
      <Field label={t(`${K}domains`)} hint={t(`${K}domains_hint`)}>
        <Textarea rows={2} disabled={blocked} value={row.domains} placeholder={'domain:example.com\nfull:www.example.com\nkeyword:ads'} onChange={e => onPatch({ domains: e.target.value })} />
      </Field>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_9rem]">
        <Field label={t(`${K}cidrs`)}>
          <Textarea rows={2} disabled={blocked} value={row.cidrs} onChange={e => onPatch({ cidrs: e.target.value })} />
        </Field>
        <Field label={t(`${K}ports`)} hint={t(`${K}ports_hint`)}>
          <Input disabled={blocked} value={row.ports} placeholder="25,465,587" onChange={e => onPatch({ ports: e.target.value })} />
        </Field>
        <Field label={t(`${K}network`)}>
          <NativeSelect disabled={blocked} value={row.network} onChange={e => onPatch({ network: e.target.value as DraftRoute['network'] })}>
            <option value="">{t(`${K}network_any`)}</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </NativeSelect>
        </Field>
      </div>

      <div className="flex items-start gap-1">
        <Field className="flex-1" label={t(`${K}target.label`)} hint={row.target === targetKey.local ? t(`${K}target.local_hint`) : undefined}>
          <NativeSelect disabled={blocked} value={row.target} onChange={e => onPatch({ target: e.target.value })}>
            <option value="">{t(`${K}target.choose`)}</option>
            {targets.map(o => <option key={o.key} value={o.key}>{o.label}</option>)}
          </NativeSelect>
        </Field>
        <span className="mt-6"><InfoTip text={t(`${K}help.local`)} /></span>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {info?.link && <RouteLinkStatus link={info.link} testId="route-link" />}
        {info && <span data-testid="route-entries" className="text-xs text-muted-foreground">{t(`${K}entries`, { count: info.entries })}</span>}
        {dirty
          ? <span data-testid="route-sent" data-state="unsaved"><StatusBadge tone="neutral" quiet={false} label={t(`${K}sent.unsaved`)} /></span>
          : info && (
            <span data-testid="route-sent" data-state={info.sent ? 'sent' : 'not_sent'}>
              <StatusBadge tone={info.sent ? 'neutral' : 'warning'} quiet={false} label={t(info.sent ? `${K}sent.sent` : `${K}sent.not_sent`)} />
            </span>
          )}
      </div>
      {(info?.notes ?? []).map((n, j) => (
        <p key={j} data-testid="route-note" data-code={n.code} className="text-xs text-warning-ink">{noteText(t, n)}</p>
      ))}
      {issues.map((x, j) => <p key={j} data-testid="route-issue" className="text-xs text-destructive">{issueText(t, x, labelOf)}</p>)}
    </div>
  )
}
