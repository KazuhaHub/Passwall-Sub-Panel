import { useState } from 'react'
import { getServerPorts, suggestPort } from '@/api/fronts'
import type { NodeSetupEntry } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { RelayLine } from '@/api/types'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { CDN_DEFAULT_PORT } from '../lines/lineModel'
import { useNodesT } from '../shared/useNodesT'
import { EntryCard } from './EntryCard'
import { EntryServerPicker } from './EntryServerPicker'
import { newSetupRelay, type SetupField, type SetupRelay } from './setupModel'

export interface EntriesStepProps {
  servers: Server[]
  recent: number[]
  entries: SetupRelay[]
  onChange: (entries: SetupRelay[]) => void
  canTunnel: boolean
  useDirect: boolean
  onDirectChange: (useDirect: boolean) => void
  hostOf: (server: Server) => string
  newKey: () => string
  randomSite: () => string
  tcpTLS: boolean
  onBusyChange: (busy: boolean) => void
  /** The wizard tried to go on: show every field's problem, not only those of fields already left. */
  showErrors?: boolean
}

/**
 * Step 2 of the create wizard: whether the direct line is published, which servers are entries, and each
 * entry's fields. Choosing a server fills the entry's name, address and a free port; the step is busy while
 * those answers are pending, and the wizard is told so it does not move on with a port still being looked up.
 */
export function EntriesStep({
  servers, recent, entries, onChange, canTunnel, useDirect, onDirectChange, hostOf, newKey, randomSite, tcpTLS, onBusyChange, showErrors = false,
}: EntriesStepProps) {
  const t = useNodesT()
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState<ReadonlySet<string>>(new Set())
  const changeBusy = (value: boolean) => { setBusy(value); onBusyChange(value) }
  const touch = (key: string, field: SetupField) => setTouched(s => new Set(s).add(`${key}:${field}`))
  const selected = servers.filter(s => entries.some(e => e.line.entry_panel_id === s.id))
  const patchLine = (key: string, patch: Partial<RelayLine>) =>
    onChange(entries.map(e => e.key === key ? { ...e, line: { ...e.line, ...patch } } : e))

  // 443 is the port a REALITY or TLS-over-TCP line is best on, so it is taken when the server has nothing on it
  // (or only shared-port fronts, which can share it); any other line, and a taken 443, get the server's suggestion.
  async function fillPort(server: Server) {
    try {
      if (tcpTLS) {
        const usage = await getServerPorts(server.id)
        const on443 = usage.filter(p => p.port === 443 || (p.range_high != null && p.port <= 443 && p.range_high >= 443))
        if (on443.length === 0 || on443.every(p => p.kind === 'front')) return 443
      }
      return await suggestPort(server.id)
    } catch {
      // The entry stays editable when a suggestion is unavailable.
      return 0
    }
  }

  async function select(values: Server[]) {
    changeBusy(true)
    try {
      const picked = new Set(values.map(s => s.id))
      const kept = entries.filter(e => !e.line.entry_panel_id || picked.has(e.line.entry_panel_id))
      const added = values.filter(s => !selected.some(v => v.id === s.id))
      const additions = await Promise.all(added.map(async server => {
        const draft = newSetupRelay(newKey())
        return { ...draft, line: { ...draft.line, name: server.name, address: hostOf(server), port: await fillPort(server), entry_panel_id: server.id } }
      }))
      onChange([...kept, ...additions])
    } finally { changeBusy(false) }
  }

  async function suggest(line: RelayLine, key: string) {
    const server = servers.find(s => s.id === line.entry_panel_id)
    if (!server) return
    changeBusy(true)
    try {
      patchLine(key, { port: await suggestPort(server.id) })
    } catch {
      // The address and current port stay available for manual editing.
    } finally { changeBusy(false) }
  }

  function changeMode(entry: SetupRelay, mode: 'forward' | 'tunnel') {
    const site = randomSite()
    onChange(entries.map(e => e.key === entry.key ? {
      ...e, line: { ...e.line, mode: mode === 'forward' ? undefined : mode },
      tunnel: mode === 'tunnel' ? e.tunnel ?? { listen: '', sni: site, target: `${site}:443` } : e.tunnel,
    } : e))
  }

  const patchTunnel = (key: string, field: keyof NodeSetupEntry, value: string) =>
    onChange(entries.map(e => e.key === key && e.tunnel ? { ...e, tunnel: { ...e.tunnel, [field]: value } } : e))

  function add(patch: Partial<RelayLine>) {
    const entry = newSetupRelay(newKey())
    onChange([...entries, { ...entry, line: { ...entry.line, ...patch } }])
  }

  return (
    <div className="flex flex-col gap-4">
      <Field orientation="horizontal" label={t('nodes.setup.direct')}>
        <Switch checked={useDirect} onCheckedChange={onDirectChange} />
      </Field>
      <Field label={t('nodes.setup.entries')}>
        {p => <EntryServerPicker id={p.id} aria-describedby={p['aria-describedby']} servers={servers} recent={recent} selected={selected} disabled={busy} onChange={values => void select(values)} />}
      </Field>
      {entries.map(entry => (
        <EntryCard key={entry.key} entry={entry} canTunnel={canTunnel} busy={busy} showAll={showErrors} touched={touched} onTouch={touch}
          onPatch={patch => patchLine(entry.key, patch)} onTunnel={(field, value) => patchTunnel(entry.key, field, value)}
          onMode={mode => changeMode(entry, mode)} onRemove={() => onChange(entries.filter(e => e.key !== entry.key))}
          onSuggest={() => void suggest(entry.line, entry.key)} />
      ))}
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => add({})}>{t('nodes.relay.entry_external')}</Button>
        <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => add({ mode: 'cdn', port: CDN_DEFAULT_PORT })}>{t('nodes.relay.entry_cdn')}</Button>
      </div>
    </div>
  )
}
