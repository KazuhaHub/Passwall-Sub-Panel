import { useEffect, useMemo, useRef, useState } from 'react'
import { Box, Button, CircularProgress, IconButton, InputAdornment, TextField, ToggleButton, ToggleButtonGroup, Typography, useTheme } from '@mui/material'
import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import { useTranslation } from 'react-i18next'
import { frontErrorOf, suggestCamouflage, suggestPort } from '@/api/fronts'
import {
  deriveRealityPublicKey, generateRealityKeypair, previewTunnelSync, switchLineMode, syncTunnelFromLanding, updateTunnelListener,
  type TunnelListenerInput,
} from '@/api/nodes'
import type { Node, RelayLine, TunnelListener } from '@/api/types'
import InfoTip from '@/components/InfoTip'
import { pushSnack } from '@/components/SnackbarHost'
import ProbeQualityChip from './ProbeQualityChip'

type Mode = 'forward' | 'tunnel'

/** The admin declined a confirmation (a tunnel one or a shared port one): the refusal is rethrown and nothing changed. */
const declined = (err: unknown) => {
  const e = frontErrorOf(err)
  return !!e && e.status === 409 && e.code === 'confirm_required'
}

/**
 * Copies what the server decides about one saved line (its mode, status, port
 * and listener) onto the editor's row for it, and leaves every other row, and
 * the admin's unsaved edits on this one, as they are.
 */
export function mergeServerLine<T extends RelayLine>(rows: T[], server: RelayLine[], lineId: string): T[] {
  const fresh = server.find(s => s.id === lineId)
  if (!fresh) return rows
  return rows.map(row => {
    if (row.id !== lineId) return row
    // An unchanged listener keeps its identity, so a card's unsaved edits are not reset.
    const tunnel = JSON.stringify(row.tunnel) === JSON.stringify(fresh.tunnel) ? row.tunnel : fresh.tunnel
    return {
      ...row, mode: fresh.mode, running: fresh.running, managed_ready: fresh.managed_ready, probe: fresh.probe,
      probe_withheld: fresh.probe_withheld, port: fresh.port, sni: fresh.sni, host: fresh.host, tunnel,
    }
  })
}

interface Props {
  /** The landing node that owns the line. */
  nodeId: number
  /** The editor's row for the line. */
  line: RelayLine
  /** The entry server the stored line has (0 for a line that is not saved yet). */
  savedEntry: number
  /** A new row only records the chosen mode; it is created by the node save. */
  onPatch: (patch: Partial<RelayLine>) => void
  /** The saved landing after a mode switch. */
  onSwitched: (node: Node) => void
  /** The saved lines after the listener was edited or synced. */
  onRelays: (relays: RelayLine[]) => void
  /** Opens the listener's own settings. */
  onOpenListener: (listenerNodeId: number) => void
}

/** The line's mode switch and, for a tunnel line, the listener it runs on. */
export default function TunnelLineEditor({ nodeId, line, savedEntry, onPatch, onSwitched, onRelays, onOpenListener }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const [busy, setBusy] = useState(false)
  const entry = line.entry_panel_id ?? 0
  const saved = !!line.id
  const mode: Mode = line.mode === 'tunnel' ? 'tunnel' : 'forward'
  // The mode of a saved line moves only through its own endpoint, on the stored line:
  // an entry that was just changed in the form has no stored mode to switch.
  if (entry <= 0 || (saved && savedEntry !== entry)) return null

  async function choose(next: Mode | null) {
    if (!next || next === mode || busy) return
    if (!saved) {
      onPatch(next === 'tunnel' ? { mode: 'tunnel', port: 0, sni: '', host: '' } : { mode: undefined })
      return
    }
    setBusy(true)
    try {
      const node = await switchLineMode(nodeId, line.id!, next)
      onSwitched(node)
      pushSnack(t(next === 'tunnel' ? 'admin:nodes.tunnel.switched_tunnel' : 'admin:nodes.tunnel.switched_forward'), 'success')
    } catch (err) {
      // Declining the confirmation rethrows its refusal: nothing changed, nothing to report.
      if (!declined(err)) pushSnack(errorText(err), 'error')
    } finally { setBusy(false) }
  }

  const errorText = (err: unknown) => frontErrorOf(err)?.message || t('admin:nodes.tunnel.failed')

  return (
    <Box data-testid="tunnel-line" sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Typography sx={{ fontSize: 12 }}>{t('admin:nodes.tunnel.mode')}</Typography>
        <ToggleButtonGroup exclusive size="small" value={mode} disabled={busy} onChange={(_, v: Mode | null) => void choose(v)}
          aria-label={t('admin:nodes.tunnel.mode')}>
          <ToggleButton value="forward">{t('admin:nodes.tunnel.mode_forward')}</ToggleButton>
          <ToggleButton value="tunnel">{t('admin:nodes.tunnel.mode_tunnel')}</ToggleButton>
        </ToggleButtonGroup>
        {busy && <CircularProgress size={16} />}
        <InfoTip text={t('admin:nodes.tunnel.mode_tip')} />
      </Box>
      {!saved && mode === 'tunnel' && (
        <Typography data-testid="tunnel-new-hint" sx={{ fontSize: 12 }}>{t('admin:nodes.tunnel.new_hint')}</Typography>
      )}
      {saved && mode === 'tunnel' && (
        <TunnelCard nodeId={nodeId} line={line} onRelays={onRelays} onOpenListener={onOpenListener} errorText={errorText} />
      )}
    </Box>
  )
}

