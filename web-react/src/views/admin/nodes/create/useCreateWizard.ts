import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createInbound, generateRealityKeypair, previewNodeSetup, type NodeSetupPlan } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { Node } from '@/api/types'
import { randomRealitySite } from '@/utils/xrayReality'
import { draftIssues, inputOf, type DraftRoute } from '../exit/routes/routeDraft'
import { canUseJson, snapshotRaw } from '../inbound/InboundForm'
import { panelTypeOf } from '../inbound/formRules'
import { EMPTY_INBOUND, SS2022_METHODS, newRelayKey, randomBase64, serverHost, splitList, type InboundFormState } from '../inbound/inboundModel'
import { validateInboundForm } from '../inbound/inboundSettings'
import { setupEntriesValid, type SetupRelay } from './setupModel'
import { buildSetupRequest, setupOptions } from './setupRequest'

export const STEPS = ['inbound', 'lines', 'exit', 'review'] as const
export type Step = 0 | 1 | 2 | 3

const RECENT_KEY = 'psp.nodes.recentEntries'
// Per-viewer convenience: which entry servers were used last, so the picker offers them first. Not worth failing for.
function readRecent(): number[] {
  try { const v: unknown = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]'); return Array.isArray(v) ? v.filter(Number.isInteger) as number[] : [] } catch { return [] }
}
function writeRecent(ids: number[]) { try { localStorage.setItem(RECENT_KEY, JSON.stringify(ids.slice(0, 20))) } catch { /* remembered for this visit only */ } }

function initialForm(landing: Server): InboundFormState {
  const site = randomRealitySite()
  return { ...EMPTY_INBOUND, panel_id: landing.id, server_address: serverHost(landing), display_name: landing.name, region: landing.region ?? '', reality_dest: `${site}:443`, reality_server_names_text: site }
}

/** The key pair is generated on arrival and whatever else is typed is the person's: only what is not generated counts as an edit. */
export const wizardKey = (f: InboundFormState) => JSON.stringify({ ...f, private_key: '', public_key: '', short_ids_text: '' })

/**
 * The create wizard's state and its moves (design 4.8): four steps over one draft — the inbound with the node's
 * basics, the lines that reach it, the default exit, and the plan the server drew from them. A step's problems stop
 * `next` and are said at the top of the step. The review is a real round trip: the server plans the ports, the
 * person may edit the loopback and link ports it chose, and creating sends the plan's token so the server can
 * refuse (409) when the world changed since. A refused plan is dropped and the person reviews again.
 */
