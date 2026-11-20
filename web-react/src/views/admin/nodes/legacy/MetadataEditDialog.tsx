// AUTOIMPORT-BEGIN
import ProbeQualityChip from '../../ProbeQualityChip'
import LinkQualityCell from '../../LinkQualityCell'
import PreviewCaption from '@/components/PreviewCaption'
import TagsAutocomplete from '../../TagsAutocomplete'
import TransitLinkFields from '../../TransitLinkFields'
import RoutesSection from '../../routes/RoutesSection'
import { nodeGroupsHint, useLineVisibility } from '../../useLineVisibility'
import { EGRESS_READY_POLL_MS, MetaForm } from './inboundModel'
import { splitTagsText } from '@/api/lineLabels'
import { type LineVisibilityDraft, type TransitLink, getNode, getTransitLink, updateNodeMetadata, updateTransitLink } from '@/api/nodes'
import { PanelCapability, Server } from '@/api/servers'
import { Node } from '@/api/types'
import { pushSnack } from '@/components/SnackbarHost'
import { type FieldErrors, firstError, validateHost, validateName } from '@/utils/validators'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { Accordion, AccordionDetails, AccordionSummary, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, TextField, Typography, useTheme } from '@mui/material'
import { type FormEvent, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// AUTOIMPORT-END
// The legacy node metadata editor: name, address, region, tags, the exit and the routes by destination. The lines
// are no longer here: they are edited one at a time in the line editor (N2, views/admin/nodes/lines), which is
// also where a line's enable switch, multiplier and the direct line's switch live. This dialog stays on MUI until
// N3 (General and Exit tabs) replaces it. The host (LegacyEditors) mounts it for the node being edited and
// unmounts it on close. It never sends `relays` or `hide_direct`, so saving here leaves every line as it is.
// AUTOIMPORT-BEGIN
// AUTOIMPORT-END

/** What this dialog edits of a node. */
type EditForm = Omit<MetaForm, 'relays' | 'hide_direct' | 'traffic_factor_milli' | 'traffic_factor_text'>

function editFormOf(n: Node): EditForm {
  return {
    display_name: n.display_name,
    server_address: n.server_address,
    flow: n.flow ?? '',
    region: n.region,
    tags_text: (n.tags ?? []).join(', '),
    sort_order: n.sort_order,
    egress_panel_id: n.egress_panel_id ?? 0,
  }
}

export interface MetadataEditProps {
  node: Node
  servers: Server[]
  /** Every node (for the listener banner and the default-exit names). */
  nodes: Node[]
  allTags: string[]
  onClose: () => void
  onOpenInbound: (node: Node) => void
  /** Patches the cached node list after a write. */
  mutateManaged: (updater: (prev: Node[]) => Node[]) => void
  onSaved: () => void
}

export default function MetadataEditDialog({ node, servers, nodes: managed, allTags, onClose, onOpenInbound, mutateManaged, onSaved }: MetadataEditProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  // The host mounts this component only while the editor is open.
  const editOpen = true
  const [editBusy, setEditBusy] = useState(false)
  const [editing, setEditing] = useState<Node | null>(node)
  const [editForm, setEditForm] = useState<EditForm>(() => editFormOf(node))
  // Which groups would see the node with the unsaved address, region and tags (ADR 0041). The node's own lines are
  // sent as saved: they are not edited here.
  const lineVisibilityDraft = useMemo<LineVisibilityDraft | null>(() => {
    if (!editOpen || !editing) return null
    return {
      server_address: editForm.server_address,
      region: editForm.region,
      tags: splitTagsText(editForm.tags_text),
      relays: (editing.relays ?? []).map(r => ({ id: r.id, region: (r.region ?? '').trim(), tags: r.tags ?? [] })),
    }
  }, [editOpen, editing, editForm.server_address, editForm.region, editForm.tags_text])
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
  function validateMeta(f: EditForm): FieldErrors<MetaField> {
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
      // Only what this dialog edits: no relays and no hide_direct (a save here leaves every line as it is), no
      // direct multiplier (the direct line's editor owns it), and no sort_order (positions are written only by
      // PUT /nodes/order; see InboundEditDialog).
      const saved = await updateNodeMetadata(editing.id, {
        display_name: editForm.display_name,
        server_address: editForm.server_address,
        flow: editForm.flow || undefined,
        region: editForm.region,
        tags: editForm.tags_text
          ? editForm.tags_text.split(',').map(s => s.trim()).filter(Boolean)
          : [],
        egress_panel_id: egress,
      })
      mutateManaged(prev => prev.map(node => node.id === saved.id ? saved : node))
      setEditing(saved)
      pushSnack(t('admin:nodes.toast.saved'), 'success')
      onSaved()
      onClose()
    } catch {
      // The request layer already surfaced the failure.
    } finally { setEditBusy(false) }
  }

  return (
      <Dialog open onClose={() => !editBusy && onClose()}
        slotProps={{
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
            {!isListener && (editing?.relays ?? []).length > 0 && (
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
