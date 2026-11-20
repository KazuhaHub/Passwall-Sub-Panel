// AUTOIMPORT-BEGIN
import CdnLineFields from '../../CdnLineFields'
import { CDN_DEFAULT_PORT } from '../../CdnLineFields'
import LineLabelsFields from '../../LineLabelsFields'
import LinkQualityCell from '../../LinkQualityCell'
import ProbeQualityChip from '../../ProbeQualityChip'
import SourceAddressesField from '../../SourceAddressesField'
import TagsAutocomplete from '../../TagsAutocomplete'
import TransitLinkFields from '../../TransitLinkFields'
import TunnelLineEditor from '../../TunnelLineEditor'
import { mergeServerLine } from '../../TunnelLineEditor'
import RoutesSection from '../../routes/RoutesSection'
import { lineVisibilityHint, nodeGroupsHint, useLineVisibility } from '../../useLineVisibility'
import { CDN_ENTRY, EGRESS_READY_POLL_MS, MetaForm, RelayDraft, newRelayKey } from './inboundModel'
import { lineLabelsInput, splitTagsText } from '@/api/lineLabels'
import { type LineVisibilityDraft, type TransitLink, getNode, getTransitLink, updateNodeMetadata, updateTransitLink } from '@/api/nodes'
import { PanelCapability, Server } from '@/api/servers'
import { type NodeSourceGuard, addRefusedSource, getNodeSourceGuard, parseSourceCIDRs } from '@/api/sourceGuard'
import { Node, RelayLine } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import PreviewCaption from '@/components/PreviewCaption'
import { pushSnack } from '@/components/SnackbarHost'
import { TrafficFactorField } from '@/components/TrafficFactorField'
import { DEFAULT_TRAFFIC_FACTOR_MILLI, factorTextToMilli, milliToFactorText } from '@/utils/trafficFactor'
import { type FieldErrors, firstError, validateHost, validateName } from '@/utils/validators'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { Accordion, AccordionDetails, AccordionSummary, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton, MenuItem, Switch, TextField, Tooltip, Typography, alpha, useTheme } from '@mui/material'
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// AUTOIMPORT-END
// The legacy node metadata editor (name, address, region, tags, exit, routes and every line), moved out of the
// old NodesView.tsx with its behaviour unchanged. It stays on MUI until N2 (line editor) and N3 (General and
// Exit tabs) replace it. The host (LegacyEditors) mounts it for the node being edited and unmounts it on close.
// AUTOIMPORT-BEGIN
// AUTOIMPORT-END

function editFormOf(n: Node): MetaForm {
  return {
    display_name: n.display_name,
    server_address: n.server_address,
    flow: n.flow ?? '',
    region: n.region,
    tags_text: (n.tags ?? []).join(', '),
    sort_order: n.sort_order,
    relays: (n.relays ?? []).map((r, i) => ({ ...r, only_line_name: !!r.only_line_name, _key: r.id ?? `saved-${i}` })),
    hide_direct: !!n.hide_direct,
    traffic_factor_milli: n.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI,
    egress_panel_id: n.egress_panel_id ?? 0,
  }
}

export interface MetadataEditProps {
  node: Node
  /** The line to scroll to; or, with addLine, a blank line is appended. */
  lineId?: string | null
  addLine?: boolean
  servers: Server[]
  /** Every node (for the listener banner and the default-exit names). */
  nodes: Node[]
  allTags: string[]
  onClose: () => void
  /** A tunnel listener opens in this same editor. */
  onSwitchNode: (node: Node) => void
  onOpenInbound: (node: Node) => void
  /** Patches the cached node list after a write. */
  mutateManaged: (updater: (prev: Node[]) => Node[]) => void
  onSaved: () => void
}

