import type { Server } from '@/api/servers'
import { FormSection } from '@/components/form/FormSection'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import { useNodesT } from '../shared/useNodesT'
import { FieldRow, SelectField, SwitchField, TextField, type SectionProps } from './controls'
import { applyProtocolChange, applyServerChange, leavesJson } from './formRules'
import { type CreateProtocol, PROTOCOL_OPTIONS, SUI_ONLY_PROTOCOLS } from './inboundModel'

/** Which server, which protocol, where it listens: the part of the form every protocol has. */
export function ProtocolSection({ form, setForm, update, panelType, servers, protocolReadonly, json, onLeaveJson }: SectionProps & {
  /** Create only: the writable servers to pick from. */
  servers?: Server[]
  protocolReadonly?: boolean
  json: boolean
  onLeaveJson?: () => void
}) {
  const t = useNodesT()
  // A protocol only S-UI has is not offered for a server that cannot run it.
  const options = panelType === 'sui' ? PROTOCOL_OPTIONS : PROTOCOL_OPTIONS.filter(o => !SUI_ONLY_PROTOCOLS.has(o.value))

  return (
    <FormSection title={t('nodes.inbound_form.section_protocol')}>
      <FieldRow>
        {servers && (
          <Field label={t('nodes.create_dialog.panel')}>
            <NativeSelect value={servers.find(s => s.id === form.panel_id)?.id ?? servers[0]?.id ?? ''}
              onChange={e => {
                const next = servers.find(s => s.id === Number(e.target.value))
                if (!next) return
                // JSON mode is a 3X-UI escape hatch: a server of another kind cannot take it.
                if (leavesJson(next)) onLeaveJson?.()
                setForm(prev => applyServerChange(prev, servers, next))
              }}>
              {servers.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </NativeSelect>
          </Field>
        )}
        <SelectField label={t('nodes.create_dialog.protocol')} value={form.protocol} disabled={protocolReadonly || json}
          options={options} onChange={v => setForm(prev => applyProtocolChange(prev, v as CreateProtocol))} />
      </FieldRow>
      <FieldRow>
        <TextField label={t('nodes.create_dialog.listen')} value={form.listen} onChange={v => update('listen', v)} />
        <TextField label={t('nodes.create_dialog.port')} type="number" value={form.port} onChange={v => update('port', Number(v))} />
      </FieldRow>
      {panelType === '3xui' && <SwitchField label={t('nodes.create_dialog.enable')} checked={form.enable} onChange={c => update('enable', c)} />}
    </FormSection>
  )
}
