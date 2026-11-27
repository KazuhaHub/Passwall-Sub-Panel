import { useState } from 'react'
import { type Cert, fetchPanelWebCert } from '@/api/certs'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconSpinner } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useNodesT } from '../shared/useNodesT'
import { AreaField, FieldRow, MONO, SelectField, type SectionProps } from './controls'
import type { InboundFormState } from './inboundModel'

/**
 * The certificate source picker, shared by the VLESS-stream TLS section, Hysteria 2 and the S-UI protocols
 * (all of them emit tlsSettings.certificates). ONE selector with four sources that exclude each other, so
 * a manual certificate and a managed one can never be chosen at once:
 *  - none: no certificates (REALITY, or a root the client already trusts)
 *  - file: paths on the server's host (acme/certbot in production); "fetch from panel" fills them from
 *    the panel's own web certificate
 *  - inline: PEM pasted into the config (one-off, no ACME on the host)
 *  - psp_managed: a PSP-issued certificate; the backend deploys and renews it
 * The bind of a managed certificate is applied on save, on create and on edit alike.
 */
export function CertFields({ form, setForm, update, certs }: Pick<SectionProps, 'form' | 'setForm' | 'update'> & { certs: Cert[] }) {
  const t = useNodesT()
  const [fetching, setFetching] = useState(false)
  const active = certs.filter(c => c.status === 'active')

  async function fromPanel() {
    if (!form.panel_id) {
      pushToast({ message: t('nodes.cert_source.no_panel'), severity: 'warning' })
      return
    }
    setFetching(true)
    try {
      const r = await fetchPanelWebCert(form.panel_id)
      if (!r.supported) {
        pushToast({ message: t('nodes.cert_source.from_panel_unsupported'), severity: 'warning' })
        return
      }
      setForm(prev => ({ ...prev, tls_cert_mode: 'file', tls_cert_file: r.cert_file ?? '', tls_key_file: r.key_file ?? '' }))
      pushToast({ message: t('nodes.cert_source.from_panel_filled'), severity: 'success' })
    } catch {
      // The request layer already showed the error.
    } finally {
      setFetching(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <SelectField label={t('nodes.create_dialog.tls_cert_mode')} value={form.tls_cert_mode} className="sm:max-w-xs"
        options={[
          { value: '', label: t('nodes.create_dialog.tls_cert_none') },
          { value: 'file', label: t('nodes.create_dialog.tls_cert_file') },
          { value: 'inline', label: t('nodes.create_dialog.tls_cert_inline') },
          { value: 'psp_managed', label: t('nodes.create_dialog.tls_cert_managed') },
        ]}
        onChange={v => update('tls_cert_mode', v as InboundFormState['tls_cert_mode'])} />
      {form.tls_cert_mode === 'file' && (
        <>
          <div>
            <Button type="button" size="sm" variant="outline" disabled={!form.panel_id || fetching} onClick={() => void fromPanel()}>
              {fetching && <IconSpinner aria-hidden className="animate-spin" />}
              {t('nodes.cert_source.from_panel')}
            </Button>
          </div>
          <FieldRow>
            <Field label={t('nodes.create_dialog.tls_cert_file_path')}>
              <Input className={MONO} placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" value={form.tls_cert_file} onChange={e => update('tls_cert_file', e.target.value)} />
            </Field>
            <Field label={t('nodes.create_dialog.tls_key_file_path')}>
              <Input className={MONO} placeholder="/etc/letsencrypt/live/example.com/privkey.pem" value={form.tls_key_file} onChange={e => update('tls_key_file', e.target.value)} />
            </Field>
          </FieldRow>
        </>
      )}
      {form.tls_cert_mode === 'inline' && (
        <FieldRow>
          <AreaField label={t('nodes.create_dialog.tls_cert_pem')} value={form.tls_cert_pem} rows={4}
            placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'} onChange={v => update('tls_cert_pem', v)} />
          <AreaField label={t('nodes.create_dialog.tls_key_pem')} value={form.tls_key_pem} rows={4}
            placeholder={'-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'} onChange={v => update('tls_key_pem', v)} />
        </FieldRow>
      )}
      {form.tls_cert_mode === 'psp_managed' && (
        <div className="flex flex-col gap-1.5 sm:max-w-xs">
          <Field label={t('nodes.create_dialog.tls_cert_managed_pick')}>
            <NativeSelect value={form.cert_id} onChange={e => update('cert_id', Number(e.target.value))}>
              <option value={0}>{t('nodes.cert_source.pick_managed')}</option>
              {active.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
            </NativeSelect>
          </Field>
          <p data-testid="managed-cert-help" className={cn('text-xs', active.length === 0 ? 'text-destructive' : 'text-muted-foreground')}>
            {active.length === 0 ? t('nodes.cert_source.no_active_certs') : t('nodes.cert_source.managed_help')}
          </p>
        </div>
      )}
    </div>
  )
}
