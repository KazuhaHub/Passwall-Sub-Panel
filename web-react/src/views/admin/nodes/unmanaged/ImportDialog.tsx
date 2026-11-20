import { useMemo, useState } from 'react'
import { importNode } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { UnmanagedInbound } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { TagsField } from '@/components/form/TagsField'
import { pushToast } from '@/components/feedback/toasts'
import { validateHost, validateName } from '@/utils/validators'
import { serverHost } from '../legacy/inboundModel'
import { useNodesT } from '../shared/useNodesT'

const VLESS_FLOWS = ['', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443']

/**
 * Takes an unmanaged inbound under PSP. The old "sort order" field is gone: a number on a hidden ten-step scale
 * is the opposite of "drag to any position", so the node is appended (0, the field's own default) and then placed
 * with Move to… (design U7). The caller highlights what the import added.
 */
export function ImportDialog({ inbound, server, tagOptions, onClose, onImported }: {
  inbound: UnmanagedInbound
  server?: Server
  tagOptions: readonly string[]
  onClose: () => void
  onImported: () => void
}) {
  const t = useNodesT()
  const protocol = (inbound.Protocol || '').toLowerCase()
  const [name, setName] = useState(inbound.Remark || `${inbound.Protocol}:${inbound.Port}`)
  // The proxy host usually equals the panel's host; it is only a default, the admin may override it.
  const [address, setAddress] = useState(serverHost(server))
  const [flow, setFlow] = useState('')
  const [region, setRegion] = useState('')
  const [tags, setTags] = useState<string[]>([])
  const [touched, setTouched] = useState(false)
  const errors = useMemo(() => ({
    name: validateName(name, { required: true, max: 64 }),
    address: validateHost(address, { required: true }),
    region: validateName(region, { required: true, max: 32 }),
  }), [name, address, region])
  const show = (k: keyof typeof errors) => (touched && errors[k] ? t(errors[k]) : undefined)

  const submit = async () => {
    setTouched(true)
    if (errors.name || errors.address || errors.region) return
    try {
      await importNode({
        panel_id: inbound.PanelID, inbound_id: inbound.InboundID, display_name: name, server_address: address,
        protocol: protocol || undefined,
        // Flow only applies to VLESS; never persisted for other protocols.
        flow: protocol === 'vless' ? (flow || undefined) : undefined,
        region, tags, sort_order: 0,
      })
      pushToast({ message: t('nodes.imported'), severity: 'success' })
      onImported()
      onClose()
    } catch { /* the request layer already surfaced it */ }
  }

  return (
    <Dialog open onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('nodes.import_dialog.title')}</DialogTitle>
          <DialogDescription>{inbound.PanelName} · inbound #{inbound.InboundID}</DialogDescription>
        </DialogHeader>
        <Field label={t('nodes.import_dialog.display_name')} required error={show('name')}><Input value={name} onChange={e => setName(e.target.value)} onBlur={() => setTouched(true)} /></Field>
        <Field label={t('nodes.import_dialog.server_address')} required error={show('address')}><Input value={address} onChange={e => setAddress(e.target.value)} onBlur={() => setTouched(true)} /></Field>
        {protocol === 'vless' && (
          <Field label={t('nodes.import_dialog.flow')}>
            <NativeSelect value={flow} onChange={e => setFlow(e.target.value)}>{VLESS_FLOWS.map(f => <option key={f} value={f}>{f || '—'}</option>)}</NativeSelect>
          </Field>
        )}
        <Field label={t('nodes.import_dialog.region')} required error={show('region')}><Input value={region} onChange={e => setRegion(e.target.value)} onBlur={() => setTouched(true)} /></Field>
        <Field label={t('nodes.import_dialog.tags')}>{p => <TagsField id={p.id} aria-labelledby={p.labelId} value={tags} onChange={setTags} suggestions={tagOptions} />}</Field>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton onClick={submit}>{t('nodes.import')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
