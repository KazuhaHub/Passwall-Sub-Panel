import { type Dispatch, type SetStateAction, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { generateRealityKeypair } from '@/api/nodes'
import type { Node } from '@/api/types'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { xrayRequiresMLKEMFirst } from '@/utils/xrayReality'
import { useNodeDetail } from '../lines/useNodeDetail'
import { reasonOf } from '../shared/reasonOf'
import { useNodesT } from '../shared/useNodesT'
import { InboundForm, canUseJson, snapshotRaw } from './InboundForm'
import { SourceRulesCard } from './SourceRulesCard'
import { panelTypeOf, xrayVersionOf } from './formRules'
import { EMPTY_INBOUND, SS2022_METHODS, type InboundFormState, randomBase64 } from './inboundModel'
import { validateInboundForm } from './inboundSettings'
import { readInbound } from './readInbound'
import { saveInbound, type PartialFailure } from './saveInbound'

const RAW = { raw_settings: '', raw_stream_settings: '', raw_sniffing: '' }
// The node's own fields travel in the form state but belong to the General tab: a rename there is not an edit here.
const NODE_OWN = { display_name: '', server_address: '', region: '', tags_text: '' }
/** What counts as an edit: the structured fields, or in JSON mode the three documents. The raw ones are only a snapshot while the form mode is on. */
const keyOf = (f: InboundFormState, json: boolean) => JSON.stringify({ ...f, ...NODE_OWN, ...(json ? {} : RAW) })

/**
 * Inbound (design 4.5): how the listener is configured, as one form with its own save bar, plus the node's
 * source-country rules, which are another resource with their own Save.
 *
 * The form is read once and then belongs to the person; later reads (a refetch, another tab's save) do not
 * replace it. After this tab's own save it is read again, so what is shown is what the server now holds.
 * Loading failures, a malformed snapshot and an inbound this form cannot represent show their reason and no
 * fields: editable defaults over a missing configuration would, on save, overwrite the real inbound.
 */
export function InboundTab({ node, onDirtyChange }: { node: Node; onDirtyChange: (dirty: boolean) => void }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const detail = useNodeDetail(node.id)
  const servers = useAllServers(scope)
  const canAccess = useCan('access.view')
  const server = (servers.data ?? []).find(s => s.id === node.panel_id)
  const panelType = panelTypeOf(server)
  const locked = xrayRequiresMLKEMFirst(xrayVersionOf(server))
  // Xray 26.9.8+ forces the REALITY fingerprint (the form does it on mount): the starting point already has it, so opening the tab is not an edit.
  const settle = useCallback((f: InboundFormState) => (locked && f.reality_fingerprint !== 'chrome' ? { ...f, reality_fingerprint: 'chrome' } : f), [locked])

  const read = useMemo(() => (detail.data ? readInbound(detail.data, node.id, t) : null), [detail.data, node.id, t])
  const [form, setForm] = useState<InboundFormState | null>(null)
  const baseline = useRef<InboundFormState | null>(null)
  const [json, setJson] = useState(false)
  // The form needs a setter that cannot make it appear: until the first read there is no form to change.
  const patchForm = useCallback<Dispatch<SetStateAction<InboundFormState>>>(u => setForm(prev => (prev ? (typeof u === 'function' ? u(prev) : u) : prev)), [])
  useEffect(() => {
    if (form === null && read?.kind === 'ready' && servers.data) { const f = settle(read.form); baseline.current = f; setForm(f) }
  }, [form, read, servers.data, settle])

  const [pending, setPending] = useState(false)
  const [problem, setProblem] = useState('')
  // After a save where the inbound went through but flow or the certificate source did not: the form as it was then and what is left to send.
  const partial = useRef<{ key: string; parts: Set<PartialFailure['part']> } | null>(null)
  const [genBusy, setGenBusy] = useState(false)
  const intent = useRef(0)
  useEffect(() => () => { intent.current += 1 }, [])
  const [sourceDirty, setSourceDirty] = useState(false)

  const dirty = form !== null && baseline.current !== null && (json ? keyOf(form, true) !== keyOf(snapshotRaw(baseline.current), true) : keyOf(form, false) !== keyOf(baseline.current, false))
  useEffect(() => { onDirtyChange(dirty || sourceDirty) }, [dirty, sourceDirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  const setJsonMode = (next: boolean) => {
    if (next) setForm(f => (f ? snapshotRaw(f) : f))
    setJson(next)
  }

  async function genKeys() {
    const mine = intent.current
    setGenBusy(true)
    try {
      const kp = await generateRealityKeypair()
      if (intent.current !== mine) return
      setForm(f => (f ? { ...f, private_key: kp.private_key, public_key: kp.public_key, short_ids_text: kp.short_id } : f))
    } catch { /* The request layer already surfaced the failure. */ } finally { if (intent.current === mine) setGenBusy(false) }
  }
  const genSSPassword = () => {
    const method = SS2022_METHODS.find(m => m.value === form?.ss_method)
    setForm(f => (f ? { ...f, ss_password: randomBase64(method?.bytes ?? 32) } : f))
  }

  async function save() {
    if (!form || pending) return
    setProblem('')
    if (json) {
      for (const [label, raw] of [['settings', form.raw_settings], ['streamSettings', form.raw_stream_settings], ['sniffing', form.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') } catch { setProblem(t('nodes.create_dialog.advanced_invalid_json', { field: label })); return }
      }
    } else {
      const err = validateInboundForm(form, panelType, t as (k: string) => string)
      if (err) { setProblem(err); return }
    }
    setPending(true)
    try {
      // The inbound's name is the node's, which the General tab may have changed since this form was read.
      const current = { ...form, display_name: node.display_name }
      // Same form as when the inbound last went through: only what failed is sent again; any further edit sends the inbound too.
      const only = partial.current && partial.current.key === keyOf(form, json) ? partial.current.parts : undefined
      const failed = await saveInbound(node, current, panelType, json, only)
      partial.current = null
      // What the server holds now: the form, the baseline and the node (its flow and certificate source) all follow it.
      const fresh = await detail.refetch()
      const again = fresh.data ? readInbound(fresh.data, node.id, t) : null
      if (again?.kind === 'ready') {
        const f = settle(json ? snapshotRaw(again.form) : again.form)
        baseline.current = f
        // What did not go through stays as the person set it, so it is still an edit to retry, not silently reverted.
        const kept: Partial<InboundFormState> = {}
        if (failed.some(p => p.part === 'flow')) kept.vless_flow = form.vless_flow
        if (failed.some(p => p.part === 'cert')) { kept.tls_cert_mode = form.tls_cert_mode; kept.cert_id = form.cert_id }
        const next = { ...f, ...kept }
        setForm(next)
        if (failed.length > 0) partial.current = { key: keyOf(next, json), parts: new Set(failed.map(p => p.part)) }
      }
      if (failed.length === 0) { pushToast({ message: t('nodes.edit_inbound_dialog.saved'), severity: 'success' }); return }
      setProblem(failed.map(p => t(`nodes.inbound.partial_${p.part}`, { reason: reasonOf(p.error) })).join(' '))
    } catch { /* The request layer already surfaced the failure; nothing of the inbound was saved. */ } finally { setPending(false) }
  }

  const supports = (c: string) => !!server?.capabilities?.includes(c as never)
  if (servers.isPending || (detail.isPending && !detail.data)) return <LoadingState />
  const source = canAccess && server?.panel_type === 'psp' ? <SourceRulesCard nodeId={node.id} onDirtyChange={setSourceDirty} /> : null

  let body
  if (detail.isError && !detail.data) {
    body = <ErrorState title={t('nodes.edit_inbound_dialog.load_failed', { error: reasonOf(detail.error) })} retry={() => void detail.refetch()} />
  } else if (read?.kind === 'error') {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col gap-2">
          <span>{t('nodes.edit_inbound_dialog.load_failed', { error: read.message })}</span>
          <span>{t('nodes.edit_inbound_dialog.load_next')}</span>
          <Button variant="outline" size="sm" className="self-start" disabled={detail.isFetching} onClick={() => void detail.refetch()}>{t('common:actions.retry')}</Button>
        </AlertDescription>
      </Alert>
    )
  } else if (read?.kind === 'unsupported') {
    body = <p className="text-sm text-muted-foreground">{t(read.reason === 'ss_method' ? 'nodes.edit_inbound_dialog.unsupported_ss_method' : 'nodes.edit_inbound_dialog.unsupported')}</p>
  } else if (!supports('inbound.update')) {
    body = <p className="text-sm text-muted-foreground">{t('nodes.inbound.no_update')}</p>
  } else if (form) {
    body = (
      <FormValidityProvider>
        <form className="flex flex-col gap-6" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
          {read?.kind === 'ready' && read.syncNotice && <p className="text-sm text-muted-foreground">{t('nodes.edit_inbound_dialog.sync_notice')}</p>}
          {canUseJson(server) && (
            <SegmentedControl aria-label={t('nodes.inbound.mode')} value={json ? 'json' : 'form'} onChange={v => setJsonMode(v === 'json')}
              options={[{ value: 'form', label: t('nodes.inbound.mode_form') }, { value: 'json', label: t('nodes.inbound.mode_json') }]} />
          )}
          <InboundForm form={form} setForm={patchForm} server={server} protocolReadonly json={json} onGenKeys={genKeys} onGenSSPassword={genSSPassword} genKeysBusy={genBusy} />
          <SaveBar dirty={dirty || problem !== ''} pending={pending} errors={problem ? [{ message: problem, blocking: false }] : []} onSave={() => void save()}
            onDiscard={() => { setProblem(''); setJson(false); setForm(baseline.current ? { ...EMPTY_INBOUND, ...baseline.current } : form) }} />
        </form>
      </FormValidityProvider>
    )
  } else body = <LoadingState />

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      {body}
      {source}
    </div>
  )
}
