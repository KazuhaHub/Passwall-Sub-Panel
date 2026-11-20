import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router'
import { frontErrorOf, suggestCamouflage, suggestPort } from '@/api/fronts'
import { deriveRealityPublicKey, generateRealityKeypair, previewTunnelSync, syncTunnelFromLanding, updateTunnelListener, type TunnelListenerInput } from '@/api/nodes'
import type { RelayLine, TunnelListener } from '@/api/types'
import { StatusDot } from '@/components/common/StatusDot'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconRandom } from '@/lib/icons'
import type { TunnelPlanPresenter } from '@/utils/tunnelConfirm'
import { useNodesT } from '../shared/useNodesT'
import { PortInput } from './fieldKit'
import { ProbeChip } from './ProbeChip'

/** The administrator declined a confirmation (a tunnel one or a shared port one): the refusal is rethrown and nothing changed. */
const declined = (err: unknown) => {
  const e = frontErrorOf(err)
  return !!e && e.status === 409 && e.code === 'confirm_required'
}

interface Form { port: string; server_name: string; target: string; short_id: string; private_key: string; public_key: string; fingerprint: string }

const formOf = (tn?: TunnelListener): Form => ({
  port: tn?.port ? String(tn.port) : '',
  server_name: tn?.server_names?.[0] ?? '',
  target: tn?.target ?? '',
  short_id: tn?.short_ids?.[0] ?? '',
  private_key: tn?.private_key ?? '',
  public_key: tn?.public_key ?? '',
  fingerprint: tn?.fingerprint ?? '',
})

/**
 * The entry listener of a saved tunnel line (the old TunnelLineEditor's card): what runs on the entry server, its
 * REALITY settings, and its own Save, which writes only the listener (`PUT …/tunnel`), never the line. Random
 * only fills the fields in; nothing is applied until Save. Editing the private key fills in its public key, so the
 * pair that is sent always matches. "Copy from landing" shows the diff first and writes only once it is confirmed.
 *
 * `onRelays` receives the lines as the server settled them; `onDirty` tells the editor there are unsaved listener
 * edits, so closing the sheet asks.
 */