interface Form {
  port: string
  server_name: string
  target: string
  short_id: string
  private_key: string
  public_key: string
  fingerprint: string
}

const formOf = (tn?: TunnelListener): Form => ({
  port: tn?.port ? String(tn.port) : '',
  server_name: tn?.server_names?.[0] ?? '',
  target: tn?.target ?? '',
  short_id: tn?.short_ids?.[0] ?? '',
  private_key: tn?.private_key ?? '',
  public_key: tn?.public_key ?? '',
  fingerprint: tn?.fingerprint ?? '',
})

function TunnelCard({ nodeId, line, onRelays, onOpenListener, errorText }: {
  nodeId: number
  line: RelayLine
  onRelays: (relays: RelayLine[]) => void
  onOpenListener: (listenerNodeId: number) => void
  errorText: (err: unknown) => string
}) {
  const { t } = useTranslation(['admin', 'common'])
  const md = useTheme().palette.md
  const tn = line.tunnel
  const lineId = line.id!
  const entry = line.entry_panel_id ?? 0
  const [form, setForm] = useState<Form>(() => formOf(tn))
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState('')
  const [keyError, setKeyError] = useState('')
  // The private key whose public key the form already holds.
  const derivedFor = useRef(tn?.private_key ?? '')
  useEffect(() => {
    setForm(formOf(tn))
    derivedFor.current = tn?.private_key ?? ''
    setKeyError('')
  }, [tn])
  // Editing the private key fills in its public key, so the pair sent on Save always matches.
  useEffect(() => {
    const key = form.private_key.trim()
    if (!key || key === derivedFor.current) { setKeyError(''); return }
    const timer = setTimeout(() => {
      deriveRealityPublicKey(key).then(pub => {
        derivedFor.current = key
        setForm(f => (f.private_key.trim() === key ? { ...f, public_key: pub } : f))
        setKeyError('')
      }).catch(err => setKeyError(frontErrorOf(err)?.message || t('admin:nodes.tunnel.failed')))
    }, 400)
    return () => clearTimeout(timer)
  }, [form.private_key, t])
  const dirty = useMemo(() => JSON.stringify(form) !== JSON.stringify(formOf(tn)), [form, tn])
  const set = (patch: Partial<Form>) => setForm(f => ({ ...f, ...patch }))
  const reality = !tn || (tn.security ?? 'reality') === 'reality'
  const hasPrivate = tn?.private_key !== undefined || form.private_key !== ''

  async function run(what: string, job: () => Promise<void>) {
    setBusy(what)
    try { await job() } catch (err) { if (!declined(err)) pushSnack(errorText(err), 'error') } finally { setBusy('') }
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
      } catch (err) {
        setKeyError(frontErrorOf(err)?.message || t('admin:nodes.tunnel.failed'))
        return
      }
    }
    onRelays(await updateTunnelListener(nodeId, lineId, input))
    pushSnack(t('admin:nodes.tunnel.saved'), 'success')
  })

  // The preview comes first: the admin sees what would change, and only then is anything written.
  const sync = () => run('sync', async () => {
    let diff
    try { diff = await previewTunnelSync(nodeId, lineId) } catch (err) { pushSnack(errorText(err), 'error'); return }
    if (diff.length === 0) { pushSnack(t('admin:nodes.tunnel.sync_none'), 'info'); return }
    // The server's refusal carries the diff and the plan: the question is asked by the api call.
    onRelays(await syncTunnelFromLanding(nodeId, lineId, dirty ? t('admin:nodes.tunnel.confirm.sync_discards') : undefined))
    pushSnack(t('admin:nodes.tunnel.synced'), 'success')
  })

  const status = (ok: boolean, yes: string, no: string, testId: string) => (
    <Typography data-testid={testId} sx={{ fontSize: 12, color: ok ? md.primary : md.onSurfaceVariant }}>{ok ? yes : no}</Typography>
  )

  return (
    <Box data-testid="tunnel-card" sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
      {status(!!line.running, t('admin:nodes.tunnel.running'), t('admin:nodes.tunnel.pending'), 'tunnel-running')}
      {!tn ? (
        <Typography data-testid="tunnel-missing" sx={{ fontSize: 12, color: md.error }}>{t('admin:nodes.tunnel.missing')}</Typography>
      ) : (
        <>
          {status(tn.applied, t('admin:nodes.tunnel.applied'), t('admin:nodes.tunnel.not_applied', { state: tn.config_sync_state || '-' }), 'tunnel-applied')}
          {status(tn.egress_ready, t('admin:nodes.tunnel.link_ready'), t('admin:nodes.tunnel.link_pending'), 'tunnel-link')}
          <ProbeQualityChip probe={tn.egress_probe} withheld={line.probe_withheld || tn.egress_withheld} testId="tunnel-probe" />
          {tn.routes_withheld && (
            <Typography sx={{ fontSize: 12, color: md.error }}>{t('admin:nodes.tunnel.routes_withheld', { why: tn.routes_withheld })}</Typography>
          )}
          <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
            <TextField size="small" type="number" label={t('admin:nodes.tunnel.port')} value={form.port}
              onChange={e => set({ port: e.target.value })} sx={{ width: 140 }}
              slotProps={{ htmlInput: { 'data-testid': 'tunnel-port' } }} />
            <Button size="small" sx={{ mt: 0.5 }} disabled={!!busy} onClick={() => void randomPort()}>{t('admin:nodes.tunnel.random')}</Button>
          </Box>
          {reality && (
            <>
              <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
                <TextField size="small" label={t('admin:nodes.tunnel.server_name')} value={form.server_name}
                  onChange={e => set({ server_name: e.target.value })} sx={{ flex: 1 }}
                  slotProps={{ htmlInput: { 'data-testid': 'tunnel-sni' } }} />
                <TextField size="small" label={t('admin:nodes.tunnel.target')} value={form.target}
                  onChange={e => set({ target: e.target.value })} sx={{ flex: 1 }}
                  slotProps={{ htmlInput: { 'data-testid': 'tunnel-target' } }} />
                <Button size="small" sx={{ mt: 0.5 }} disabled={!!busy} onClick={() => void randomSite()}>{t('admin:nodes.tunnel.random')}</Button>
              </Box>
              <TextField size="small" label={t('admin:nodes.tunnel.short_id')} value={form.short_id}
                onChange={e => set({ short_id: e.target.value })} slotProps={{ htmlInput: { 'data-testid': 'tunnel-short-id' } }} />
              <TextField size="small" label={t('admin:nodes.tunnel.public_key')} value={form.public_key}
                onChange={e => set({ public_key: e.target.value })} slotProps={{ htmlInput: { 'data-testid': 'tunnel-public-key' } }} />
              {hasPrivate && (
                <TextField size="small" label={t('admin:nodes.tunnel.private_key')} value={form.private_key}
                  type={reveal ? 'text' : 'password'} autoComplete="off"
                  onChange={e => set({ private_key: e.target.value })}
                  error={!!keyError} helperText={keyError || undefined}
                  slotProps={{
                    htmlInput: { 'data-testid': 'tunnel-private-key' },
                    input: {
                      endAdornment: (
                        <InputAdornment position="end">
                          <IconButton size="small" aria-label={t(reveal ? 'admin:nodes.tunnel.hide_key' : 'admin:nodes.tunnel.show_key')}
                            onClick={() => setReveal(r => !r)}>
                            {reveal ? <VisibilityOffIcon fontSize="small" /> : <VisibilityIcon fontSize="small" />}
                          </IconButton>
                        </InputAdornment>
                      ),
                    },
                  }} />
              )}
              <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
                <TextField size="small" label={t('admin:nodes.tunnel.fingerprint')} value={form.fingerprint}
                  onChange={e => set({ fingerprint: e.target.value })} sx={{ flex: 1 }}
                  slotProps={{ htmlInput: { 'data-testid': 'tunnel-fingerprint' } }} />
                <Button size="small" sx={{ mt: 0.5 }} disabled={!!busy} onClick={() => void randomKeys()}>{t('admin:nodes.tunnel.random_keys')}</Button>
              </Box>
            </>
          )}
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.tunnel.prefill_hint')}</Typography>
          <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
            <Button size="small" variant="contained" disabled={!!busy || !dirty || !!keyError} onClick={() => void save()}
              startIcon={busy === 'save' ? <CircularProgress size={14} color="inherit" /> : null}>{t('admin:nodes.tunnel.save')}</Button>
            <Button size="small" disabled={!!busy} onClick={() => void sync()}>{t('admin:nodes.tunnel.sync')}</Button>
            <Button size="small" onClick={() => onOpenListener(tn.node_id)}>{t('admin:nodes.tunnel.open_listener')}</Button>
          </Box>
          <Typography data-testid="tunnel-routes-note" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
            {t('admin:nodes.tunnel.routes_note', { count: tn.routes })}{' '}
            <Button size="small" sx={{ p: 0, minWidth: 0, fontSize: 12 }} onClick={() => onOpenListener(tn.node_id)}>{t('admin:nodes.tunnel.routes_open')}</Button>
          </Typography>
        </>
      )}
    </Box>
  )
}