export default function MetadataEditDialog({ node, lineId, addLine, servers, nodes: managed, allTags, onClose, onSwitchNode, onOpenInbound, mutateManaged, onSaved }: MetadataEditProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  // The host mounts this component only while the editor is open.
  const editOpen = true
  const [editBusy, setEditBusy] = useState(false)
  const [editing, setEditing] = useState<Node | null>(node)
  const editLineTarget = useRef<string | null>(addLine ? null : lineId ?? null)
  const [editForm, setEditForm] = useState<MetaForm>(() => {
    const form = editFormOf(node)
    if (!addLine) return form
    editLineTarget.current = newRelayKey()
    return { ...form, relays: [...form.relays, { _key: editLineTarget.current, name: '', address: '', port: 0, enabled: true }] }
  })
  // Which groups would see each line with the unsaved labels (ADR 0041). Rows
  // keep editor order, including rows the save would drop for a blank address.
  const lineVisibilityDraft = useMemo<LineVisibilityDraft | null>(() => {
    if (!editOpen || !editing) return null
    const labels = (line: { region?: string; tags?: string[] }) => ({ region: (line.region ?? '').trim(), tags: line.tags ?? [] })
    return {
      server_address: editForm.server_address,
      region: editForm.region,
      tags: splitTagsText(editForm.tags_text),
      relays: editForm.relays.map(r => ({ id: r.id, ...labels(r) })),
    }
  }, [editOpen, editing, editForm.server_address, editForm.region, editForm.tags_text, editForm.relays])
  const lineVisibility = useLineVisibility(editing?.id || 0, lineVisibilityDraft)
  type MetaField = 'display_name' | 'server_address' | 'region'
  const [editMetaErr, setEditMetaErr] = useState<FieldErrors<MetaField>>({})
  const relayEntryServers = useMemo(
    () => servers.filter(s => s.panel_type === 'psp' && s.id !== editing?.panel_id),
    [servers, editing?.panel_id],
  )
  // The editor opened on a tunnel line's hidden listener: the server takes its
  // name, address, exit and multiplier from the line, so those fields are left out.
  const isListener = (editing?.parent_node_id ?? 0) > 0
  const listenerLineName = isListener
    ? managed.find(n => n.id === editing?.parent_node_id)?.relays?.find(x => x.id === editing?.parent_line_id)?.name
    : undefined
  // The tunnel of the node being edited, between its server and the chosen
  // exit. Its settings are shared by every node of the pair.
  const [linkForm, setLinkForm] = useState<TransitLink | null>(null)
  const [linkDirty, setLinkDirty] = useState(false)
  const [linkError, setLinkError] = useState(false)
  const editEgress = editForm.egress_panel_id ?? 0
  // What the panel says of the saved node's lines' source addresses (ADR 0044):
  // the set in force, the Cloudflare ranges, and the sources its landing refused.
  const [sourceGuard, setSourceGuard] = useState<NodeSourceGuard | null>(null)
  const [sourceBusy, setSourceBusy] = useState(false)
  const editingId = editing?.id ?? 0
  useEffect(() => {
    setSourceGuard(null)
    if (!editOpen || editingId <= 0) return
    const controller = new AbortController()
    getNodeSourceGuard(editingId, { signal: controller.signal })
      .then(setSourceGuard)
      .catch(() => { /* The editor works without it. */ })
    return () => controller.abort()
  }, [editOpen, editingId])
  async function addSource(lineId: string, ip: string) {
    setSourceBusy(true)
    try {
      const saved = await addRefusedSource(editingId, lineId, ip)
      // The address is saved; the line's own list in the form follows it.
      setEditForm(f => ({ ...f, relays: f.relays.map(x => (x.id === lineId ? { ...x, source_cidrs: saved, _source_text: undefined } : x)) }))
      editBaseline.current = editBaseline.current && {
        ...editBaseline.current,
        relays: editBaseline.current.relays.map(x => (x.id === lineId ? { ...x, source_cidrs: saved } : x)),
      }
      pushSnack(t('admin:nodes.source.added', { ip }), 'success')
      setSourceGuard(await getNodeSourceGuard(editingId))
    } catch {
      // The request layer already surfaced the failure.
    } finally { setSourceBusy(false) }
  }
  useEffect(() => {
    if (!editOpen || !editing || editEgress <= 0) return
    let cancelled = false
    setLinkError(false)
    getTransitLink(editing.panel_id, editEgress)
      .then(link => { if (!cancelled) { setLinkForm(link); setLinkDirty(false) } })
      .catch(() => { if (!cancelled) { setLinkForm(null); setLinkError(true) } })
    return () => { cancelled = true }
  }, [editOpen, editing, editEgress])
  // The editor opens on the list's copy of the node, which says whether the
  // tunnel ran on both servers when the list was loaded. While it is pending,
  // ask again, so the status turns to ready without reopening the page. The
  // answer is kept apart from `editing`: replacing that would reload the
  // tunnel settings above and drop unsaved changes to them.
  const [egressReadyFor, setEgressReadyFor] = useState<Node | null>(null)
  const egressReady = !!editing?.egress_ready || (editing !== null && egressReadyFor === editing)
  useEffect(() => {
    const node = editing
    const exit = node?.egress_panel_id ?? 0
    if (!editOpen || !node || exit <= 0 || egressReady) return
    const controller = new AbortController()
    let inFlight = false
    const timer = setInterval(() => {
      if (inFlight) return
      inFlight = true
      getNode(node.id, { signal: controller.signal, quiet: true })
        .then(({ node: fresh }) => {
          if (controller.signal.aborted || !fresh.egress_ready || fresh.egress_panel_id !== exit) return
          setEgressReadyFor(node)
          // Only the row this answer was asked for: a row replaced since
          // (a save, a refetch) already holds something newer.
          mutateManaged(prev => prev.map(x => x === node ? { ...x, egress_ready: true } : x))
        })
        .catch(() => {})
        .finally(() => { inFlight = false })
    }, EGRESS_READY_POLL_MS)
    return () => { controller.abort(); clearInterval(timer) }
  // mutateManaged only writes the query cache; it is not a reason to restart.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editOpen, editing, egressReady])
  const patchLink = (patch: Partial<TransitLink>) => {
    setLinkForm(prev => (prev ? { ...prev, ...patch } : prev))
    setLinkDirty(true)
  }
  const panelSupports = (panelID: number, capability: PanelCapability) =>
    !!servers.find(s => s.id === panelID)?.capabilities?.includes(capability)
  const editBaseline = useRef<MetaForm | null>(editFormOf(node))
  // The form as the admin has changed it, without what the server decides
  // (status, a tunnel line's listener and port), for telling edits from a refresh.
  const editSnapshot = (f: MetaForm) => JSON.stringify({
    ...f,
    relays: f.relays.map(({ tunnel: _tunnel, running: _running, managed_ready: _ready, probe: _probe, probe_withheld: _withheld, cdn_issues: _issues, source_withheld: _sourceWithheld, mode: _mode, ...r }) =>
      (_mode === 'tunnel' ? { ...r, port: 0, sni: '', host: '' } : r)),
  })
  const editFormDirty = () => linkDirty || (editBaseline.current !== null && editSnapshot(editForm) !== editSnapshot(editBaseline.current))

  // The server settled one saved line (a mode switch, a listener edit or sync):
  // show it on that row, keep every other edit, and keep the list's copy current.
  function applyServerLine(landingId: number, relays: RelayLine[], lineId: string) {
    setEditForm(f => ({ ...f, relays: mergeServerLine(f.relays, relays, lineId) }))
    if (editBaseline.current) editBaseline.current = { ...editBaseline.current, relays: mergeServerLine(editBaseline.current.relays, relays, lineId) }
    mutateManaged(prev => prev.map(x => (x.id === landingId ? { ...x, relays } : x)))
  }
  // A tunnel line's listener is a node of its own, hidden from the list: its
  // settings open in this same editor. Unsaved edits to the landing are not carried over.
  async function openListener(listenerId: number) {
    if (editFormDirty()) {
      const ok = await confirm({
        title: t('admin:nodes.tunnel.open_discard_title'),
        message: t('admin:nodes.tunnel.open_discard_message'),
        destructive: true,
      })
      if (!ok) return
    }
    try {
      const { node: listener } = await getNode(listenerId)
      onSwitchNode(listener)
    } catch {
      // The request layer already surfaced the failure.
    }
  }
  function validateMeta(f: MetaForm): FieldErrors<MetaField> {
    return {
      display_name: validateName(f.display_name, { required: true, max: 64 }),
      server_address: validateHost(f.server_address, { required: true }),
      region: validateName(f.region, { required: true, max: 32 }),
    }
  }

  async function submitEdit(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    const errs = validateMeta(editForm)
    setEditMetaErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    // An invalid or empty multiplier keeps the last valid value in the form, so
    // saving would silently write the old number: block instead.
    if ((editForm.traffic_factor_text !== undefined && factorTextToMilli(editForm.traffic_factor_text) === null)
      || editForm.relays.some(r => r._factor_text !== undefined && factorTextToMilli(r._factor_text) === null)) {
      pushSnack(t('admin:nodes.validate.traffic_factor'), 'warning'); return
    }
    // A CDN line without a domain is not dropped like a blank row: it blocks the save.
    if (editForm.relays.some(r => r.mode === 'cdn' && !r.domain?.trim())) {
      pushSnack(t('admin:nodes.cdn.domain_required'), 'warning'); return
    }
    setEditBusy(true)
    try {
      const egress = editForm.egress_panel_id ?? 0
      if (egress > 0 && linkForm && linkDirty) {
        const reality = linkForm.protocol === 'vless_reality'
        const link = await updateTransitLink(editing.panel_id, egress, {
          protocol: linkForm.protocol,
          exit_address: linkForm.exit_address.trim(),
          reality_server_name: reality ? linkForm.reality_server_name.trim() : undefined,
          reality_target: reality ? linkForm.reality_target.trim() : undefined,
        })
        setLinkForm(link)
        setLinkDirty(false)
      }
      const saved = await updateNodeMetadata(editing.id, {
        display_name: editForm.display_name,
        server_address: editForm.server_address,
        flow: editForm.flow || undefined,
        region: editForm.region,
        tags: editForm.tags_text
          ? editForm.tags_text.split(',').map(s => s.trim()).filter(Boolean)
          : [],
        // No sort_order: see InboundEditDialog; positions are written only by PUT /nodes/order.
        // Drop blank-address lines, trim, and coerce port to a number so a
        // half-filled row never reaches the backend (which would 400).
        relays: editForm.relays
          .map(({ managed_ready: _ready, probe: _probe, probe_withheld: _probeWithheld, running: _running, tunnel: _tunnel, cdn_issues: _issues, source_withheld: _sourceWithheld, _source_text, _key, _factor_text, _factor_touched, traffic_factor_milli, ...r }) => ({ ...r, ...(r.mode === 'tunnel' ? { source_cidrs: undefined } : _source_text !== undefined ? { source_cidrs: parseSourceCIDRs(_source_text) } : {}), ...(_factor_touched && traffic_factor_milli !== undefined ? { traffic_factor_milli } : {}), name: r.name.trim(), only_line_name: !!r.only_line_name, address: r.address.trim(), domain: r.mode === 'cdn' ? r.domain?.trim() : undefined, port: Number(r.port) || 0, sni: r.sni?.trim() || undefined, host: r.host?.trim() || undefined, entry_panel_id: r.entry_panel_id || undefined, ...lineLabelsInput(r) }))
          // A CDN line may leave the address empty (it is the domain); every other line needs one.
          .filter(r => r.address || (r.mode === 'cdn' && r.domain)),
        hide_direct: editForm.hide_direct,
        // Direct factor only when changed; relay lines carry their own value above.
        ...(editForm.traffic_factor_milli !== undefined
          && editForm.traffic_factor_milli !== (editing.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI)
          ? { traffic_factor_milli: editForm.traffic_factor_milli } : {}),
        egress_panel_id: egress,
      })
      mutateManaged(prev => prev.map(node => node.id === saved.id ? saved : node))
      setEditing(saved)
      pushSnack(t('admin:nodes.toast.saved'), 'success')
      // What the saved node says about its CDN lines is shown on the lines when
      // the editor is opened again; do not let a likely problem pass unseen.
      const flagged = (saved.relays ?? []).filter(r => r.mode === 'cdn' && r.cdn_issues?.some(i => i.warn))
      if (flagged.length > 0) {
        pushSnack(t('admin:nodes.cdn.saved_warnings', { lines: flagged.map(r => r.name || r.domain).join(', ') }), 'warning')
      }
      onSaved()
      onClose()
    } catch {
      // The request layer already surfaced the failure.
    } finally { setEditBusy(false) }
  }

  return (
      <Dialog open onClose={() => !editBusy && onClose()}
        slotProps={{
          transition: { onEntered: () => {
            const target = editLineTarget.current
            if (target) document.getElementById(`node-line-${target}`)?.scrollIntoView?.({ block: 'start' })
          } },
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }
        }}>
        <DialogTitle>
          {t('admin:nodes.edit_title')} — {editing?.display_name}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="node-form" onSubmit={submitEdit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            {isListener && (
              <Box data-testid="listener-banner" sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
                <Typography sx={{ fontSize: 13 }}>
                  {listenerLineName
                    ? t('admin:nodes.tunnel.listener_banner', { parent: editing?.parent_node_name ?? `#${editing?.parent_node_id}`, line: listenerLineName })
                    : t('admin:nodes.tunnel.listener_banner_no_line', { parent: editing?.parent_node_name ?? `#${editing?.parent_node_id}` })}
                </Typography>
                {editing && panelSupports(editing.panel_id, 'inbound.update') && (
                  <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => onOpenInbound(editing)}>{t('admin:nodes.tunnel.listener_inbound')}</Button>
                )}
              </Box>
            )}
            {!isListener && <TextField required fullWidth label={t('admin:nodes.field.display_name')}
              value={editForm.display_name}
              onChange={e => setEditForm({ ...editForm, display_name: e.target.value })}
              error={!!editMetaErr.display_name}
              helperText={editMetaErr.display_name ? t(`admin:${editMetaErr.display_name}`) : t('admin:nodes.field.display_name_factor_hint', { placeholder: '{{ factor }}' })} />}
            {!isListener && <TextField required fullWidth label={t('admin:nodes.field.server_address')}
              value={editForm.server_address}
              onChange={e => setEditForm({ ...editForm, server_address: e.target.value })}
              error={!!editMetaErr.server_address}
              helperText={editMetaErr.server_address ? t(`admin:${editMetaErr.server_address}`) : ''} />}
            {/* Flow is a VLESS inbound-level setting, configured where the
                inbound is created / imported — not a node-meta field. It's
                deliberately NOT editable here; editForm.flow round-trips the
                node's existing value unchanged on save. */}
            {!isListener && <TextField required fullWidth label={t('admin:nodes.field.region')}
              value={editForm.region}
              onChange={e => setEditForm({ ...editForm, region: e.target.value })}
              error={!!editMetaErr.region}
              helperText={editMetaErr.region ? t(`admin:${editMetaErr.region}`) : ''} />}
            {!isListener && <TagsAutocomplete
              label={t('admin:nodes.field.tags')}
              value={editForm.tags_text}
              options={allTags}
              onChange={v => setEditForm({ ...editForm, tags_text: v })} />}
            {!isListener && (() => {
              const nodeGroups = nodeGroupsHint(lineVisibility)
              return <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, mt: -1.5 }}>
                <PreviewCaption hint={nodeGroups.now} testId="node-groups"
                  readyText={groups => t('admin:nodes.node_groups.visible', { groups: groups.join(t('admin:nodes.line_labels.group_separator')) })}
                  noneText={t('admin:nodes.node_groups.none')} unknownText={t('admin:nodes.line_labels.visibility_unknown')} />
                {nodeGroups.removed.length > 0 && <Typography data-testid="node-groups-removed" sx={{ fontSize: 12, color: md.error }}>
                  {t('admin:nodes.node_groups.removed', { groups: nodeGroups.removed.join(t('admin:nodes.line_labels.group_separator')) })}
                </Typography>}
              </Box>
            })()}
            {!isListener && (editForm.relays.length > 0 || editLineTarget.current === 'direct') && (
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: -1.5 }}>{t('admin:nodes.line_labels.node_change_hint')}</Typography>
            )}

            {!isListener && editing && servers.find(s => s.id === editing.panel_id)?.panel_type === 'psp' && relayEntryServers.length > 0 && (
              <Box data-testid="egress-section" sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                <TextField select size="small" label={t('admin:nodes.egress.server')} value={editEgress}
                  onChange={e => {
                    const exit = Number(e.target.value) || 0
                    setEditForm(f => ({ ...f, egress_panel_id: exit }))
                    setLinkForm(null)
                    setLinkDirty(false)
                  }}
                  helperText={editEgress > 0 ? t('admin:nodes.egress.tunnel_hint') : t('admin:nodes.egress.local_hint')}>
                  <MenuItem value={0}>{t('admin:nodes.egress.local')}</MenuItem>
                  {relayEntryServers.map(server => (
                    <MenuItem key={server.id} value={server.id}>{server.name}</MenuItem>
                  ))}
                </TextField>
                {editEgress > 0 && editEgress === (editing.egress_panel_id ?? 0) && (
                  <Typography data-testid="egress-status" sx={{ fontSize: 12, color: egressReady ? md.primary : md.onSurfaceVariant }}>
                    {egressReady ? t('admin:nodes.egress.ready') : t('admin:nodes.egress.pending')}
                  </Typography>
                )}
                {editEgress > 0 && editEgress === (editing.egress_panel_id ?? 0) && (
                  <ProbeQualityChip probe={editing.egress_probe} withheld={editing.egress_withheld} testId="egress-probe" />
                )}
                {editEgress > 0 && linkError && (
                  <Typography sx={{ fontSize: 12, color: md.error }}>{t('admin:nodes.egress.load_failed')}</Typography>
                )}
                {editEgress > 0 && linkForm && (
                  <>
                    <LinkQualityCell entry={editing.panel_id} exit={editEgress} />
                    <Accordion key={editEgress} disableGutters elevation={0} variant="outlined">
                      <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                        <Typography variant="body2">{t('admin:nodes.egress.advanced')}</Typography>
                      </AccordionSummary>
                      <AccordionDetails sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                        <Typography variant="caption" color="text.secondary">{t('admin:nodes.egress.shared_hint')}</Typography>
                        <TransitLinkFields link={linkForm} onChange={patchLink} />
                      </AccordionDetails>
                    </Accordion>
                  </>
                )}
              </Box>
            )}

            {/* Routing by destination: ordered routes that send some destinations
                out through another server, or keep them here, while the rest
                leaves through the default exit above. Saved on its own. */}
            {editing && editing.kind !== 'separator' && servers.find(s => s.id === editing.panel_id)?.panel_type === 'psp' && (
              <RoutesSection nodeId={editing.id} entryPanelId={editing.panel_id} servers={relayEntryServers.map(s => ({ id: s.id, name: s.name }))} pendingExit={editEgress}
                defaultExitNodesOf={exit => {
                  const names = managed.filter(n => n.panel_id === editing.panel_id && (n.egress_panel_id ?? 0) === exit).map(n => n.display_name)
                  // The node being edited counts even when the list's copy is older.
                  if ((editing.egress_panel_id ?? 0) === exit && !managed.some(n => n.id === editing.id)) names.push(editing.display_name)
                  return names
                }} />
            )}

            {/* Transit / 中转 lines: the same landing offered through extra
                relay fronts. Server-side forwarding (realm/gost/iptables) is
                set up outside the panel; here we only record each front's
                address/port (+ optional CDN SNI/Host). */}
            {!isListener && <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{t('admin:nodes.relay.title')}</Typography>
                <Button size="small" startIcon={<AddIcon />}
                  onClick={() => setEditForm(f => ({ ...f, relays: [...f.relays, { _key: newRelayKey(), name: '', only_line_name: false, address: '', port: 0, sni: '', host: '', enabled: true, region: f.region.trim(), tags: splitTagsText(f.tags_text) }] }))}>
                  {t('admin:nodes.relay.add')}
                </Button>
              </Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.relay.hint')}</Typography>
              {editForm.relays.length === 0 ? (
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>{t('admin:nodes.relay.empty')}</Typography>
              ) : editForm.relays.map((r, i) => {
                const update = (patch: Partial<RelayDraft>) =>
                  setEditForm(f => ({ ...f, relays: f.relays.map((x, j) => (j === i ? { ...x, ...patch } : x)) }))
                return (
                  <Box key={r._key ?? i} id={`node-line-${r.id || r._key}`} sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
                    <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
                      <TextField size="small" label={t('admin:nodes.relay.name')} value={r.name}
                        onChange={e => update({ name: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.name_ph')}
                        helperText={t('admin:nodes.relay.name_factor_hint', { placeholder: '{{ factor }}' })} />
                      <FormControlLabel id="node-line-direct" sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} label={t('admin:nodes.relay.enabled')}
                        control={<Switch size="small" checked={r.enabled} onChange={(_, c) => update({ enabled: c })} />} />
                      <Tooltip title={t('admin:nodes.relay.remove')}>
                        <IconButton size="small" onClick={() => setEditForm(f => ({ ...f, relays: f.relays.filter((_, j) => j !== i) }))}>
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    </Box>
                    {/* The kind of line: an external entry, a Passwall Node server (a direct
                        forward or an encrypted tunnel, chosen below), or a CDN in front of the
                        landing. A CDN line is never turned into another kind, nor another into it. */}
                    <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5 }}>
                      <TextField select size="small" fullWidth label={t('admin:nodes.relay.entry')}
                        value={r.mode === 'cdn' ? CDN_ENTRY : (r.entry_panel_id ?? 0)}
                        disabled={r.mode === 'cdn' && !!r.id}
                        onChange={e => {
                          const entry = Number(e.target.value) || 0
                          if (entry === CDN_ENTRY) {
                            update({ mode: 'cdn', entry_panel_id: 0, domain: '', address: '', sni: '', host: '', port: CDN_DEFAULT_PORT, client_tls: undefined })
                          } else if (r.mode === 'cdn') {
                            update({ mode: undefined, domain: undefined, client_tls: undefined, entry_panel_id: entry, address: '', sni: '', host: '', port: 0 })
                          } else {
                            // A managed entry forwards unchanged: no SNI/Host override.
                            update(entry ? { entry_panel_id: entry, sni: '', host: '' } : { entry_panel_id: 0, mode: undefined })
                          }
                        }}
                        helperText={r.mode === 'cdn' ? t('admin:nodes.cdn.entry_hint')
                          : (r.entry_panel_id ?? 0) > 0 ? t('admin:nodes.relay.entry_managed_hint') : t('admin:nodes.relay.entry_external_hint')}>
                        <MenuItem value={0}>{t('admin:nodes.relay.entry_external')}</MenuItem>
                        <MenuItem value={CDN_ENTRY} disabled={!!r.id && r.mode !== 'cdn'}>{t('admin:nodes.relay.entry_cdn')}</MenuItem>
                        {relayEntryServers.map(server => (
                          <MenuItem key={server.id} value={server.id} disabled={r.mode === 'cdn' && !!r.id}>{server.name}</MenuItem>
                        ))}
                      </TextField>
                    </Box>
                    {r.mode === 'cdn' && <CdnLineFields line={r} onPatch={update} />}
                    {r.mode !== 'cdn' && (r.entry_panel_id ?? 0) > 0 && (
                      <TunnelLineEditor nodeId={editing?.id ?? 0} line={r}
                        savedEntry={editing?.relays?.find(x => x.id === r.id)?.entry_panel_id ?? 0}
                        onPatch={update}
                        onSwitched={node => applyServerLine(node.id, node.relays ?? [], r.id!)}
                        onRelays={relays => applyServerLine(editing?.id ?? 0, relays, r.id!)}
                        onOpenListener={id => void openListener(id)} />
                    )}
                    {r.mode !== 'cdn' && (r.entry_panel_id ?? 0) > 0 && r.id && r.mode !== 'tunnel' && (
                      <Typography sx={{ fontSize: 12, color: r.managed_ready ? md.primary : md.onSurfaceVariant }}>
                        {r.managed_ready ? t('admin:nodes.relay.managed_ready') : t('admin:nodes.relay.managed_pending')}
                      </Typography>
                    )}
                    {r.mode !== 'cdn' && (r.entry_panel_id ?? 0) > 0 && r.id && r.mode !== 'tunnel' && (
                      <ProbeQualityChip probe={r.probe} withheld={r.probe_withheld} testId="relay-probe" />
                    )}
                    {r.mode !== 'cdn' && <Box sx={{ display: 'flex', gap: 1 }}>
                      <TextField size="small" required label={t('admin:nodes.relay.address')} value={r.address}
                        onChange={e => update({ address: e.target.value })} sx={{ flex: 2 }} placeholder="relay.example.com" />
                      {r.mode !== 'tunnel' && <TextField size="small" type="number" label={t('admin:nodes.relay.port')} value={r.port || ''}
                        required={(r.entry_panel_id ?? 0) > 0}
                        onChange={e => update({ port: Number(e.target.value) || 0 })} sx={{ width: 120 }}
                        helperText={(r.entry_panel_id ?? 0) > 0 ? t('admin:nodes.relay.port_managed_hint') : t('admin:nodes.relay.port_hint')} />}
                    </Box>}
                    {r.mode !== 'cdn' && !((r.entry_panel_id ?? 0) > 0) && (
                    <Box sx={{ display: 'flex', gap: 1 }}>
                      <TextField size="small" label={t('admin:nodes.relay.sni')} value={r.sni ?? ''}
                        onChange={e => update({ sni: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.cdn_optional')} />
                      <TextField size="small" label={t('admin:nodes.relay.host')} value={r.host ?? ''}
                        onChange={e => update({ host: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.cdn_optional')} />
                    </Box>
                    )}
                    {r.mode !== 'tunnel' && (
                      <SourceAddressesField line={r}
                        text={r._source_text ?? (r.source_cidrs ?? []).join('\n')}
                        onText={text => update({ _source_text: text })}
                        guard={r.id ? sourceGuard?.lines.find(x => x.line_id === r.id) : undefined}
                        canGuard={sourceGuard?.can_guard}
                        cannotGuardReason={sourceGuard?.cannot_guard_reason}
                        cloudflare={sourceGuard?.cloudflare}
                        busy={sourceBusy}
                        onAdd={ip => { if (r.id) void addSource(r.id, ip) }} />
                    )}
                    <LineLabelsFields region={r.region ?? ''} tags={r.tags ?? []} tagOptions={allTags}
                      visibility={lineVisibilityHint(lineVisibility, 'relays', i)}
                      onChange={patch => update(patch)} />
                    <TrafficFactorField compact text={r._factor_text ?? milliToFactorText(r.traffic_factor_milli)}
                      onText={(text, m) => update({ _factor_text: text, _factor_touched: true, ...(m !== null ? { traffic_factor_milli: m } : {}) })} />
                    <FormControlLabel sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} label={t('admin:nodes.relay.only_line_name')}
                      control={<Switch size="small" checked={!!r.only_line_name} onChange={(_, checked) => update({ only_line_name: checked })} />} />
                  </Box>
                )
              })}
              <FormControlLabel sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} label={t('admin:nodes.relay.hide_direct')}
                control={<Switch size="small" checked={editForm.hide_direct} onChange={(_, checked) => setEditForm(f => ({ ...f, hide_direct: checked }))} />} />
              <TrafficFactorField label={t('admin:nodes.field.traffic_factor_direct')} text={editForm.traffic_factor_text ?? milliToFactorText(editForm.traffic_factor_milli)}
                onText={(text, m) => setEditForm(f => ({ ...f, traffic_factor_text: text, ...(m !== null ? { traffic_factor_milli: m } : {}) }))} />
            </Box>}
            <Box sx={{ display: 'none' }}>{alpha(md.error, 0.5)}</Box>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose} disabled={editBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="node-form" variant="contained" disabled={editBusy}
            startIcon={editBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
  )
}
