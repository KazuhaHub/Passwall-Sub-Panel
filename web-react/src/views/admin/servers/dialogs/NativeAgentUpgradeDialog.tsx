import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getNativeAgentUpgrade, requestNativeAgentUpgrade, upgradeOptions, type NativeAgentUpgrade, type Server } from '@/api/servers'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { IconSpinner } from '@/lib/icons'
import { canonicalReleaseVersion } from '@/utils/productVersion'
import NodeReleaseSelector from '../install/NodeReleaseSelector'
import { DialogNotice } from './DialogNotice'

const ENDED = ['verified', 'failed', 'manual_attention', 'dispatch_closed']

export function NativeAgentUpgradeDialog({ server, onClose }: { server: Server | null, onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const cancelRef = useRef<HTMLButtonElement>(null)
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
  // The server's own refusal, when it has one. Empty means it did not refuse.
  const [blocked, setBlocked] = useState('')
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
  // THE NODE'S OWN REASON, shown only where the operator has to act on it. A node
  // on a UI-only Docker host has no shell to read the updater's log from, so this
  // is the one diagnostic its operator can reach. It is the node's string: render
  // it as text (React escapes it) and never as markup.
  const reason = task && ['failed', 'manual_attention'].includes(task.upgrade_state)
    ? [task.result_error_code, task.result_error].filter(Boolean).join(': ')
    : ''

  useEffect(() => {
    requestController.current?.abort()
    setVersion(''); setTask(null); setBusy(false); setError(''); setBlocked(''); key.current = ''
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
        // NO PREDICATE AT ALL. This list was filtered twice before — by a
        // verified edge, then by whether a signed policy offered the release — and
        // each filter emptied the dialog for a node the operator could see, with
        // the remedy a document they had no reason to know about. What the panel
        // offers is what the panel can see is published.
        setTargets((option.targets ?? []).map(target => target.version))
        // THE SERVER'S REFUSAL IS SHOWN INSTEAD OF A MENU. A node that cannot be
        // upgraded at all — most often because its own version is not a canonical
        // release, so it never registered the upgrade handler and never advertised
        // the capability — used to be offered the whole release list and refused
        // only after the operator had chosen from it.
        const refusal = option.state === 'blocked' ? (option.detail || t('admin:servers.agent_upgrade.blocked_fallback')) : ''
        setBlocked(refusal)
        // THE SELECTOR MAY HAVE AUTO-SELECTED ALREADY. It resolves from its own
        // catalog fetch, which can land before this one, so a refusal that only
        // hid the selector would leave a version chosen and Confirm live next to
        // the alert explaining why it cannot be done.
        if (refusal) setVersion('')
      })
      .catch(() => { if (live) { setTargets(undefined); setBlocked('') } })
    return () => { live = false }
  }, [server?.id])

  useEffect(() => {
    if (!server || !task || ENDED.includes(task.upgrade_state)) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      try {
        const status = await getNativeAgentUpgrade(server.id, task.task_id, controller.signal)
        if (controller.signal.aborted) return
        setTask(status); setError('')
        if (!ENDED.includes(status.upgrade_state)) timer = setTimeout(poll, 3000)
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

  const target = version.trim()
  const canSubmit = !busy && exact(target) && target !== expected
  // The confirm names the server and both versions in one sentence, so what the button does is read before it is pressed.
  const stateTone = task?.upgrade_state === 'verified' ? 'success' : task && ENDED.includes(task.upgrade_state) ? 'warning' : 'info'

  return <Dialog open={!!server} onOpenChange={open => { if (!open && !busy) onClose() }}>
    <DialogContent showCloseButton={false} initialFocus={cancelRef} className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{t('admin:servers.agent_upgrade.title', { name: server?.name ?? '' })}</DialogTitle>
        <DialogDescription>{t('admin:servers.agent_upgrade.requirements')}</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-4">
        <DialogNotice tone="warning">{t('admin:servers.agent_upgrade.warning')}</DialogNotice>
        <Field label={t('admin:servers.agent_upgrade.current')}>
          <Input value={expected} readOnly />
        </Field>
        {blocked
          ? <DialogNotice tone="warning">{blocked}</DialogNotice>
          : <NodeReleaseSelector key={server?.id} enabled={!!server} selection={{ method: 'linux', os: 'linux', arch: 'amd64' }}
            initialChannel={effectiveUpdateChannel(server?.update_channel)} value={version}
            onChange={next => { setVersion(next); if (!error) key.current = '' }} autoSelectLatest context="upgrade"
            newerThan={expected} targets={targets} disabled={busy || !!task || !!error} />}
        <p className="text-xs text-muted-foreground">{t('admin:servers.agent_upgrade.version_hint')}</p>
        {!task && !blocked && canSubmit && <p className="text-sm font-medium" data-testid="upgrade-summary">
          {t('admin:servers.agent_upgrade.summary', { name: server?.name ?? '', from: expected, to: target })}
        </p>}
        {error && <DialogNotice tone="danger">{error}</DialogNotice>}
        {task && <>
          <DialogNotice tone={stateTone}>{t(`admin:servers.agent_upgrade.state.${task.upgrade_state}`)}</DialogNotice>
          {reason && <div className="flex flex-col gap-1">
            <p className="text-sm">{t('admin:servers.agent_upgrade.reason')}</p>
            <div className="font-mono text-xs break-words whitespace-pre-wrap">{reason}</div>
          </div>}
          <p className="text-sm break-all">{t('admin:servers.agent_upgrade.task', { id: task.task_id })}</p>
          {task.binary_sha256 && <p className="text-xs break-all">SHA-256: {task.binary_sha256}</p>}
          {(error || task.dispatch_closed) && <Button variant="outline" className="self-start" onClick={() => {
            requestController.current?.abort()
            const controller = new AbortController(); requestController.current = controller
            void getNativeAgentUpgrade(server!.id, task.task_id, controller.signal).then(status => {
              if (!controller.signal.aborted) { setTask(status); setError('') }
            }).catch(() => { if (!controller.signal.aborted) setError(t('admin:servers.agent_upgrade.poll_failed')) })
            setRetry(n => n + 1)
          }}>{t('admin:servers.agent_upgrade.refresh')}</Button>}
        </>}
      </div>
      <DialogFooter>
        <Button ref={cancelRef} variant="outline" onClick={onClose} disabled={busy}>{t('common:actions.close')}</Button>
        {!task && !blocked && <Button onClick={() => void submit()} disabled={!canSubmit}>
          {busy && <IconSpinner aria-hidden className="animate-spin" />}{t(error ? 'admin:servers.agent_upgrade.retry' : 'admin:servers.agent_upgrade.confirm')}
        </Button>}
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
