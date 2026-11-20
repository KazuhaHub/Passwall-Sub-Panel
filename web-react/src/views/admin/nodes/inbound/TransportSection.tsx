import { FormSection } from '@/components/form/FormSection'
import { useNodesT } from '../shared/useNodesT'
import { FieldRow, SelectField, SubHeading, SwitchField, TextField, dashed, type SectionProps } from './controls'
import { applySecurityChange } from './formRules'
import { PROTOCOL_OPTIONS, TCP_HEADER_TYPES, VLESS_FLOWS, VLESS_NETWORKS, VLESS_SECURITIES, XHTTP_MODES, type VlessNetwork, type VlessSecurity } from './inboundModel'

/**
 * Network, security and flow of VLESS, VMess and Trojan (they ride on the same Xray transport layer; only
 * the inner protocol settings differ), then the fields of the chosen network. The options the panel or the
 * protocol cannot take are not offered rather than offered and refused later.
 */
export function TransportSection({ form, setForm, update, panelType }: SectionProps) {
  const t = useNodesT()
  const native = panelType === '3xui'
  const trojan = form.protocol === 'trojan'
  const network = form.vless_network
  const proxyProtocol = t('nodes.create_dialog.accept_proxy_protocol')
  const protocolLabel = PROTOCOL_OPTIONS.find(o => o.value === form.protocol)?.label ?? form.protocol

  return (
    <FormSection title={t('nodes.inbound_form.section_transport')} description={protocolLabel}>
      <FieldRow className="sm:grid-cols-3">
        {/* xhttp is Xray's; S-UI's sing-box has plain http instead. */}
        <SelectField label={t('nodes.create_dialog.vless_network')} value={network}
          options={VLESS_NETWORKS.filter(o => native ? o.value !== 'http' : o.value !== 'xhttp')}
          onChange={v => update('vless_network', v as VlessNetwork)} />
        {/* Trojan must run over TLS, so its selector is locked: a config that would not start cannot be picked. REALITY is VLESS-only in
            practice (VMess clients never shipped it), so only VLESS is offered it. */}
        <SelectField label={t('nodes.create_dialog.vless_security')} value={trojan ? 'tls' : form.vless_security} disabled={trojan}
          options={VLESS_SECURITIES.filter(o => !(o.value === 'reality' && form.protocol !== 'vless'))}
          onChange={v => setForm(prev => applySecurityChange(prev, v as VlessSecurity))} />
        {form.protocol === 'vless' && (
          <SelectField label={t('nodes.create_dialog.vless_flow')} value={form.vless_flow} options={dashed(VLESS_FLOWS)} onChange={v => update('vless_flow', v)} />
        )}
      </FieldRow>

      {network === 'tcp' && native && (
        <>
          <SubHeading>{t('nodes.create_dialog.section_tcp')}</SubHeading>
          <FieldRow>
            <SelectField label={t('nodes.create_dialog.tcp_header_type')} value={form.tcp_header_type} options={dashed(TCP_HEADER_TYPES)} onChange={v => update('tcp_header_type', v)} />
            <SwitchField label={proxyProtocol} checked={form.tcp_accept_proxy_protocol} onChange={c => update('tcp_accept_proxy_protocol', c)} />
          </FieldRow>
        </>
      )}

      {network === 'ws' && (
        <>
          <SubHeading>{t('nodes.create_dialog.section_ws')}</SubHeading>
          <FieldRow>
            <TextField label={t('nodes.create_dialog.ws_path')} value={form.ws_path} onChange={v => update('ws_path', v)} />
            <TextField label={t('nodes.create_dialog.ws_host')} value={form.ws_host} onChange={v => update('ws_host', v)} />
          </FieldRow>
          {native && <SwitchField label={proxyProtocol} checked={form.ws_accept_proxy_protocol} onChange={c => update('ws_accept_proxy_protocol', c)} />}
        </>
      )}

      {network === 'grpc' && (
        <>
          <SubHeading>{t('nodes.create_dialog.section_grpc')}</SubHeading>
          <FieldRow>
            <TextField label={t('nodes.create_dialog.grpc_service_name')} value={form.grpc_service_name} onChange={v => update('grpc_service_name', v)} />
            {native && <TextField label={t('nodes.create_dialog.grpc_authority')} value={form.grpc_authority} onChange={v => update('grpc_authority', v)} />}
          </FieldRow>
          {native && <SwitchField label={t('nodes.create_dialog.grpc_multi_mode')} checked={form.grpc_multi_mode} onChange={c => update('grpc_multi_mode', c)} />}
        </>
      )}

      {(network === 'httpupgrade' || network === 'http') && (
        <>
          <SubHeading>{network === 'http' ? 'HTTP' : t('nodes.create_dialog.section_httpupgrade')}</SubHeading>
          <FieldRow>
            <TextField label={t('nodes.create_dialog.ws_path')} value={form.httpupgrade_path} onChange={v => update('httpupgrade_path', v)} />
            <TextField label={t('nodes.create_dialog.ws_host')} value={form.httpupgrade_host} onChange={v => update('httpupgrade_host', v)} />
          </FieldRow>
          {network === 'httpupgrade' && native && (
            <SwitchField label={proxyProtocol} checked={form.httpupgrade_accept_proxy_protocol} onChange={c => update('httpupgrade_accept_proxy_protocol', c)} />
          )}
        </>
      )}

      {network === 'xhttp' && (
        <>
          <SubHeading>{t('nodes.create_dialog.section_xhttp')}</SubHeading>
          <FieldRow className="sm:grid-cols-3">
            <TextField label={t('nodes.create_dialog.ws_path')} value={form.xhttp_path} onChange={v => update('xhttp_path', v)} />
            <TextField label={t('nodes.create_dialog.ws_host')} value={form.xhttp_host} onChange={v => update('xhttp_host', v)} />
            <SelectField label={t('nodes.create_dialog.xhttp_mode')} value={form.xhttp_mode} options={dashed(XHTTP_MODES)} onChange={v => update('xhttp_mode', v)} />
          </FieldRow>
        </>
      )}
    </FormSection>
  )
}
