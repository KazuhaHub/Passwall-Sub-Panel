// AUTOIMPORT-BEGIN
import SourceRulesSection from '../../accessControl/SourceRulesSection'
import { type SourceDraftHandle } from '../../accessControl/SourceRulesSection'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { EMPTY_INBOUND, InboundFormState, SS2022_METHODS, parseJSONSafe, randomBase64 } from '../inbound/inboundModel'
import { InboundDetail, buildSniffing, buildStreamSettings, canonicalProtocolName, parseInboundForEdit, settingsBuilderFor, validateInboundForm } from '../inbound/inboundSettings'
import { setNodeCertSource } from '@/api/certs'
import { generateRealityKeypair, getNode, updateInboundConfig, updateNodeMetadata } from '@/api/nodes'
import { PanelCapability, Server } from '@/api/servers'
import { Node } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useCan } from '@/utils/permissions'
import { Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, Typography, useTheme } from '@mui/material'
import { type FormEvent, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { InboundFormFields } from './InboundFormFields'
// AUTOIMPORT-END
// The legacy inbound editor (protocol, port, transport, TLS / REALITY, source-country rules), moved out of the
// old NodesView.tsx with its behaviour unchanged. It stays on MUI until N3 replaces it with the Inbound tab.
// The host (LegacyEditors) mounts it for one node and unmounts it on close; it loads the inbound itself.
// AUTOIMPORT-BEGIN
// AUTOIMPORT-END

export interface InboundEditProps {
  node: Node
  servers: Server[]
  onClose: () => void
  onSaved: () => void
}

export default function InboundEditDialog({ node, servers, onClose, onSaved }: InboundEditProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canAccess = useCan('access.view')
  // Registered by the source-rules section: whether its draft holds unsaved changes.
  const sourceDirtyRef = useRef<SourceDraftHandle | null>(null)
  const panelSupports = (panelID: number, capability: PanelCapability) =>
    !!servers.find(s => s.id === panelID)?.capabilities?.includes(capability)
  const [editInboundBusy, setEditInboundBusy] = useState(false)
  const [editInboundLoading, setEditInboundLoading] = useState(false)
  const [editInboundForm, setEditInboundForm] = useState<InboundFormState>(EMPTY_INBOUND)
  const [editInboundUnsupported, setEditInboundUnsupported] = useState<'' | 'protocol' | 'ss_method'>('')
  const [editInboundLoadError, setEditInboundLoadError] = useState('')
  const [editInboundReady, setEditInboundReady] = useState(false)
  const [editInboundSyncNotice, setEditInboundSyncNotice] = useState(false)
  const editInboundIntent = useRef(0)
  const [editInboundGenBusy, setEditInboundGenBusy] = useState(false)
  const [editingInboundNode, setEditingInboundNode] = useState<Node | null>(null)
  useEffect(() => () => { editInboundIntent.current += 1 }, [])
  const [editAdvanced, setEditAdvanced] = useState(false)
  async function openEditInbound(n: Node) {
    const intent = ++editInboundIntent.current
    setEditingInboundNode(n)
    setEditInboundUnsupported('')
    setEditInboundLoadError('')
    setEditInboundReady(false)
    setEditInboundSyncNotice(false)
    setEditInboundLoading(true)
    setEditInboundGenBusy(false)
    setEditAdvanced(false)
    // Clear the previous form, but do not render editable defaults while the
    // requested configuration is missing. A metadata row is not an inbound.
    setEditInboundForm(EMPTY_INBOUND)
    try {
      const detail = await getNode(n.id)
      if (editInboundIntent.current !== intent) return
      if (detail.node?.id !== n.id) throw new Error(t('admin:nodes.edit_inbound_dialog.invalid_configuration'))
      setEditingInboundNode(detail.node)
      if (!detail.inbound) {
        setEditInboundLoadError(detail.inbound_error || t('admin:nodes.edit_inbound_dialog.missing_configuration'))
        return
      }
      const ib = detail.inbound
      if (typeof ib.protocol !== 'string' || !ib.protocol.trim() ||
          typeof ib.port !== 'number' || !Number.isInteger(ib.port) || ib.port < 1 || ib.port > 65535) {
        throw new Error(t('admin:nodes.edit_inbound_dialog.invalid_configuration'))
      }
      if (ib.protocol !== 'vless' && ib.protocol !== 'shadowsocks' &&
          ib.protocol !== 'vmess' && ib.protocol !== 'trojan' &&
          ib.protocol !== 'hysteria2' && ib.protocol !== 'anytls' &&
          ib.protocol !== 'tuic' && ib.protocol !== 'naive') {
        setEditInboundUnsupported('protocol')
        return
      }
      // Malformed snapshots are read failures, not unsupported protocols. The
      // forgiving parser below is only safe after verifying JSON object shape.
      for (const raw of [ib.settings, ib.stream_settings, ib.sniffing]) {
        if (typeof raw !== 'string') throw new Error(t('admin:nodes.edit_inbound_dialog.invalid_configuration'))
        const value: unknown = JSON.parse(raw || '{}')
        if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(t('admin:nodes.edit_inbound_dialog.invalid_configuration'))
      }
      if (ib.protocol === 'shadowsocks') {
        const method = parseJSONSafe(ib.settings).method
        if (!SS2022_METHODS.some(candidate => candidate.value === method)) {
          setEditInboundUnsupported('ss_method')
          return
        }
      }
      setEditInboundForm(parseInboundForEdit(detail.node, ib as InboundDetail))
      setEditInboundSyncNotice(['pending', 'drift', 'failed'].includes(detail.node.config_sync_state || '') || !!detail.inbound_error)
      setEditInboundReady(true)
    } catch (err) {
      if (editInboundIntent.current !== intent) return
      const reason = err as { response?: { data?: { error?: string } }; message?: string }
      setEditInboundLoadError(reason.response?.data?.error || reason.message || t('admin:nodes.edit_inbound_dialog.missing_configuration'))
    } finally { if (editInboundIntent.current === intent) setEditInboundLoading(false) }
  }

  // `afterSave`: the inbound was just saved, and a dirty source draft was
  // already asked about before that save, so nothing is asked again.
  async function closeEditInbound(afterSave = false) {
    // The source-country section saves on its own; closing must not drop its draft silently.
    if (!afterSave && sourceDirtyRef.current?.dirty() && !(await confirm(discardChangesConfirm(t)))) return
    editInboundIntent.current += 1
    setEditInboundReady(false)
    setEditInboundForm(EMPTY_INBOUND)
    onClose()
  }

  async function genKeysForEdit() {
    if (!editInboundReady) return
    const intent = editInboundIntent.current
    setEditInboundGenBusy(true)
    try {
      const kp = await generateRealityKeypair()
      if (editInboundIntent.current !== intent) return
      setEditInboundForm(f => ({
        ...f,
        private_key: kp.private_key,
        public_key: kp.public_key,
        short_ids_text: kp.short_id,
      }))
    } finally { if (editInboundIntent.current === intent) setEditInboundGenBusy(false) }
  }

  function genSSPasswordEdit() {
    const method = SS2022_METHODS.find(m => m.value === editInboundForm.ss_method)
    setEditInboundForm(f => ({ ...f, ss_password: randomBase64(method?.bytes ?? 32) }))
  }
  async function submitEditInbound(e: FormEvent) {
    e.preventDefault()
    if (!editingInboundNode || !editInboundReady || editInboundLoading || editInboundLoadError || editInboundUnsupported || editInboundBusy) return
    const f = editInboundForm
    const panelType = servers.find(s => s.id === editingInboundNode.panel_id)?.panel_type ?? '3xui'
    if (editAdvanced) {
      for (const [label, raw] of [['settings', f.raw_settings], ['streamSettings', f.raw_stream_settings], ['sniffing', f.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') }
        catch { pushSnack(t('admin:nodes.create_dialog.advanced_invalid_json', { field: label, defaultValue: `${label} 不是合法的 JSON` }), 'warning'); return }
      }
    } else {
      const err = validateInboundForm(f, panelType, t)
      if (err) { pushSnack(err, 'warning'); return }
    }
    const settings = editAdvanced
      ? (f.raw_settings || '{}')
      : JSON.stringify(settingsBuilderFor(f.protocol)(f))
    const streamSettings = editAdvanced
      ? (f.raw_stream_settings || '{}')
      : JSON.stringify(buildStreamSettings(f))
    const sniffing = editAdvanced
      ? (f.raw_sniffing || '{}')
      : panelType === '3xui' ? JSON.stringify(buildSniffing(f)) : '{}'
    // The source-country draft is a different endpoint with its own [Save]. A
    // dirty one asks the ordinary "discard unsaved changes?": Yes saves the
    // inbound only and drops the draft; No or a dismiss aborts this OK, so both
    // drafts stay as they are.
    if (sourceDirtyRef.current?.dirty() && !(await confirm(discardChangesConfirm(t)))) return
    setEditInboundBusy(true)
    try {
      await updateInboundConfig(editingInboundNode.id, {
        remark: f.display_name,
        enable: panelType === '3xui' ? f.enable : true,
        listen: f.listen,
        port: f.port,
        protocol: canonicalProtocolName(f.protocol),
        settings, stream_settings: streamSettings, sniffing,
        allocate: '',
        cert_source: f.tls_cert_mode === 'psp_managed' && f.cert_id > 0 ? 'psp_managed' : undefined,
        cert_id: f.tls_cert_mode === 'psp_managed' && f.cert_id > 0 ? f.cert_id : undefined,
      })
      // VLESS flow is a node-level property (stored on the node row, consumed by
      // the subscription renderers) — updateInboundConfig does NOT carry it, so
      // a flow change made in this dialog would otherwise be silently dropped.
      // Persist it via the metadata endpoint when it actually changed, mirroring
      // the metadata-edit dialog. (Non-VLESS protocols clear it.)
      const newFlow = f.protocol === 'vless' ? f.vless_flow.trim() : ''
      if (newFlow !== (editingInboundNode.flow ?? '')) {
        try {
          await updateNodeMetadata(editingInboundNode.id, {
            display_name: editingInboundNode.display_name,
            server_address: editingInboundNode.server_address,
            flow: newFlow || undefined,
            region: editingInboundNode.region,
            tags: editingInboundNode.tags ?? [],
            // No sort_order: an explicit value is a deliberate move to the order writer, so a dialog
            // opened before a drag and saved after it would put the node back.
          })
        } catch { /* toast via interceptor */ }
      }
      // Reconcile the managed-cert binding (the unified Certificate-source
      // selector). The update request already asks the backend to inject a
      // managed cert before the panel update; this call persists/reconciles the
      // binding and is content-diff-gated, so it does not cause a second push.
      const wantManaged = f.tls_cert_mode === 'psp_managed' && f.cert_id > 0
      const prevManaged = editingInboundNode.cert_source === 'psp_managed'
      try {
        if (wantManaged) {
          await setNodeCertSource(editingInboundNode.id, 'psp_managed', f.cert_id)
        } else if (prevManaged) {
          // Switched away from managed → unbind so the renewal worker stops
          // re-deploying. The manual cert (if any) was already pushed above.
          await setNodeCertSource(editingInboundNode.id, 'manual', 0)
        }
      } catch { /* toast via interceptor */ }
      pushSnack(t('admin:nodes.edit_inbound_dialog.saved'), 'success')
      void closeEditInbound(true)
      onSaved()
    } finally { setEditInboundBusy(false) }
  }

  // Open on mount: the host mounts one dialog per request. The read is keyed to the intent counter inside.
  useEffect(() => { void openEditInbound(node) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
      <Dialog open onClose={() => !editInboundBusy && void closeEditInbound()}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 800, maxWidth: '95vw' } }
        }}>
        <DialogTitle sx={{ pt: 2.5, pb: 1, fontSize: 18 }}>
          {t('admin:nodes.edit_inbound_dialog.title')}{editingInboundNode ? ` — ${editingInboundNode.display_name}` : ''}
        </DialogTitle>
        <DialogContent sx={{ pt: 1 }}>
          {editInboundLoading ? (
            <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}><CircularProgress size={24} /></Box>
          ) : editInboundLoadError ? (
            <Alert severity="error">
              {t('admin:nodes.edit_inbound_dialog.load_failed', { error: editInboundLoadError })}
              <Typography variant="body2">{t('admin:nodes.edit_inbound_dialog.load_next')}</Typography>
            </Alert>
          ) : editInboundUnsupported ? (
            <Typography sx={{ color: md.onSurfaceVariant, py: 2 }}>
              {t(editInboundUnsupported === 'ss_method' ? 'admin:nodes.edit_inbound_dialog.unsupported_ss_method' : 'admin:nodes.edit_inbound_dialog.unsupported')}
            </Typography>
          ) : editInboundReady ? (
            <Box component="form" id="edit-inbound-form" onSubmit={submitEditInbound}>
              {editInboundSyncNotice && <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>
                {t('admin:nodes.edit_inbound_dialog.sync_notice')}
              </Typography>}
              <InboundFormFields form={editInboundForm} setForm={setEditInboundForm}
                showMetadata={false}
                server={servers.find(s => s.id === editingInboundNode?.panel_id)}
                scanSourceName={editingInboundNode?.panel_name}
                onGenKeys={genKeysForEdit}
                onGenSSPassword={genSSPasswordEdit}
                genKeysBusy={editInboundGenBusy}
                protocolReadonly
                advanced={editAdvanced}
                onSetAdvanced={setEditAdvanced}
                panelType={servers.find(s => s.id === editingInboundNode?.panel_id)?.panel_type}
                allowRealityScan={!!editingInboundNode && panelSupports(editingInboundNode.panel_id, 'reality.scan')}
              />
              {/* A native panel's inbound can deny source countries. A separate endpoint
                  from the form above, so it has its own Save and is not part of OK. */}
              {canAccess && editingInboundNode && servers.find(s => s.id === editingInboundNode.panel_id)?.panel_type === 'psp' && (
                <SourceRulesSection nodeId={editingInboundNode.id} dirtyRef={sourceDirtyRef} />
              )}
            </Box>
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => void closeEditInbound()} disabled={editInboundBusy} variant="text">{t('common:actions.cancel')}</Button>
          {!!editInboundLoadError && <Button variant="outlined" disabled={editInboundLoading || !editingInboundNode}
            onClick={() => editingInboundNode && void openEditInbound(editingInboundNode)}>{t('common:actions.retry')}</Button>}
          {editInboundReady && !editInboundUnsupported && !editInboundLoading && !editInboundLoadError && (
            <Button type="submit" form="edit-inbound-form" variant="contained" disabled={editInboundBusy}
              startIcon={editInboundBusy ? <CircularProgress size={16} color="inherit" /> : null}>
              {t('common:actions.ok')}
            </Button>
          )}
        </DialogActions>
      </Dialog>
  )
}
