import { useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconCopy, IconWarning } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copyToClipboard } from '@/utils/clipboard'

/**
 * The one-line install command a one-time ticket makes. Display only: issuance, identity binding and expiry
 * remain owned by the installation flow, which passes `disabled` once the ticket has expired.
 *
 * THE COPY BUTTON IS THIS FILE'S OWN, NOT THE SHARED CopyButton, because the command is a secret with a lifetime:
 * the click re-reads the expiry (an expired command is never copied, it reports expiry instead), and a copy that
 * is still in flight when the command changes must not report its result against the new one.
 */
export default function NodeInstallCommand({ command, expiresAt, disabled = false, onExpired }: {
  command: string
  expiresAt: string
  disabled?: boolean
  onExpired: () => void
}) {
  const { t, i18n } = useTranslation('admin')
  const expires = new Date(expiresAt)
  const expiryLabel = Number.isFinite(expires.getTime()) ? expires.toLocaleString(i18n.language) : expiresAt
  const [feedback, setFeedback] = useState<'copied' | 'copy_failed' | ''>('')
  const [copying, setCopying] = useState(false)
  const intent = useRef(0)
  const observed = useRef({ command, expiresAt, disabled })
  useLayoutEffect(() => {
    const previous = observed.current
    const changed = previous.command !== command || previous.expiresAt !== expiresAt || previous.disabled !== disabled
    observed.current = { command, expiresAt, disabled }
    // Invalidating on unmount OR on an unchanged dependency cycle discards a copy
    // that is already in flight, and both happen without the user doing anything:
    // StrictMode unmounts and remounts every component, so a copy clicked between
    // the two passes resolved against a bumped intent and its feedback vanished —
    // the button looked dead. Dependency changes are already invalidated above,
    // during commit, so the cleanup has nothing left to cancel: a real unmount
    // discards the state it would have written anyway.
    if (changed) {
      intent.current += 1
      setFeedback('')
      setCopying(false)
    }
  }, [command, expiresAt, disabled])

  async function copy() {
    if (disabled || copying || !command) return
    if (Date.parse(expiresAt) <= Date.now()) { onExpired(); return }
    const current = intent.current
    setCopying(true)
    setFeedback('')
    try {
      const copied = await copyToClipboard(command)
      if (intent.current === current) setFeedback(copied ? 'copied' : 'copy_failed')
    } catch {
      if (intent.current === current) setFeedback('copy_failed')
    } finally {
      if (intent.current === current) setCopying(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      {!disabled && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="node-install-command">{t('admin:servers.native.install_command')}</Label>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <Input id="node-install-command" readOnly value={command} autoComplete="off" spellCheck={false} className="font-mono text-xs" />
            {/* AsyncButton with the in-flight state passed in: the shared clipboard write can take a moment, and a disabled-only button looks broken. */}
            <AsyncButton variant="outline" pending={copying} icon={<IconCopy />} className="shrink-0" onClick={() => copy()}>{t('admin:servers.native.copy_command')}</AsyncButton>
          </div>
        </div>
      )}
      {disabled && <Button variant="outline" disabled className="self-start"><IconCopy />{t('admin:servers.native.copy_command')}</Button>}
      <p className={disabled ? 'flex items-center gap-1.5 text-xs text-warning-ink' : 'text-xs text-muted-foreground'}>
        {disabled && <IconWarning aria-hidden className="size-3.5 shrink-0" />}
        {t(disabled ? 'admin:servers.native.command_expired' : 'admin:servers.native.command_safety', { time: expiryLabel })}
      </p>
      {feedback && <p role={feedback === 'copy_failed' ? 'alert' : 'status'} className="text-sm">{t(`admin:servers.native.${feedback}`)}</p>}
    </div>
  )
}
