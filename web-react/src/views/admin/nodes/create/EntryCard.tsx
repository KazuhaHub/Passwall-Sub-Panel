import { InfoTip } from '@/components/common/HelpTip'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconRandom } from '@/lib/icons'
import type { NodeSetupEntry } from '@/api/nodes'
import type { RelayLine } from '@/api/types'
import { CdnFields } from '../lines/CdnFields'
import { PortInput } from '../lines/fieldKit'
import { kindOf } from '../lines/lineModel'
import { useNodesT } from '../shared/useNodesT'
import { setupProblems, type SetupField, type SetupRelay } from './setupModel'

export interface EntryCardProps {
  entry: SetupRelay
  canTunnel: boolean
  busy: boolean
  /** Show every problem now (a step that was tried), not only those of fields already left. */
  showAll: boolean
  touched: ReadonlySet<string>
  onTouch: (key: string, field: SetupField) => void
  onPatch: (patch: Partial<RelayLine>) => void
  onTunnel: (field: keyof NodeSetupEntry, value: string) => void
  onMode: (mode: 'forward' | 'tunnel') => void
  onRemove: () => void
  onSuggest: () => void
}

/** One entry of the wizard: its name, how it forwards, and the fields of its kind. */
export function EntryCard({ entry, canTunnel, busy, showAll, touched, onTouch, onPatch, onTunnel, onMode, onRemove, onSuggest }: EntryCardProps) {
  const t = useNodesT()
  const { key, line, tunnel } = entry
  const problems = setupProblems(entry)
  const managed = !!line.entry_panel_id
  const kind = kindOf(line)
  // The key, not the words: the CDN fields (shared with the line editor) translate the key they are given.
  const shown = (field: SetupField) => (showAll || touched.has(`${key}:${field}`) ? problems[field] : undefined)
  const message = (field: SetupField) => { const key = shown(field); return key ? t(`nodes.${key}`) : undefined }
  const touch = (field: SetupField) => () => onTouch(key, field)

  return (
    <div data-testid="setup-entry" className="flex flex-col gap-3 rounded-lg border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium">{t(`nodes.setup.kind.${kind}`)}</span>
        <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onRemove}>{t('nodes.relay.remove')}</Button>
      </div>
      <Field label={t('nodes.relay.name')}>
        <Input value={line.name} disabled={busy} onChange={e => onPatch({ name: e.target.value })} />
      </Field>
      {managed && (
        <div className="flex items-start gap-1">
          <Field className="flex-1" label={t('nodes.setup.mode')}>
            <NativeSelect value={line.mode ?? 'forward'} disabled={busy} onChange={e => onMode(e.target.value as 'forward' | 'tunnel')}>
              <option value="forward">{t('nodes.tunnel.mode_forward')}</option>
              <option value="tunnel" disabled={!canTunnel}>{t('nodes.tunnel.mode_tunnel')}</option>
            </NativeSelect>
          </Field>
          <InfoTip className="mt-6" text={t('nodes.setup.mode_help')} />
        </div>
      )}
      {line.mode === 'cdn' ? (
        <CdnFields draft={line} patch={onPatch} idPrefix={`${key}-`} issues={line.cdn_issues ?? []}
          error={f => (f === 'domain' || f === 'port') ? shown(f) : undefined} blur={f => { if (f === 'domain' || f === 'port') onTouch(key, f) }} />
      ) : (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,16rem)]">
            <Field label={t('nodes.relay.address')} required error={message('address')}>
              <Input required value={line.address} disabled={busy} onChange={e => onPatch({ address: e.target.value })} onBlur={touch('address')} />
            </Field>
            <Field label={t('nodes.relay.port')} required={managed} error={message('port')}>
              {p => (
                <div className="flex items-center gap-2">
                  <PortInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} required={managed} disabled={busy}
                    value={line.port} onChange={port => onPatch({ port })} onBlur={touch('port')} />
                  {managed && (
                    <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onSuggest}><IconRandom />{t('nodes.setup.random_port')}</Button>
                  )}
                </div>
              )}
            </Field>
          </div>
          {line.mode === 'tunnel' && tunnel && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              {(['listen', 'sni', 'target'] as const).map(field => (
                <Field key={field} label={t(`nodes.setup.${field}`)} required={field !== 'listen'} error={field === 'listen' ? undefined : message(field)}>
                  <Input required={field !== 'listen'} value={tunnel[field]} disabled={busy}
                    onChange={e => onTunnel(field, e.target.value)} onBlur={field === 'listen' ? undefined : touch(field)} />
                </Field>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}
