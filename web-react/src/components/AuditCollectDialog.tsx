import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { AuditCollect, DestNodeStatus } from '@/api/accessControl'
import { ModeRadioGroup } from '@/components/access/ModeRadioGroup'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { pushSnack } from '@/components/feedback/toasts'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestSettings, useDestStatus, useSetAuditCollect } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { applyMinutes } from '@/utils/accessControl'
import { discardChangesConfirm } from '@/views/admin/accessControl/confirmCopy'
import { destErrorText } from '@/views/admin/accessControl/destErrors'

const A = 'admin:access_control.audit_collect.'

/** The modes, in the order they are offered. The usage mode is offered only where the node declares it. */
const MODES: AuditCollect[] = ['off', 'hits', 'hits_and_usage']

/**
 * "What does this node record?" (S14/S15): the one place a native server's
 * recording mode is written. It is shared by the coverage sheet and the
 * server's access dialog. The restart sentence beside Save is the warning, so
 * there is no confirmation after it (S20). A node that cannot record (too old)
 * may only be set to not recording; a sing-box node only enforces, and its
 * dialog is read-only. The days a mode keeps its records are the retention
 * settings, read, never written in.
 *
 * The usage mode (per-site connection counts, Xray nodes that declare it) says
 * what it costs: about 40% more proxy CPU on a busy node, which is why it is a
 * per-node opt-in. Choosing it while the privacy page is off only hints (amber);
 * turning it off says the usage already recorded stays until it expires.
 */
export default function AuditCollectDialog({ node, onClose }: {
  /** The node being set, or null (closed). */
  node: DestNodeStatus | null
  onClose: () => void
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const open = node !== null
  const status = useDestStatus(scope, { enabled: open })
  const settings = useDestSettings(scope, { enabled: open })
  const save = useSetAuditCollect(scope)
  // The server answers the normalised value (unset is the default, hits); the usage mode is shown as what it is, not as hits.
  const stored: AuditCollect = node?.collect === 'off' ? 'off' : node?.collect === 'hits_and_usage' ? 'hits_and_usage' : 'hits'
  const [mode, setMode] = useState<AuditCollect>(stored)
  const [error, setError] = useState<string | null>(null)
  const execOnly = node?.engine === 'sing-box'
  const tooOld = !!node && !node.supports.hits && !execOnly
  const dirty = open && mode !== stored
  const legalOn = useSiteStore(s => s.legalEnabled)
  const noUsage = !!node && !node.supports.usage && !execOnly && !tooOld

  // Seeded on each open: a node's mode read while the dialog is open never overwrites a choice in progress.
  useEffect(() => { if (open) { setMode(stored); setError(null) } }, [open, node?.panel_id]) // eslint-disable-line react-hooks/exhaustive-deps

  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const days = settings.data?.effective.dest_hit_retention_days
  const usageDays = settings.data?.effective.dest_usage_retention_days
  const minutes = applyMinutes(status.data)

  const submit = async () => {
    if (!node) return
    setError(null)
    try {
      await save.mutateAsync({ serverId: node.panel_id, collect: mode })
      pushSnack(minutes === null
        ? t(`${A}saved_unknown`, { node: node.panel_name })
        : t(`${A}saved`, { node: node.panel_name, count: minutes }), 'success')
      onClose()
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  const options = MODES.map(value => {
    const disabled = execOnly || (tooOld && value !== 'off') || save.isPending || (value === 'hits_and_usage' && (tooOld || noUsage))
    return {
      value, disabled,
      children: (
        <>
          <span className="block text-sm font-semibold">{t(`${A}mode.${value}`)}</span>
          <span className="block text-muted-foreground">{t(`${A}mode_help.${value}`, { days: days ?? '…', usageDays: usageDays ?? '…' })}</span>
          {value === 'hits' && tooOld && <span className="block text-foreground">{t(`${A}too_old`)}</span>}
          {value === 'hits_and_usage' && <span className="block text-foreground">{t(`${A}usage_cost`)}</span>}
          {value === 'hits_and_usage' && (noUsage || tooOld) && <span className="block text-foreground">{t(`${A}usage_too_old`)}</span>}
        </>
      ),
    }
  })

  return (
    <Dialog open={open} onOpenChange={next => { if (!next) guard(onClose) }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(`${A}title`, { name: node?.panel_name ?? '' })}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          {execOnly && <p className="text-sm">{t(`${A}exec_only`)}</p>}
          {error && <Alert variant="destructive">{error}</Alert>}
          <ModeRadioGroup<AuditCollect> value={mode} onChange={setMode} aria-label={t(`${A}label`)} options={options} />
          {mode === 'hits_and_usage' && !legalOn && (
            <FieldHint tone="warning" summary={t(`${A}usage_privacy_summary`)} detail={t(`${A}usage_privacy_detail`)} />
          )}
          {stored === 'hits_and_usage' && mode !== 'hits_and_usage' && (
            <p className="text-sm text-muted-foreground">{t(`${A}usage_off_note`, { usageDays: usageDays ?? '…' })}</p>
          )}
        </div>
        {!execOnly && <p className="text-xs text-muted-foreground">{mode === 'hits_and_usage' ? t(`${A}restart_usage`, { usageDays: usageDays ?? '…' }) : t(`${A}restart`)}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={() => guard(onClose)}>{t(`${A}cancel`)}</Button>
          {!execOnly && <AsyncButton disabled={!dirty} onClick={submit}>{t(`${A}save`)}</AsyncButton>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