export function ListenerCard({ nodeId, line, present, onRelays, onDirty }: {
  nodeId: number
  line: RelayLine
  present: TunnelPlanPresenter
  onRelays: (relays: RelayLine[]) => void
  onDirty: (dirty: boolean) => void
}) {
  const t = useNodesT()
  const tn = line.tunnel
  const lineId = line.id!
  const entry = line.entry_panel_id ?? 0
  const [form, setForm] = useState<Form>(() => formOf(tn))
  const [busy, setBusy] = useState('')
  const [keyError, setKeyError] = useState('')
  // The private key whose public key the form already holds.
  const derivedFor = useRef(tn?.private_key ?? '')
  useEffect(() => {
    setForm(formOf(tn))
    derivedFor.current = tn?.private_key ?? ''
    setKeyError('')
  }, [tn])
  useEffect(() => {
    const key = form.private_key.trim()
    if (!key || key === derivedFor.current) { setKeyError(''); return }
    const timer = setTimeout(() => {
      deriveRealityPublicKey(key).then(pub => {
        derivedFor.current = key
        setForm(f => (f.private_key.trim() === key ? { ...f, public_key: pub } : f))
        setKeyError('')
      }).catch(err => setKeyError(frontErrorOf(err)?.message || t('nodes.tunnel.failed')))
    }, 400)
    return () => clearTimeout(timer)
  }, [form.private_key, t])
  const dirty = useMemo(() => JSON.stringify(form) !== JSON.stringify(formOf(tn)), [form, tn])
  useEffect(() => { onDirty(dirty) }, [dirty, onDirty])
  useEffect(() => () => onDirty(false), [onDirty])
  const set = (patch: Partial<Form>) => setForm(f => ({ ...f, ...patch }))
  const reality = !tn || (tn.security ?? 'reality') === 'reality'
  const hasPrivate = tn?.private_key !== undefined || form.private_key !== ''

  const errorText = (err: unknown) => frontErrorOf(err)?.message || t('nodes.tunnel.failed')
  async function run(what: string, job: () => Promise<void>) {
    setBusy(what)
    try { await job() } catch (err) { if (!declined(err)) pushToast({ message: errorText(err), severity: 'error' }) } finally { setBusy('') }
  }

  const randomPort = () => run('port', async () => set({ port: String(await suggestPort(entry)) }))
  const randomSite = () => run('site', async () => {
    const site = await suggestCamouflage(entry)
    set({ server_name: site.host, target: `${site.host}:${site.port || 443}` })
  })
  const randomKeys = () => run('keys', async () => {
    const pair = await generateRealityKeypair()
    derivedFor.current = pair.private_key.trim()
    setKeyError('')
    set({ private_key: pair.private_key, public_key: pair.public_key, short_id: pair.short_id })
  })

  const save = () => run('save', async () => {
    const input: TunnelListenerInput = {
      port: Number(form.port) || 0, server_name: form.server_name.trim(), target: form.target.trim(), short_id: form.short_id.trim(),
      private_key: form.private_key.trim(), public_key: form.public_key.trim(), fingerprint: form.fingerprint.trim(),
    }
    // A private key typed a moment ago has no derived public key yet: derive it now, never send a mismatched pair.
    if (input.private_key && input.private_key !== derivedFor.current) {
      try {
        input.public_key = await deriveRealityPublicKey(input.private_key)
        derivedFor.current = input.private_key
      } catch (err) { setKeyError(errorText(err)); return }
    }
    onRelays(await updateTunnelListener(nodeId, lineId, input))
    pushToast({ message: t('nodes.tunnel.saved'), severity: 'success' })
  })

  // The preview comes first: the person sees what would change, and only then is anything written.
  const sync = () => run('sync', async () => {
    let diff
    try { diff = await previewTunnelSync(nodeId, lineId) } catch (err) { pushToast({ message: errorText(err), severity: 'error' }); return }
    if (diff.length === 0) { pushToast({ message: t('nodes.tunnel.sync_none'), severity: 'info' }); return }
    onRelays(await syncTunnelFromLanding(nodeId, lineId, dirty ? t('nodes.tunnel.confirm.sync_discards') : undefined, { present }))
    pushToast({ message: t('nodes.tunnel.synced'), severity: 'success' })
  })

  const status = (ok: boolean, yes: string, no: string, testId: string) => (
    <div data-testid={testId}><StatusDot tone={ok ? 'success' : 'neutral'} label={ok ? yes : no} className="text-xs" /></div>
  )

  return (
    <section data-testid="tunnel-card" aria-label={t('nodes.line.listener')} className="flex flex-col gap-3 rounded-lg border p-3">
      <h3 className="text-sm font-medium">{t('nodes.line.listener')}</h3>
      {status(!!line.running, t('nodes.tunnel.running'), t('nodes.tunnel.pending'), 'tunnel-running')}
      {!tn ? (
        <p data-testid="tunnel-missing" className="text-xs text-destructive">{t('nodes.tunnel.missing')}</p>
      ) : (
        <>
          {status(tn.applied, t('nodes.tunnel.applied'), t('nodes.tunnel.not_applied', { state: tn.config_sync_state || '-' }), 'tunnel-applied')}
          {status(tn.egress_ready, t('nodes.tunnel.link_ready'), t('nodes.tunnel.link_pending'), 'tunnel-link')}
          <ProbeChip probe={tn.egress_probe} withheld={line.probe_withheld || tn.egress_withheld} testId="tunnel-probe" />
          {tn.routes_withheld && <p className="text-xs text-destructive">{t('nodes.tunnel.routes_withheld', { why: tn.routes_withheld })}</p>}
          <div className="flex items-end gap-2">
            <Field label={t('nodes.tunnel.port')} className="w-40"><PortInput data-testid="tunnel-port" value={Number(form.port) || 0} onChange={p => set({ port: p ? String(p) : '' })} /></Field>
            <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void randomPort()}><IconRandom />{t('nodes.tunnel.random')}</Button>
          </div>
          {reality && (
            <>
              <div className="flex flex-wrap items-end gap-2">
                <Field label={t('nodes.tunnel.server_name')} className="min-w-40 flex-1"><Input data-testid="tunnel-sni" value={form.server_name} onChange={e => set({ server_name: e.target.value })} /></Field>
                <Field label={t('nodes.tunnel.target')} className="min-w-40 flex-1"><Input data-testid="tunnel-target" value={form.target} onChange={e => set({ target: e.target.value })} /></Field>
                <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void randomSite()}><IconRandom />{t('nodes.tunnel.random')}</Button>
              </div>
              <Field label={t('nodes.tunnel.short_id')}><Input data-testid="tunnel-short-id" value={form.short_id} onChange={e => set({ short_id: e.target.value })} /></Field>
              <Field label={t('nodes.tunnel.public_key')}><Input data-testid="tunnel-public-key" className="font-mono" value={form.public_key} onChange={e => set({ public_key: e.target.value })} /></Field>
              {hasPrivate && (
                <Field label={t('nodes.tunnel.private_key')} error={keyError || undefined} errorBlocksSave={false}>
                  <SecretField data-testid="tunnel-private-key" value={form.private_key} onChange={v => set({ private_key: v })} />
                </Field>
              )}
              <div className="flex items-end gap-2">
                <Field label={t('nodes.tunnel.fingerprint')} className="flex-1"><Input data-testid="tunnel-fingerprint" value={form.fingerprint} onChange={e => set({ fingerprint: e.target.value })} /></Field>
                <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void randomKeys()}><IconRandom />{t('nodes.tunnel.random_keys')}</Button>
              </div>
            </>
          )}
          <p className="text-xs text-muted-foreground">{t('nodes.tunnel.prefill_hint')}</p>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={!!busy || !dirty || !!keyError} onClick={() => void save()}>{t('nodes.tunnel.save')}</Button>
            <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void sync()}>{t('nodes.tunnel.sync')}</Button>
            <Button variant="outline" size="sm" render={<Link to={`/admin/nodes/${tn.node_id}`} />}>{t('nodes.line.listener_settings')}</Button>
          </div>
          <p data-testid="tunnel-routes-note" className="text-xs text-muted-foreground">
            {t('nodes.tunnel.routes_note', { count: tn.routes })}{' '}
            <Link to={`/admin/nodes/${tn.node_id}`} className="underline underline-offset-2">{t('nodes.tunnel.routes_open')}</Link>
          </p>
        </>
      )}
    </section>
  )
}
