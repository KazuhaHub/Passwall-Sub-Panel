import { useState } from 'react'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconKey, IconSpinner } from '@/lib/icons'
import { useNodesT } from '../shared/useNodesT'
import { FieldRow, SelectField, SubHeading, TextField, type SectionProps } from './controls'
import { FINGERPRINTS } from './inboundModel'
import { RealityScanner } from './RealityScanner'

/** The REALITY half of the security section: the decoy it borrows, its keypair, and who may connect. */
export function RealityFields({ form, update, panelType, fingerprintLocked, canScan, scanSourceName, onGenKeys, onRandomRealitySite, genKeysBusy, setForm }: SectionProps & {
  /** Xray 26.9.8+ requires X25519MLKEM768 first, which only Chrome's fingerprint offers: the fingerprint is fixed. */
  fingerprintLocked: boolean
  /** The server has the `reality.scan` capability. */
  canScan: boolean
  scanSourceName?: string
  onGenKeys: () => void | Promise<void>
  onRandomRealitySite?: () => void
  genKeysBusy: boolean
}) {
  const t = useNodesT()
  const [scanning, setScanning] = useState(false)
  const native = panelType === '3xui'

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SubHeading>{t('nodes.create_dialog.section_reality')}</SubHeading>
        <Button type="button" size="sm" variant="outline" disabled={genKeysBusy} onClick={() => void onGenKeys()}>
          {genKeysBusy ? <IconSpinner aria-hidden className="animate-spin" /> : <IconKey aria-hidden />}
          {t('nodes.create_dialog.gen_keys')}
        </Button>
      </div>
      <FieldRow>
        <Field label={t('nodes.create_dialog.reality_dest')} required>
          {({ id, labelId: _l, ...wiring }) => (
          <div className="flex gap-2">
            <Input id={id} {...wiring} value={form.reality_dest} onChange={e => update('reality_dest', e.target.value)} />
            {onRandomRealitySite && <Button type="button" variant="outline" onClick={onRandomRealitySite}>{t('nodes.egress.random_site')}</Button>}
            <Button type="button" variant="outline" disabled={!form.panel_id || !canScan} onClick={() => setScanning(true)}>
              {t('nodes.create_dialog.reality_scan_open')}
            </Button>
          </div>
          )}
        </Field>
        <TextField label={t('nodes.create_dialog.reality_server_names')} required value={form.reality_server_names_text} onChange={v => update('reality_server_names_text', v)} />
      </FieldRow>
      <FieldRow>
        <SelectField label={t('nodes.create_dialog.reality_fingerprint')} value={form.reality_fingerprint} disabled={fingerprintLocked}
          hint={fingerprintLocked ? t('nodes.create_dialog.reality_fingerprint_mlkem_locked') : undefined}
          options={FINGERPRINTS.map(f => ({ value: f, label: f }))} onChange={v => update('reality_fingerprint', v)} />
        {native && <TextField label={t('nodes.create_dialog.reality_spider_x')} value={form.reality_spider_x} onChange={v => update('reality_spider_x', v)} />}
      </FieldRow>
      <TextField mono required label={t('nodes.create_dialog.private_key')} value={form.private_key} onChange={v => update('private_key', v)} />
      <TextField mono required label={t('nodes.create_dialog.public_key')} value={form.public_key} onChange={v => update('public_key', v)} />
      <TextField mono required label={t('nodes.create_dialog.short_ids')} value={form.short_ids_text} onChange={v => update('short_ids_text', v)} />
      {/* These gate the version the client reports. Xray 26.7.11–26.7.28 defaults an empty minClientVer to 26.3.27, and
          26.9.8+ leaves the floor off when it is empty; an explicit 1.0.0 avoids the older default floor, while REALITY's
          ML-KEM requirements apply independently. */}
      {native && (
        <FieldRow>
          <TextField label={t('nodes.create_dialog.reality_min_client')} placeholder="1.0.0" hint={t('nodes.create_dialog.reality_min_client_hint')}
            value={form.reality_min_client} onChange={v => update('reality_min_client', v)} />
          <TextField label={t('nodes.create_dialog.reality_max_client')} value={form.reality_max_client} onChange={v => update('reality_max_client', v)} />
        </FieldRow>
      )}
      <RealityScanner open={scanning} panelId={form.panel_id} sourceName={scanSourceName} onClose={() => setScanning(false)}
        onUse={result => setForm(prev => ({
          ...prev,
          reality_dest: result.target,
          // A result with no names leaves what is there.
          reality_server_names_text: result.serverNames.length > 0 ? result.serverNames.join(', ') : prev.reality_server_names_text,
        }))} />
    </>
  )
}
