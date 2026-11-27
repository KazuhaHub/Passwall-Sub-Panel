import { FormSection } from '@/components/form/FormSection'
import { useNodesT } from '../shared/useNodesT'
import { FieldRow, SelectField, SubHeading, SwitchField, TextField, dashed, type SectionProps } from './controls'
import { TPROXY_MODES, type InboundFormState } from './inboundModel'

/**
 * Socket-level tuning and sniffing, collapsed so the form stays short for the common case; the open state
 * is remembered. 3X-UI only: the other adapters take no such options.
 *
 * sockopt keeps to what a listener can use: tcpFastOpen, keep-alive, tproxy ("redirect" / "tproxy" Linux
 * modes) and the mark for transparent proxies / iptables-tagged egress. sockopt has more (dial-side
 * domainStrategy), but those do not apply to an inbound.
 */
export function AdvancedSection({ form, update, panelType }: SectionProps) {
  const t = useNodesT()
  if (panelType !== '3xui') return null
  // 0 is "not set" and shows as an empty box.
  const num = (key: 'sockopt_mark' | 'sockopt_tcp_keep_alive_interval' | 'sockopt_tcp_keep_alive_idle' | 'sockopt_tcp_user_timeout', label: string) => (
    <TextField type="number" label={label} value={form[key] || ''} onChange={v => update(key, Number(v) || 0)} />
  )

  return (
    <FormSection advanced sectionKey="node-inbound-advanced" title={t('nodes.inbound_form.section_advanced')}>
      <SubHeading>{t('nodes.create_dialog.section_sockopt')}</SubHeading>
      <SwitchField label={t('nodes.create_dialog.sockopt_enabled')} checked={form.sockopt_enabled} onChange={c => update('sockopt_enabled', c)} />
      {form.sockopt_enabled && (
        <>
          <FieldRow>
            {num('sockopt_mark', t('nodes.create_dialog.sockopt_mark'))}
            <SelectField label={t('nodes.create_dialog.sockopt_tproxy')} value={form.sockopt_tproxy} options={dashed(TPROXY_MODES)}
              onChange={v => update('sockopt_tproxy', v as InboundFormState['sockopt_tproxy'])} />
          </FieldRow>
          <FieldRow className="sm:grid-cols-3">
            {num('sockopt_tcp_keep_alive_interval', t('nodes.create_dialog.sockopt_tcp_keep_alive_interval'))}
            {num('sockopt_tcp_keep_alive_idle', t('nodes.create_dialog.sockopt_tcp_keep_alive_idle'))}
            {num('sockopt_tcp_user_timeout', t('nodes.create_dialog.sockopt_tcp_user_timeout'))}
          </FieldRow>
          <FieldRow>
            <SwitchField label={t('nodes.create_dialog.sockopt_accept_proxy_protocol')} checked={form.sockopt_accept_proxy_protocol} onChange={c => update('sockopt_accept_proxy_protocol', c)} />
            <SwitchField label={t('nodes.create_dialog.sockopt_tcp_fast_open')} checked={form.sockopt_tcp_fast_open} onChange={c => update('sockopt_tcp_fast_open', c)} />
          </FieldRow>
        </>
      )}

      <SubHeading>{t('nodes.create_dialog.section_sniffing')}</SubHeading>
      <SwitchField label={t('nodes.create_dialog.sniffing_enabled')} checked={form.sniffing_enabled} onChange={c => update('sniffing_enabled', c)} />
      <TextField label={t('nodes.create_dialog.sniffing_dest_override')} value={form.sniffing_dest_override_text} onChange={v => update('sniffing_dest_override_text', v)} />
      <FieldRow>
        <SwitchField label={t('nodes.create_dialog.sniffing_metadata_only')} checked={form.sniffing_metadata_only} onChange={c => update('sniffing_metadata_only', c)} />
        <SwitchField label={t('nodes.create_dialog.sniffing_route_only')} checked={form.sniffing_route_only} onChange={c => update('sniffing_route_only', c)} />
      </FieldRow>
    </FormSection>
  )
}
