import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, Link, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getNativeAgentUpgrade, requestNativeAgentUpgrade, type NativeAgentUpgrade, type Server } from '@/api/servers'

export function NativeAgentUpgradeDialog({ server, onClose }: { server: Server | null, onClose: () => void }) {
  const { t } = useTranslation()
  const [version, setVersion] = useState('')
  const [task, setTask] = useState<NativeAgentUpgrade | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const key = useRef('')
  const requestController = useRef<AbortController | null>(null)
  const expected = server?.panel_version?.split(' ')[0] ?? ''
  const exact = (s: string) => /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/.test(s)

  useEffect(() => {
    requestController.current?.abort()
    setVersion(''); setTask(null); setBusy(false); setError(''); key.current = ''
    return () => requestController.current?.abort()
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
    if (!server || busy || task) return
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
        <TextField label={t('admin:servers.agent_upgrade.target')} placeholder="v0.0.1-beta3" value={version}
          onChange={event => { setVersion(event.target.value); if (!error) key.current = '' }} disabled={busy || !!task || !!error}
          helperText={t('admin:servers.agent_upgrade.version_hint')} />
        <Link href="https://github.com/KazuhaHub/Passwall-Node/releases" target="_blank" rel="noopener noreferrer">{t('admin:servers.native.releases')}</Link>
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
      <Button onClick={onClose} disabled={busy}>{t('common:close', { defaultValue: '关闭' })}</Button>
      {!task && <Button variant="contained" onClick={() => void submit()} disabled={busy || !exact(version.trim()) || !exact(expected) || version.trim() === expected}>
        {busy && <CircularProgress size={16} sx={{ mr: 1 }} />}{t(error ? 'admin:servers.agent_upgrade.retry' : 'admin:servers.agent_upgrade.confirm')}
      </Button>}
    </DialogActions>
  </Dialog>
}
