import type { Cert } from '@/api/certs'
import { FormSection } from '@/components/form/FormSection'
import { useNodesT } from '../shared/useNodesT'
import { CertFields } from './CertFields'
import { CipherSuitesField } from './CipherSuitesField'
import { FieldRow, SelectField, SubHeading, SwitchField, TextField, dashed, type SectionProps } from './controls'
import { FINGERPRINTS, TLS_VERSIONS } from './inboundModel'
import { RealityFields } from './RealityFields'

/** TLS or REALITY for the VLESS-shaped protocols; nothing is rendered for "none". */
export function SecuritySection({ certs, fingerprintLocked, canScan, scanSourceName, onGenKeys, onRandomRealitySite, genKeysBusy, ...section }: SectionProps & {
  certs: Cert[]
  fingerprintLocked: boolean
  canScan: boolean
  scanSourceName?: string
  onGenKeys: () => void | Promise<void>
  onRandomRealitySite?: () => void
  genKeysBusy: boolean
}) {
  const t = useNodesT()
  const { form, update, panelType } = section
  const native = panelType === '3xui'
  const tls = form.vless_security === 'tls'
  const reality = form.protocol === 'vless' && form.vless_security === 'reality'
  if (!tls && !reality) return null

  return (
    <FormSection title={t('nodes.inbound_form.section_security')}>
      {tls && (
        <>
          <SubHeading>{t('nodes.create_dialog.section_tls')}</SubHeading>
          <FieldRow>
            <TextField label={t('nodes.create_dialog.tls_server_name')} value={form.tls_server_name} onChange={v => update('tls_server_name', v)} />
            <TextField label={t('nodes.create_dialog.tls_alpn')} value={form.tls_alpn_text} onChange={v => update('tls_alpn_text', v)} />
          </FieldRow>
          <FieldRow className="sm:grid-cols-3">
            <SelectField label={t('nodes.create_dialog.tls_fingerprint')} value={form.tls_fingerprint}
              options={[{ value: '', label: '—' }, ...FINGERPRINTS.map(f => ({ value: f, label: f }))]} onChange={v => update('tls_fingerprint', v)} />
            <SelectField label={t('nodes.create_dialog.tls_min_version')} value={form.tls_min_version} options={dashed(TLS_VERSIONS)} onChange={v => update('tls_min_version', v)} />
            <SelectField label={t('nodes.create_dialog.tls_max_version')} value={form.tls_max_version} options={dashed(TLS_VERSIONS)} onChange={v => update('tls_max_version', v)} />
          </FieldRow>
          {native && <CipherSuitesField label={t('nodes.create_dialog.tls_cipher_suites')} hint={t('nodes.create_dialog.tls_cipher_suites_hint')}
            value={form.tls_cipher_suites} onChange={v => update('tls_cipher_suites', v)} />}
          <FieldRow>
            <SwitchField danger label={t('nodes.create_dialog.tls_allow_insecure')} checked={form.tls_allow_insecure} onChange={c => update('tls_allow_insecure', c)} />
            {native && <SwitchField label={t('nodes.create_dialog.tls_reject_unknown_sni')} checked={form.tls_reject_unknown_sni} onChange={c => update('tls_reject_unknown_sni', c)} />}
          </FieldRow>
          <CertFields form={form} setForm={section.setForm} update={update} certs={certs} />
        </>
      )}
      {reality && (
        <RealityFields {...section} fingerprintLocked={fingerprintLocked} canScan={canScan} scanSourceName={scanSourceName}
          onGenKeys={onGenKeys} onRandomRealitySite={onRandomRealitySite} genKeysBusy={genKeysBusy} />
      )}
    </FormSection>
  )
}
