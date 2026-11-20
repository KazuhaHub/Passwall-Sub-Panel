import { useMemo } from 'react'
import type { Server } from '@/api/servers'
import {
  Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxCollection, ComboboxContent, ComboboxEmpty, ComboboxGroup,
  ComboboxItem, ComboboxLabel, ComboboxList, ComboboxValue, useComboboxAnchor,
} from '@/components/ui/combobox'
import { useNodesT } from '../shared/useNodesT'

interface Group { value: string; items: Server[] }

const tagLabel = (s: Server) => [s.region, ...(s.tags ?? [])].filter(Boolean).join(' · ')
const haystack = (s: Server) => [s.name, s.region, ...(s.tags ?? [])].filter(Boolean).join(' ').toLowerCase()

/**
 * The servers in the order a person looks for them: the ones used for the last node first (in the order they were
 * used), then one group per "region · tags" label, and the servers with neither last, so a fleet that is mostly
 * untagged does not push the labelled ones down.
 */
export function groupServers(servers: readonly Server[], recent: readonly number[], recentLabel: string, untaggedLabel: string): Group[] {
  const used = recent.flatMap(id => servers.filter(s => s.id === id))
  const rest = servers.filter(s => !recent.includes(s.id))
  const byLabel = new Map<string, Server[]>()
  for (const s of [...rest].sort((a, b) => a.name.localeCompare(b.name))) {
    const label = tagLabel(s) || untaggedLabel
    byLabel.set(label, [...(byLabel.get(label) ?? []), s])
  }
  const labelled = [...byLabel.entries()].filter(([l]) => l !== untaggedLabel).sort(([a], [b]) => a.localeCompare(b))
  const untagged = byLabel.get(untaggedLabel)
  return [
    ...(used.length ? [{ value: recentLabel, items: used }] : []),
    ...labelled.map(([value, items]) => ({ value, items })),
    ...(untagged ? [{ value: untaggedLabel, items: untagged }] : []),
  ]
}

export interface EntryServerPickerProps {
  id: string
  servers: readonly Server[]
  recent: readonly number[]
  selected: readonly Server[]
  disabled: boolean
  onChange: (values: Server[]) => void
  'aria-describedby'?: string
}

/** The searchable multi-select of entry servers. Search matches the name, the region and the tags. */
export function EntryServerPicker({ id, servers, recent, selected, disabled, onChange, ...aria }: EntryServerPickerProps) {
  const t = useNodesT()
  const anchor = useComboboxAnchor()
  const groups = useMemo(() => groupServers(servers, recent, t('nodes.setup.recent'), t('nodes.setup.untagged')), [servers, recent, t])
  return (
    <Combobox multiple items={groups} value={selected as Server[]} disabled={disabled} onValueChange={v => onChange(v as Server[])}
      itemToStringLabel={(s: Server) => s.name} isItemEqualToValue={(a: Server, b: Server) => a.id === b.id}
      filter={(s: Server, query: string) => haystack(s).includes(query.trim().toLowerCase())}>
      <ComboboxChips ref={anchor}>
        <ComboboxValue>
          {(values: Server[]) => (
            <>
              {values.map(s => <ComboboxChip key={s.id}>{s.name}</ComboboxChip>)}
              <ComboboxChipsInput id={id} aria-describedby={aria['aria-describedby']} />
            </>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
        <ComboboxList>
          {(group: Group) => (
            <ComboboxGroup key={group.value} items={group.items}>
              <ComboboxLabel>{group.value}</ComboboxLabel>
              <ComboboxCollection>{(s: Server) => <ComboboxItem key={s.id} value={s}>{s.name}</ComboboxItem>}</ComboboxCollection>
            </ComboboxGroup>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
