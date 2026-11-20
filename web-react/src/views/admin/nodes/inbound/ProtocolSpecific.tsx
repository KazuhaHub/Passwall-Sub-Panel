import type { Cert } from '@/api/certs'
import { FormSection } from '@/components/form/FormSection'
import { IconKey } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { useNodesT } from '../shared/useNodesT'
import { CertFields } from './CertFields'
import { AreaField, FieldRow, MONO, SelectField, SwitchField, TextField, dashed, type SectionProps } from './controls'
import { FINGERPRINTS, PROTOCOL_OPTIONS, SS2022_METHODS, SUI_ONLY_PROTOCOLS, type InboundFormState, type SS2022Method } from './inboundModel'
import { Hysteria2Section } from './Hysteria2Section'

const TUIC_CONGESTION = ['cubic', 'new_reno', 'bbr']
const NAIVE_QUIC_CONGESTION = ['bbr', 'bbr_standard', 'bbr2', 'bbr2_variant', 'cubic', 'reno']

/** The sections that belong to one protocol only: Hysteria 2, the S-UI ones (AnyTLS, TUIC, Naive) and Shadowsocks 2022. */
export function ProtocolSpecific({ onGenSSPassword, certs, ...section }: SectionProps & { certs: Cert[]; onGenSSPassword: () => void }) {
  const { form } = section
  return (
    <>
      {form.protocol === 'hysteria2' && <Hysteria2Section {...section} certs={certs} />}
      {SUI_ONLY_PROTOCOLS.has(form.protocol) && <SuiSection {...section} certs={certs} />}
      {form.protocol === 'ss2022' && <Ss2022Section {...section} onGenSSPassword={onGenSSPassword} />}
    </>
  )
}

/** The protocols only S-UI (sing-box) runs: their own options first, then the TLS block they all share. */
function SuiSection({ form, setForm, update, certs }: SectionProps & { certs: Cert[] }) {
  const t = useNodesT()
  const protocolLabel = PROTOCOL_OPTIONS.find(o => o.value === form.protocol)?.label ?? form.protocol
  const number = (key: 'tuic_auth_timeout' | 'tuic_heartbeat') => (v: string) => update(key, Math.max(0, Number(v) || 0))

  return (
    <FormSection title={t('nodes.inbound_form.sui_section', { protocol: protocolLabel })}>
      {form.protocol === 'anytls' && (
        <AreaField label={t('nodes.create_dialog.anytls_padding')} hint={t('nodes.create_dialog.anytls_padding_hint')} rows={5}
          value={form.anytls_padding_scheme_text} onChange={v => update('anytls_padding_scheme_text', v)} />
      )}
      {form.protocol === 'tuic' && (
        <FieldRow className="sm:grid-cols-3">
          <SelectField label={t('nodes.create_dialog.tuic_congestion_control')} value={form.tuic_congestion_control}
            options={dashed(TUIC_CONGESTION)} onChange={v => update('tuic_congestion_control', v as InboundFormState['tuic_congestion_control'])} />
          <TextField type="number" label={t('nodes.create_dialog.tuic_auth_timeout')} value={form.tuic_auth_timeout} onChange={number('tuic_auth_timeout')} />
          <TextField type="number" label={t('nodes.create_dialog.tuic_heartbeat')} value={form.tuic_heartbeat} onChange={number('tuic_heartbeat')} />
        </FieldRow>
      )}
      {form.protocol === 'tuic' && (
        <SwitchField label={t('nodes.create_dialog.tuic_zero_rtt')} checked={form.tuic_zero_rtt_handshake} onChange={c => update('tuic_zero_rtt_handshake', c)} />
      )}
      {form.protocol === 'naive' && (
        <FieldRow>
          <SelectField label={t('nodes.create_dialog.naive_network')} value={form.naive_network}
            options={[{ value: '', label: t('nodes.create_dialog.naive_network_default') }, { value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]}
            onChange={v => update('naive_network', v as InboundFormState['naive_network'])} />
          <SelectField label={t('nodes.create_dialog.naive_quic_congestion_control')} value={form.naive_quic_congestion_control}
            options={[{ value: '', label: t('nodes.create_dialog.naive_quic_off') }, ...NAIVE_QUIC_CONGESTION.map(v => ({ value: v, label: v }))]}
            onChange={v => update('naive_quic_congestion_control', v as InboundFormState['naive_quic_congestion_control'])} />
        </FieldRow>
      )}
      <FieldRow className="sm:grid-cols-3">
        <TextField label={t('nodes.create_dialog.tls_server_name')} value={form.tls_server_name} onChange={v => update('tls_server_name', v)} />
        <TextField label="ALPN" value={form.tls_alpn_text} onChange={v => update('tls_alpn_text', v)} />
        <SelectField label={t('nodes.create_dialog.tls_fingerprint')} value={form.tls_fingerprint}
          options={[{ value: '', label: '—' }, ...FINGERPRINTS.map(f => ({ value: f, label: f }))]} onChange={v => update('tls_fingerprint', v)} />
      </FieldRow>
      <SwitchField danger label={t('nodes.create_dialog.tls_skip_cert_verify')} checked={form.tls_allow_insecure} onChange={c => update('tls_allow_insecure', c)} />
      <CertFields form={form} setForm={setForm} update={update} certs={certs} />
    </FormSection>
  )
}

/** Shadowsocks 2022: the cipher fixes the key length, so the PSK is generated for it rather than typed. */
function Ss2022Section({ form, update, panelType, onGenSSPassword }: SectionProps & { onGenSSPassword: () => void }) {
  const t = useNodesT()
  return (
    <FormSection title={t('nodes.create_dialog.section_ss2022')}>
      <FieldRow>
        <SelectField label={t('nodes.create_dialog.ss_method')} value={form.ss_method}
          options={SS2022_METHODS.map(m => ({ value: m.value, label: m.value }))} onChange={v => update('ss_method', v as SS2022Method)} />
        <TextField label={t('nodes.create_dialog.ss_network')} value={form.ss_network} onChange={v => update('ss_network', v)} />
      </FieldRow>
      <Field label={t('nodes.create_dialog.ss_password')} required>
        {({ id, labelId: _l, ...wiring }) => (
          <div className="flex gap-2">
            <Input id={id} {...wiring} className={MONO} value={form.ss_password} onChange={e => update('ss_password', e.target.value)} />
            <Button type="button" variant="outline" onClick={onGenSSPassword}><IconKey aria-hidden />{t('nodes.create_dialog.gen_ss_password')}</Button>
          </div>
        )}
      </Field>
      {panelType === '3xui' && <SwitchField label={t('nodes.create_dialog.ss_iv_check')} checked={form.ss_iv_check} onChange={c => update('ss_iv_check', c)} />}
    </FormSection>
  )
}