export function useCreateWizard({ servers, writable, t, onCreated }: {
  servers: readonly Server[]
  writable: readonly Server[]
  t: (key: string, opts?: Record<string, unknown>) => string
  onCreated: (node: Node | null) => void
}) {
  const [form, setForm] = useState<InboundFormState>(() => initialForm(writable[0]))
  const initial = useRef(wizardKey(form))
  const [json, setJson] = useState(false)
  const [step, setStep] = useState<Step>(0)
  const [busy, setBusy] = useState(false)
  const [entriesBusy, setEntriesBusy] = useState(false)
  const [genBusy, setGenBusy] = useState(false)
  const [attempted, setAttempted] = useState(false)
  const [error, setError] = useState('')
  const [direct, setDirect] = useState(true)
  const [entries, setEntries] = useState<SetupRelay[]>([])
  const [egress, setEgress] = useState(0)
  const [routes, setRoutes] = useState<DraftRoute[]>([])
  const [plan, setPlan] = useState<NodeSetupPlan | null>(null)
  const [loopbacks, setLoopbacks] = useState<Record<number, number>>({})
  const [linkPorts, setLinkPorts] = useState<Record<string, number>>({})
  const [recent, setRecent] = useState<number[]>(readRecent)
  const keyIntent = useRef(0)
  const changed = Object.keys(loopbacks).length > 0 || Object.keys(linkPorts).length > 0

  const landing = servers.find(s => s.id === form.panel_id)
  const panelType = panelTypeOf(landing)
  const native = panelType === 'psp'
  const dirty = wizardKey(form) !== initial.current || step > 0 || entries.length > 0 || routes.length > 0 || !direct || egress > 0

  const genKeys = useCallback(async () => {
    const mine = ++keyIntent.current
    setGenBusy(true)
    try {
      const kp = await generateRealityKeypair()
      if (mine !== keyIntent.current) return
      setForm(f => ({ ...f, private_key: kp.private_key, public_key: kp.public_key, short_ids_text: kp.short_id }))
    } catch { /* The request layer already surfaced the failure. */ } finally { if (mine === keyIntent.current) setGenBusy(false) }
  }, [])
  // The wizard opens with a fresh key pair.
  useEffect(() => { void genKeys(); return () => { keyIntent.current += 1 } }, [genKeys])

  const randomizeSite = () => setForm(prev => {
    const site = randomRealitySite(splitList(prev.reality_server_names_text))
    return { ...prev, reality_dest: `${site}:443`, reality_server_names_text: site }
  })
  const genSSPassword = () => {
    const method = SS2022_METHODS.find(m => m.value === form.ss_method)
    setForm(f => ({ ...f, ss_password: randomBase64(method?.bytes ?? 32) }))
  }
  const setJsonMode = (next: boolean) => { if (next) setForm(f => snapshotRaw(f)); setJson(next) }
  const jsonAllowed = canUseJson(landing)

  /** Why the inbound step cannot be left yet, or null. */
  function inboundProblem(): string | null {
    if (json) {
      if (!form.display_name || !form.server_address || !form.region) return t('nodes.create_dialog.validate_required')
      for (const [label, raw] of [['settings', form.raw_settings], ['streamSettings', form.raw_stream_settings], ['sniffing', form.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') } catch { return t('nodes.create_dialog.advanced_invalid_json', { field: label }) }
      }
      return null
    }
    return validateInboundForm(form, panelType, t)
  }

  const options = useMemo(() => setupOptions({ entries, plan, loopbacks, linkPorts, routes: inputOf(routes) }), [entries, plan, loopbacks, linkPorts, routes])
  const request = (opts = options) => buildSetupRequest({ form, panelType, json, entries, direct, egress, options: opts })

  function goBack() {
    setStep(s => (s - 1) as Step); setPlan(null); setLoopbacks({}); setLinkPorts({}); setError('')
  }

  async function next() {
    if (busy || genBusy || entriesBusy) return
    setError(''); setAttempted(true)
    if (step === 0) {
      const problem = inboundProblem()
      if (problem) { setError(problem); return }
      setAttempted(false)
      // The landing may have changed: an entry cannot enter through its own landing, and only a Passwall Node landing has an exit or routes.
      setEntries(es => es.filter(e => e.line.entry_panel_id !== form.panel_id))
      if (egress === form.panel_id || !native) setEgress(0)
      if (!native) setRoutes([])
      setStep(1)
      return
    }
    if (step === 1) {
      if (!direct && entries.length === 0) { setError(t('nodes.setup.needs_entry')); return }
      if (!native && entries.some(e => e.line.mode === 'tunnel')) { setError(t('nodes.setup.tunnel_landing')); return }
      if (!setupEntriesValid(entries)) return
      setAttempted(false)
      setStep(2)
      return
    }
    if (draftIssues(routes).length > 0) { setError(t('nodes.setup.invalid_routes')); return }
    setBusy(true)
    try {
      if (step === 2 || !plan || changed) {
        const next = await previewNodeSetup(request())
        setPlan(next); setLoopbacks({}); setLinkPorts({}); setStep(3)
        return
      }
      const created = await createInbound(request({ ...options, confirm: plan.token }))
      const used = entries.flatMap(e => (e.line.entry_panel_id ? [e.line.entry_panel_id] : []))
      const ids = [...new Set([...used, ...recent])]
      setRecent(ids); writeRecent(ids)
      onCreated('queued' in created ? null : created)
    } catch (err) {
      const response = (err as { response?: { status?: number; data?: { error?: string } } }).response
      setError(response?.data?.error ?? t('nodes.setup.request_failed'))
      // The world changed since the plan was drawn: it cannot be confirmed any more.
      if (response?.status === 409 && step === 3) { setPlan(null); setLoopbacks({}); setLinkPorts({}) }
    } finally { setBusy(false) }
  }

  return {
    form, setForm, json, setJsonMode, jsonAllowed, step, busy: busy || genBusy || entriesBusy, creating: busy, attempted, error, setError,
    direct, setDirect, entries, setEntries, setEntriesBusy, egress, setEgress, routes, setRoutes, plan, loopbacks, setLoopbacks, linkPorts, setLinkPorts, changed,
    recent, landing, panelType, native, dirty, genKeys, genBusy, randomizeSite, genSSPassword, next, goBack,
    /** The review's button: "Review ports" until a plan exists and is unedited, then "Create node". */
    finalStep: step === 3 && !!plan && !changed,
    newKey: newRelayKey,
  }
}
