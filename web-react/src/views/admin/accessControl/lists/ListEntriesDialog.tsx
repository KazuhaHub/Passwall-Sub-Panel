import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { AccessBadge } from '@/components/access/AccessBadge'
import { AsyncButton } from '@/components/common/AsyncButton'
import { DetailDialog, type DetailNav } from '@/components/data/DetailDialog'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { Skeleton } from '@/components/ui/skeleton'
import { IconSearch } from '@/lib/icons'
import { useDestList, useDestPolicies, useRefreshDestList } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError } from '../destErrors'
import { listStatus } from './listStatus'

const L = 'admin:access_control.lists.'

export interface ListEntriesDialogProps {
  /** The list the URL names, or null (closed). */
  listId: number | null
  onClose: () => void
  /** Opens the list's edit sheet (lists tab). */
  onEdit: (id: number) => void
  /** Opens the test sheet prefilled with an entry. */
  onTest: (target: string) => void
  /** Previous / next over the lists. */
  nav?: DetailNav
}

/**
 * A list's contents (S6 sheet): its figures and the first 200 canonical entries, in the order the server
 * returned them. The search is the component's own state and never reaches the URL (R12); it covers the
 * 200 shown, not the whole list.
 */
export default function ListEntriesDialog({ listId, onClose, onEdit, onTest, nav }: ListEntriesDialogProps) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const open = listId !== null
  const q = useDestList(scope, listId, { enabled: open })
  const policies = useDestPolicies(scope, { enabled: open })
  const refresh = useRefreshDestList(scope)
  const [search, setSearch] = useState('')
  // Stepping to another list starts a fresh search: the old words were about the old list's entries.
  const [searchedList, setSearchedList] = useState(listId)
  if (searchedList !== listId) { setSearchedList(listId); setSearch('') }

  const list = q.data?.list
  const enabledUsed = !!list && list.used_by.some(u => [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].some(p => p.id === u.id && p.state !== 'off'))
  const st = list ? listStatus(list, enabledUsed) : null
  const needle = search.trim().toLowerCase()
  const samples = q.data?.samples ?? []
  const shown = samples.filter(e => !needle || e.toLowerCase().includes(needle))
  const num = new Intl.NumberFormat(i18n.language)

  const source = !list ? undefined : list.kind === 'remote'
    ? t(`${L}sheet.source`, { url: list.source_url ?? '' })
    : list.kind === 'geosite' ? t(`${L}sheet.category`, { name: list.geosite_category ?? '' }) : t(`${L}sheet.custom`)

  const actions = list && list.kind !== 'geosite' ? (
    <>
      <Button variant="outline" size="sm" onClick={() => onEdit(list.id)}>{t(`${L}sheet.edit`)}</Button>
      {list.kind === 'remote' && (
        <AsyncButton variant="outline" size="sm" onClick={async () => {
          try { await refresh.mutateAsync(list.id); pushSnack(t(`${L}snack.refresh_started`, { name: list.name }), 'info') } catch (err) { reportDestError(t, err) }
        }}>{t(`${L}sheet.refresh`)}</AsyncButton>
      )}
    </>
  ) : undefined

  return (
    <DetailDialog open={open} size="md" nav={nav} onClose={() => { setSearch(''); onClose() }} title={list?.name ?? ''} description={source}
      status={st ? <span data-state={st.key}><AccessBadge kind={st.tone} label={t(`${L}state.${st.key}`)} /></span> : undefined}
      actions={actions}>
      {q.isPending && open && <Skeleton className="h-40 w-full" />}
      {q.isError && <ErrorState compact title={t(`${L}sheet.load_failed`)} reason={q.error} retry={() => q.refetch()} />}
      {list && (
        <div className="flex flex-col gap-3">
          <p className="text-sm tabular-nums">{t(`${L}sheet.counts`, { entries: num.format(list.entry_count), regexps: num.format(list.regexp_count) })}</p>
          <p className="text-[13px] text-muted-foreground">{t(`${L}sheet.first200`, { count: num.format(list.entry_count) })}</p>
          <InputGroup>
            <InputGroupAddon><IconSearch aria-hidden /></InputGroupAddon>
            <InputGroupInput value={search} onChange={e => setSearch(e.target.value)} placeholder={t(`${L}sheet.search`)} aria-label={t(`${L}sheet.search`)} />
          </InputGroup>
          {shown.length === 0 && <p className="text-sm text-muted-foreground">{t(`${L}sheet.${samples.length === 0 ? 'none' : 'no_match'}`)}</p>}
          <ul className="m-0 list-none p-0">
            {shown.map(e => (
              <li key={e} className="flex items-center justify-between gap-2 border-b py-1">
                <span className="min-w-0 truncate font-mono text-[13px]" title={e}>{e}</span>
                <Button variant="outline" size="sm" className="shrink-0" onClick={() => onTest(e)}>{t(`${L}sheet.test`)}</Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </DetailDialog>
  )
}
