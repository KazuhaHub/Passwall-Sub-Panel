import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getNativeAgentUpgrade, requestNativeAgentUpgrade, upgradeOptions, type NativeAgentUpgrade, type Server } from '@/api/servers'
import NodeReleaseSelector from '@/components/NodeReleaseSelector'
import { canonicalReleaseVersion } from '@/utils/productVersion'

export function NativeAgentUpgradeDialog({ server, onClose }: { server: Server | null, onClose: () => void }) {
  const { t } = useTranslation()
  const [version, setVersion] = useState('')
  const [task, setTask] = useState<NativeAgentUpgrade | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  // The releases a verified edge actually reaches. `newerThan` alone would offer
  // anything ahead of the node, including paths nobody has walked — and a
  // request along one of those is refused, which teaches the operator to distrust
  // the list rather than the request.
  const [targets, setTargets] = useState<string[] | undefined>(undefined)
  const key = useRef('')
  const requestController = useRef<AbortController | null>(null)
  // THE NODE'S OWN RECORD OF ITSELF, shown as-is and never parsed here. The node
  // is what compares it against the request, so a stamp from the replaced scheme
  // (`v0.0.1-beta9`) is a value this dialog passes through rather than judges —
  // requiring it to be a product version is what made those nodes un-upgradable.
  const expected = server?.panel_version?.split(' ')[0] ?? ''
  // A TARGET MUST NAME A RELEASE, because that is what the node turns into a
  // download address. This rule is about the target only; it used to be applied
  // to the node's reported version as well, which put the shape decision in the
  // wrong place.
  const exact = (s: string) => canonicalReleaseVersion(s) !== undefined

  useEffect(() => {
    requestController.current?.abort()
    setVersion(''); setTask(null); setBusy(false); setError(''); key.current = ''
    return () => requestController.current?.abort()
  }, [server?.id, server?.update_channel])

  useEffect(() => {
    if (!server) { setTargets(undefined); return }
    let live = true
    // Best-effort: without an answer the list falls back to "strictly newer",
    // which is weaker but not wrong, and the write path still protects the fire.
    void upgradeOptions(server.id, 'agent')
      .then(option => {
        if (!live) return
        const reachable = (option.targets ?? [])
          // ONE PREDICATE, NOT TWO. The list was filtered by a verified edge as
          // well, because admission refused a pair nobody had walked — so a node on a
          // version no edge started from got an empty dialog, and the remedy was a
          // policy document the operator had no reason to know about. Admission
          // follows the panel's own judgement now.
          .filter(target => target.offered_by_policy)
          .map(target => target.version)
        setTargets(reachable)
      })
      .catch(() => { if (live) setTargets(undefined) })
    return () => { live = false }
  }, [server?.id])

  useEffect(() => {
    if (!server || !task || ['verified', 'failed', 'manual_attention', 'dispatch_closed'].includes(task.upgrade_state)) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      try {
        const status = await getNativeAgentUpgrade(server.id, task.task_id, controller.signal)
        if (controller.signal.aborted) return
        setTask(status); setError('')
        if (!['verified', 'failed', 'manual_attention', 'dispatch_closed'].includes(status.upgrade_state)) timer = setTimeout(poll, 3000)
      } catch {
        if (!controller.signal.aborted) setError(t('admin:servers.agent_upgrade.poll_failed'))
      }
    }
    timer = setTimeout(poll, 1000)
    return () => { controller.abort(); if (timer) clearTimeout(timer) }
  }, [server?.id, task?.task_id, task?.upgrade_state, retry, t])

  async function submit() {
    if (!server || busy || task || !exact(version.trim()) || version.trim() === expected) return
    if (!key.current) key.current = crypto.randomUUID()
    const controller = new AbortController(); requestController.current = controller
    setBusy(true); setError('')
    try {
      const result = await requestNativeAgentUpgrade(server.id, version.trim(), expected, key.current, controller.signal)
      if (!controller.signal.aborted) setTask(result)
    } catch {
      if (!controller.signal.aborted) setError(t('admin:servers.agent_upgrade.request_failed'))
    } finally { if (!controller.signal.aborted) setBusy(false) }
  }

  return <Dialog open={!!server} onClose={busy ? undefined : onClose} fullWidth maxWidth="sm">
    <DialogTitle>{t('admin:servers.agent_upgrade.title', { name: server?.name ?? '' })}</DialogTitle>
    <DialogContent>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 1 }}>
        <Alert severity="warning">{t('admin:servers.agent_upgrade.warning')}</Alert>
        <Typography variant="body2">{t('admin:servers.agent_upgrade.requirements')}</Typography>
        <TextField label={t('admin:servers.agent_upgrade.current')} value={expected} slotProps={{ input: { readOnly: true } }} />
        <NodeReleaseSelector key={server?.id} enabled={!!server} selection={{ method: 'linux', os: 'linux', arch: 'amd64' }}
          initialChannel={server?.update_channel === 'beta' ? 'testing' : 'stable'} value={version}
          onChange={next => { setVersion(next); if (!error) key.current = '' }} autoSelectLatest context="upgrade"
          newerThan={exact(expected) ? expected : undefined} targets={targets} disabled={busy || !!task || !!error} />
        <Typography variant="body2">{t('admin:servers.agent_upgrade.version_hint')}</Typography>
        {error && <Alert severity="error">{error}</Alert>}
        {task && <>
          <Alert severity={task.upgrade_state === 'verified' ? 'success' : ['failed', 'manual_attention', 'dispatch_closed'].includes(task.upgrade_state) ? 'warning' : 'info'}>
            {t(`admin:servers.agent_upgrade.state.${task.upgrade_state}`)}
          </Alert>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t('admin:servers.agent_upgrade.task', { id: task.task_id })}</Typography>
          {task.binary_sha256 && <Typography variant="caption" sx={{ overflowWrap: 'anywhere' }}>SHA-256: {task.binary_sha256}</Typography>}
          {(error || task.dispatch_closed) && <Button onClick={() => {
            requestController.current?.abort()
            const controller = new AbortController(); requestController.current = controller
            void getNativeAgentUpgrade(server!.id, task.task_id, controller.signal).then(status => {
              if (!controller.signal.aborted) { setTask(status); setError('') }
            }).catch(() => { if (!controller.signal.aborted) setError(t('admin:servers.agent_upgrade.poll_failed')) })
            setRetry(n => n + 1)
          }}>{t('admin:servers.agent_upgrade.refresh')}</Button>}
        </>}
      </Box>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose} disabled={busy}>{t('common:actions.close')}</Button>
      {!task && <Button variant="contained" onClick={() => void submit()} disabled={busy || !exact(version.trim()) || version.trim() === expected}>
        {busy && <CircularProgress size={16} sx={{ mr: 1 }} />}{t(error ? 'admin:servers.agent_upgrade.retry' : 'admin:servers.agent_upgrade.confirm')}
      </Button>}
    </DialogActions>
  </Dialog>
}
