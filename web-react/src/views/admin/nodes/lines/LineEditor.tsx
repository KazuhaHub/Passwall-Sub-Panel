import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { frontErrorOf, suggestPort } from '@/api/fronts'
import { switchLineMode, type LineVisibilityDraft } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { addRefusedSource, getNodeSourceGuard, type NodeSourceGuard } from '@/api/sourceGuard'
import type { Node, RelayLine } from '@/api/types'
import { StatusDot } from '@/components/common/StatusDot'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { InfoTip } from '@/components/common/HelpTip'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { IconRandom } from '@/lib/icons'
import { useAllNodes, useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { milliToFactorText, factorTextToMilli } from '@/utils/trafficFactor'
import { lineVisibilityHint, useLineVisibility } from '../../useLineVisibility'
import { ServerSelect } from '../shared/ServerSelect'
import { useNodesT } from '../shared/useNodesT'
import { CdnFields } from './CdnFields'
import { DirectEditor } from './DirectEditor'
import { ID, PortInput, type FieldsProps } from './fieldKit'
import { LabelsFields } from './LabelsFields'
import { LineDiagram } from './LineDiagram'
import { ListenerCard } from './ListenerCard'
import { blankDraft, draftKey, draftOf, kindOf, validateDraft, type LineDraft, type LineField, type LineKind } from './lineModel'
import { saveLineChange } from './lineSave'
import { usePlanPresenter } from './PlanDialog'
import { ProbeChip } from './ProbeChip'
import { SourceAddresses } from './SourceAddresses'
import { TypeCards } from './TypeCards'
import { useApplyNode, useNodeDetail } from './useNodeDetail'

export interface LineEditorProps {
  nodeId: number
  /** The line's id, 'direct' for the node's own line, or null for a new line. */
  lineId: string | null
  /** Unsaved edits exist (the sheet asks before closing, the router before leaving). */
  onDirtyChange: (dirty: boolean) => void
  /** The line was saved; `addedId` is the id the server gave a new one. */
  onSaved: (result: { addedId?: string; isNew: boolean }) => void
}

const NONE: never[] = []

/** The address a server answers on, for a new line's entry address: what PSP last saw it sync from, else the host of its URL. */
export function hostOfServer(server: Server): string {
  if (server.address) return server.address
  try { return new URL(server.url).hostname } catch { return '' }
}

/**
 * The line editor (design 4.4): one line, one save. It reads the node fresh, then edits ONE line of it: a new line
 * starts with the four type cards, a saved line shows its kind and the fields of that kind, with the live path of
 * the draft at the top. The save re-reads the node and writes the lines back (see saveLineChange).
 */
export function LineEditor(props: LineEditorProps) {
  const t = useNodesT()
  const detail = useNodeDetail(props.nodeId)
  const scope = useQueryScope()
  const servers = useAllServers(scope)
  const nodes = useAllNodes(scope)
  const node = detail.data?.node
  if (detail.isError && !node) return <ErrorState compact reason={detail.error} retry={() => void detail.refetch()} />
  if (!node || servers.isPending) return <LoadingState />
  if (props.lineId !== null && props.lineId !== 'direct' && !(node.relays ?? []).some(r => r.id === props.lineId)) {
    return <EmptyState compact title={t('nodes.line.gone')} />
  }
  if (props.lineId === 'direct') return <DirectEditor node={node} onDirtyChange={props.onDirtyChange} onSaved={props.onSaved} />
  const tags = [...new Set((nodes.data ?? NONE).flatMap(n => n.tags ?? []))].sort((a, b) => a.localeCompare(b))
  return <LineForm key={`${node.id}.${props.lineId ?? 'new'}`} {...props} node={node} servers={servers.data ?? NONE} tagOptions={tags} />
}

function LineForm({ nodeId, lineId, node, servers, tagOptions, onDirtyChange, onSaved }: LineEditorProps & { node: Node; servers: readonly Server[]; tagOptions: readonly string[] }) {
  const t = useNodesT()
  const apply = useApplyNode()
  const { present, dialog } = usePlanPresenter()
  const typeLabel = useId()
  const isNew = lineId === null
  const live = (node.relays ?? []).find(r => r.id === lineId)
  const [kind, setKind] = useState<LineKind | null>(live ? kindOf(live) : null)
  const [choosing, setChoosing] = useState(false)
  const [draft, setDraft] = useState<LineDraft | null>(() => (live ? draftOf(live) : null))
  // The draft as it was last saved or read. Server-side actions merge into it only the fields they changed.
  const baseline = useRef<LineDraft | null>(draft)
  const [touched, setTouched] = useState<ReadonlySet<LineField>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [listenerDirty, setListenerDirty] = useState(false)
  const [nameTouched, setNameTouched] = useState(!isNew)
  const [sourceGuard, setSourceGuard] = useState<NodeSourceGuard | null>(null)
  const [sourceBusy, setSourceBusy] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [suggesting, setSuggesting] = useState(false)

  const landing = servers.find(s => s.id === node.panel_id)
  const entryServers = useMemo(() => servers.filter(s => s.panel_type === 'psp' && s.id !== node.panel_id), [servers, node.panel_id])
  const patch = useCallback((p: Partial<LineDraft>) => setDraft(d => (d ? { ...d, ...p } : d)), [])

  // A new line is "changed" once anything differs from the blank line of its kind.
  const dirty = (draft !== null && (isNew ? draftKey(draft) !== draftKey(blankDraft(kind ?? 'external', node)) : draftKey(draft) !== (baseline.current ? draftKey(baseline.current) : ''))) || listenerDirty
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  // What the panel says of the line's source addresses (ADR 0044). The editor works without it.
  useEffect(() => {
    const c = new AbortController()
    getNodeSourceGuard(nodeId, { signal: c.signal }).then(setSourceGuard).catch(() => {})
    return () => c.abort()
  }, [nodeId])

  // A new line has the kind that was chosen (a forward has no entry server until one is picked, which would read as external); a saved one has the kind its entry and mode say, which the entry field can change.
  const cur = isNew ? kind : draft ? kindOf(draft) : kind
  const problems = draft && cur ? validateDraft(draft, cur) : {}
  const error = (f: LineField) => (attempted || touched.has(f) ? problems[f] : undefined)
  const blur = (f: LineField) => setTouched(s => (s.has(f) ? s : new Set(s).add(f)))
  const fields: FieldsProps | null = draft && { draft, patch, error, blur }

  // Which groups would receive the line with the labels as typed (ADR 0041): this line among the node's saved ones.
  const visibilityDraft = useMemo<LineVisibilityDraft | null>(() => {
    if (!draft) return null
    const labels = (l: { region?: string; tags?: string[] }) => ({ region: (l.region ?? '').trim(), tags: l.tags ?? [] })
    const lines = (node.relays ?? []).map(r => ({ id: r.id, ...labels(r.id === draft.id ? draft : r) }))
    if (!draft.id) lines.push({ id: undefined, ...labels(draft) })
    return { server_address: node.server_address, region: node.region, tags: node.tags ?? [], relays: lines }
  }, [draft?.id, draft?.region, draft?.tags, node.relays, node.server_address, node.region, node.tags]) // eslint-disable-line react-hooks/exhaustive-deps
  const visibility = useLineVisibility(nodeId, visibilityDraft)
  const visIndex = draft?.id ? (node.relays ?? []).findIndex(r => r.id === draft.id) : (node.relays ?? []).length

  function pickKind(next: LineKind) {
    setChoosing(false)
    setKind(next)
    // What was typed for the line stays when its kind is changed before the first save.
    setDraft(prev => {
      const fresh = blankDraft(next, node)
      return prev ? { ...fresh, name: prev.name, only_line_name: prev.only_line_name, enabled: prev.enabled, region: prev.region, tags: prev.tags, _factor_text: prev._factor_text, _factor_touched: prev._factor_touched, traffic_factor_milli: prev.traffic_factor_milli } : fresh
    })
  }

  function pickEntry(id: number) {
    if (!draft) return
    const server = servers.find(s => s.id === id)
    if (id === 0) { patch({ entry_panel_id: 0, mode: undefined }); return }
    // A managed entry forwards unchanged: no SNI or Host override. A new line takes the server's name and address.
    patch({
      entry_panel_id: id, sni: '', host: '',
      ...(isNew && server && !nameTouched ? { name: server.name } : {}),
      ...(server && !draft.address.trim() ? { address: hostOfServer(server) } : {}),
    })
  }

  const suggest = async () => {
    if (!draft || !(draft.entry_panel_id && draft.entry_panel_id > 0)) return
    setSuggesting(true)
    try { patch({ port: await suggestPort(draft.entry_panel_id) }) } catch (err) { pushToast({ message: frontErrorOf(err)?.message || t('nodes.tunnel.failed'), severity: 'error' }) } finally { setSuggesting(false) }
  }

  /** The server settled this line (a mode switch, a listener edit or sync): show it, keep every other edit, keep the caches current. */
  function applyServerLine(saved: Node) {
    apply(saved)
    const fresh = (saved.relays ?? []).find(r => r.id === lineId)
    if (!fresh) return
    const server = { mode: fresh.mode, port: fresh.port, sni: fresh.sni ?? '', host: fresh.host ?? '' }
    setDraft(d => (d ? { ...d, ...server } : d))
    // Only what the server decided goes into the baseline too: the admin's own edits stay edits.
    baseline.current = baseline.current ? { ...baseline.current, ...server } : baseline.current
    setKind(kindOf(fresh))
  }
  const onRelays = (relays: RelayLine[]) => applyServerLine({ ...node, relays })

  async function toggleMode(next: 'forward' | 'tunnel') {
    if (!live?.id) return
    setSwitching(true)
    try {
      const saved = await switchLineMode(nodeId, live.id, next, 0, { present })
      applyServerLine(saved)
      pushToast({ message: t(next === 'tunnel' ? 'nodes.tunnel.switched_tunnel' : 'nodes.tunnel.switched_forward'), severity: 'success' })
    } catch (err) {
      const e = frontErrorOf(err)
      // Declining the plan rethrows its refusal: nothing changed, nothing to report.
      if (!(e && e.status === 409 && e.code === 'confirm_required')) pushToast({ message: e?.message || t('nodes.tunnel.failed'), severity: 'error' })
    } finally { setSwitching(false) }
  }

  async function addSource(ip: string) {
    if (!live?.id) return
    setSourceBusy(true)
    try {
      const saved = await addRefusedSource(nodeId, live.id, ip)
      setDraft(d => (d ? { ...d, source_cidrs: saved, _source_text: undefined } : d))
      baseline.current = baseline.current ? { ...baseline.current, source_cidrs: saved } : baseline.current
      pushToast({ message: t('nodes.source.added', { ip }), severity: 'success' })
      setSourceGuard(await getNodeSourceGuard(nodeId))
    } catch { /* The request layer already surfaced the failure. */ } finally { setSourceBusy(false) }
  }

  async function save() {
    if (!draft) return
    const bad = Object.keys(validateDraft(draft, cur ?? kindOf(draft))) as LineField[]
    if (bad.length > 0) {
      setAttempted(true)
      const first = (['name', 'entry', 'domain', 'address', 'port', 'factor', 'region'] as LineField[]).find(f => bad.includes(f))
      if (first) setTimeout(() => document.getElementById(ID[first])?.focus(), 0)
      return
    }
    setPending(true)
    try {
      const { saved, addedId } = await saveLineChange(nodeId, { kind: 'save', draft }, { present })
      apply(saved)
      const mine = (saved.relays ?? []).find(r => r.id === (draft.id ?? addedId))
      const warned = mine?.mode === 'cdn' && mine.cdn_issues?.some(i => i.warn)
      pushToast(warned
        ? { message: t('nodes.cdn.saved_warnings', { lines: mine?.name || mine?.domain || '' }), severity: 'warning' }
        : { message: isNew ? t('nodes.line.toast.added', { name: draft.name.trim() || draft.domain?.trim() || draft.address.trim() }) : t('nodes.line.toast.saved'), severity: 'success' })
      baseline.current = draft
      setListenerDirty(false)
      onSaved({ addedId, isNew })
    } catch { /* The request layer already surfaced the failure; a declined plan changed nothing. */ } finally { setPending(false) }
  }

  const discard = () => {
    setTouched(new Set()); setAttempted(false); setListenerDirty(false)
    if (isNew) { setDraft(null); setKind(null); setNameTouched(false) } else setDraft(live ? draftOf(live) : null)
  }

  const showCards = isNew && (kind === null || choosing)
  const labelPreview = draft && (draft.only_line_name ? draft.name.trim() : [node.display_name, draft.name.trim()].filter(Boolean).join(' · '))
  const savedEntry = live?.entry_panel_id ?? 0
  const canSwitchMode = !!live?.id && savedEntry > 0 && (draft?.entry_panel_id ?? 0) === savedEntry && (cur === 'forward' || cur === 'tunnel')
  return (
    <FormValidityProvider>
      <form className="flex flex-col gap-5" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
        {draft && fields && <LineDiagram draft={draft} node={node} servers={servers} />}

        {showCards ? (
          <section className="flex flex-col gap-2">
            <h3 id={typeLabel} className="text-sm font-medium">{t('nodes.line.choose_type')}</h3>
            <TypeCards value={kind} onChange={pickKind} entryServers={entryServers.length} landingIsNative={landing?.panel_type === 'psp'} labelledBy={typeLabel} />
          </section>
        ) : (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">{t('nodes.line.type_summary', { type: t(`nodes.type.${cur ?? 'external'}`) })}</span>
            {isNew && <Button type="button" variant="ghost" size="xs" onClick={() => setChoosing(true)}>{t('nodes.line.change_type')}</Button>}
            {!isNew && cur === 'cdn' && <span className="text-xs text-muted-foreground">{t('nodes.cdn.entry_hint')}</span>}
          </div>
        )}

        {draft && fields && !showCards && (
          <>
            <Field orientation="horizontal" label={t('nodes.relay.enabled')}>
              <Switch checked={draft.enabled} onCheckedChange={enabled => patch({ enabled })} />
            </Field>
            <Field label={t('nodes.relay.name')} hint={t('nodes.relay.name_factor_hint', { placeholder: '{{ factor }}' })} error={error('name') && t(`nodes.${error('name')}`)}>
              <Input id={ID.name} value={draft.name} placeholder={t('nodes.relay.name_ph')} onChange={e => { setNameTouched(true); patch({ name: e.target.value }) }} onBlur={() => blur('name')} />
            </Field>

            {(cur === 'forward' || cur === 'tunnel') && (
              <>
                <Field label={t('nodes.line.entry_server')} required hint={t('nodes.relay.entry_managed_hint')} error={error('entry') && t(`nodes.${error('entry')}`)}>
                  <ServerSelect id={ID.entry} servers={entryServers} value={draft.entry_panel_id ?? 0}
                    placeholder={t(isNew ? 'nodes.line.entry_pick' : 'nodes.line.entry_outside')} onChange={pickEntry} onBlur={() => blur('entry')} />
                </Field>
                {/* A saved line can be pointed at an entry configured outside the panel. The picker lists servers only, so that choice is its own button. */}
                {!isNew && (draft.entry_panel_id ?? 0) > 0 && (
                  <div className="-mt-3"><Button type="button" variant="outline" size="sm" onClick={() => pickEntry(0)}>{t('nodes.line.entry_outside')}</Button></div>
                )}
                {canSwitchMode && (
                  <div className="flex flex-wrap items-center gap-2" data-testid="tunnel-line">
                    <Button type="button" variant="outline" size="sm" disabled={switching} onClick={() => void toggleMode(cur === 'forward' ? 'tunnel' : 'forward')}>
                      {t(cur === 'forward' ? 'nodes.line.action.to_tunnel' : 'nodes.line.action.to_forward')}
                    </Button>
                    <InfoTip text={t('nodes.tunnel.mode_tip')} />
                  </div>
                )}
                {!live?.id && cur === 'tunnel' && <p data-testid="tunnel-new-hint" className="text-xs text-muted-foreground">{t('nodes.tunnel.new_hint')}</p>}
                <Field label={t('nodes.line.entry_address')} required error={error('address') && t(`nodes.${error('address')}`)}>
                  <Input id={ID.address} value={draft.address} placeholder="relay.example.com" onChange={e => patch({ address: e.target.value })} onBlur={() => blur('address')} />
                </Field>
                {cur === 'forward' && (
                  <Field label={t('nodes.line.entry_port')} required hint={t('nodes.relay.port_managed_hint')} error={error('port') && t(`nodes.${error('port')}`)}>
                    {p => (
                      <div className="flex items-center gap-2">
                        <PortInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} className="max-w-40" value={draft.port} onChange={port => patch({ port })} onBlur={() => blur('port')} />
                        <Button type="button" variant="outline" size="sm" disabled={suggesting || !(draft.entry_panel_id && draft.entry_panel_id > 0)} onClick={() => void suggest()}><IconRandom />{t('nodes.line.action.suggest_port')}</Button>
                      </div>
                    )}
                  </Field>
                )}
                {live?.id && cur === 'forward' && savedEntry === draft.entry_panel_id && (
                  <>
                    <div><StatusDot tone={live.managed_ready ? 'success' : 'neutral'} label={t(live.managed_ready ? 'nodes.relay.managed_ready' : 'nodes.relay.managed_pending')} className="text-xs" /></div>
                    <ProbeChip probe={live.probe} withheld={live.probe_withheld} testId="relay-probe" />
                  </>
                )}
                {live?.id && cur === 'tunnel' && savedEntry === draft.entry_panel_id && live.mode === 'tunnel' && (
                  <ListenerCard nodeId={nodeId} line={live} present={present} onRelays={onRelays} onDirty={setListenerDirty} />
                )}
              </>
            )}

            {cur === 'external' && (
              <>
                <Field label={t('nodes.line.entry_server')} hint={t('nodes.relay.entry_external_hint')}>
                  <ServerSelect id={ID.entry} servers={isNew ? [] : entryServers} value={0} placeholder={t('nodes.line.entry_outside')} onChange={pickEntry} disabled={isNew} />
                </Field>
                <Field label={t('nodes.line.entry_address')} required error={error('address') && t(`nodes.${error('address')}`)}>
                  <Input id={ID.address} value={draft.address} placeholder="relay.example.com" onChange={e => patch({ address: e.target.value })} onBlur={() => blur('address')} />
                </Field>
                <Field label={t('nodes.line.entry_port')} hint={t('nodes.line.entry_port_same')} error={error('port') && t(`nodes.${error('port')}`)}>
                  <PortInput id={ID.port} className="max-w-40" value={draft.port} onChange={port => patch({ port })} onBlur={() => blur('port')} />
                </Field>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field label={t('nodes.relay.sni')} hint={t('nodes.relay.cdn_optional')}><Input value={draft.sni ?? ''} onChange={e => patch({ sni: e.target.value })} /></Field>
                  <Field label={t('nodes.relay.host')} hint={t('nodes.relay.cdn_optional')}><Input value={draft.host ?? ''} onChange={e => patch({ host: e.target.value })} /></Field>
                </div>
              </>
            )}

            {cur === 'cdn' && <CdnFields {...fields} issues={live?.cdn_issues ?? []} />}

            {cur !== 'tunnel' && (
              <SourceAddresses line={draft} text={draft._source_text ?? (draft.source_cidrs ?? []).join('\n')} onText={text => patch({ _source_text: text })}
                guard={live?.id ? sourceGuard?.lines.find(x => x.line_id === live.id) : undefined} canGuard={sourceGuard?.can_guard} cannotGuardReason={sourceGuard?.cannot_guard_reason}
                cloudflare={sourceGuard?.cloudflare} busy={sourceBusy} onAdd={ip => void addSource(ip)} />
            )}

            <FormSection title={t('nodes.line.section_display')}>
              <div className="flex flex-col gap-4">
                <LabelsFields {...fields} tagOptions={tagOptions} visibility={lineVisibilityHint(visibility, 'relays', visIndex)} />
                <Field orientation="horizontal" label={t('nodes.relay.only_line_name')} hint={labelPreview ? t('nodes.line.label_preview', { label: labelPreview }) : undefined}>
                  <Switch checked={!!draft.only_line_name} onCheckedChange={only_line_name => patch({ only_line_name })} />
                </Field>
              </div>
            </FormSection>
            <FormSection title={t('nodes.line.section_billing')}>
              <Field label={t('nodes.field.traffic_factor')} hint={t('nodes.hint.traffic_factor')} error={error('factor') && t(`nodes.${error('factor')}`)}>
                <Input id={ID.factor} inputMode="decimal" className="max-w-40 tabular-nums" value={draft._factor_text ?? milliToFactorText(draft.traffic_factor_milli)}
                  onChange={e => patch({ _factor_text: e.target.value, _factor_touched: true, ...(factorTextToMilli(e.target.value) !== null ? { traffic_factor_milli: factorTextToMilli(e.target.value)! } : {}) })} onBlur={() => blur('factor')} />
              </Field>
            </FormSection>
          </>
        )}

        {draft && !showCards && (
          <SaveBar dirty={dirty || isNew} pending={pending} saveLabel={t('nodes.line.action.save')} onSave={() => void save()} onDiscard={discard} />
        )}
      </form>
      {dialog}
    </FormValidityProvider>
  )
}
