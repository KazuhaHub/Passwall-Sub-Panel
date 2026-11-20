import type { Cert } from '@/api/certs'
import { FormSection } from '@/components/form/FormSection'
import { useNodesT } from '../shared/useNodesT'
import { CertFields } from './CertFields'
import { CipherSuitesField } from './CipherSuitesField'
import { FieldRow, SelectField, TextField, type SectionProps } from './controls'
import type { InboundFormState } from './inboundModel'

/** Hysteria 2 is a QUIC transport of Xray: SNI and ALPN reuse the TLS fields, obfuscation and masquerade are its own. */
export function Hysteria2Section({ form, setForm, update, panelType, certs }: SectionProps & { certs: Cert[] }) {
  const t = useNodesT()
  const type = form.hy2_masquerade_type
  // What the masquerade content means depends on its type.
  const contentLabel = type === 'proxy' ? t('nodes.create_dialog.hy2_masquerade_content_proxy')
    : type === 'file' ? t('nodes.create_dialog.hy2_masquerade_content_file')
      : type === 'string' ? t('nodes.create_dialog.hy2_masquerade_content_string')
        : t('nodes.create_dialog.hy2_masquerade_content')

  return (
    <FormSection title={t('nodes.create_dialog.section_hysteria2')}>
      <FieldRow className="sm:grid-cols-3">
        <TextField label={t('nodes.create_dialog.tls_server_name')} value={form.tls_server_name} onChange={v => update('tls_server_name', v)} />
        <TextField label={t('nodes.create_dialog.tls_alpn')} placeholder="h3" value={form.tls_alpn_text} onChange={v => update('tls_alpn_text', v)} />
        {/* An emptied box means the default, not zero: a zero idle timeout would drop every UDP session at once. */}
        <TextField label={t('nodes.create_dialog.hy2_udp_idle_timeout')} type="number" value={form.hy2_udp_idle_timeout}
          onChange={v => update('hy2_udp_idle_timeout', Number(v) || 60)} />
      </FieldRow>
      {panelType === '3xui' && <CipherSuitesField label={t('nodes.create_dialog.tls_cipher_suites')} hint={t('nodes.create_dialog.tls_cipher_suites_hint')}
        value={form.tls_cipher_suites} onChange={v => update('tls_cipher_suites', v)} />}
      <TextField label={t('nodes.create_dialog.hy2_obfs_password')} hint={t('nodes.create_dialog.hy2_obfs_hint')}
        value={form.hy2_obfs_password} onChange={v => update('hy2_obfs_password', v)} />
      <FieldRow>
        <SelectField label={t('nodes.create_dialog.hy2_masquerade_type')} value={type}
          options={[
            { value: '', label: t('nodes.create_dialog.hy2_masquerade_none') },
            { value: 'proxy', label: t('nodes.create_dialog.hy2_masquerade_proxy') },
            { value: 'file', label: t('nodes.create_dialog.hy2_masquerade_file') },
            { value: 'string', label: t('nodes.create_dialog.hy2_masquerade_string') },
          ]}
          onChange={v => update('hy2_masquerade_type', v as InboundFormState['hy2_masquerade_type'])} />
        <TextField label={contentLabel} disabled={!type} value={form.hy2_masquerade_content} onChange={v => update('hy2_masquerade_content', v)} />
      </FieldRow>
      {/* Hysteria 2 emits tlsSettings.certificates the way the VLESS stream does (see buildStreamSettings), so the shared picker applies. */}
      <CertFields form={form} setForm={setForm} update={update} certs={certs} />
    </FormSection>
  )
}
