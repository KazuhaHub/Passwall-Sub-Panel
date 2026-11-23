import { useCallback, useRef, useState, type ReactElement } from 'react'
import { isAxiosError } from 'axios'

import type { TrustResult } from '@/api/riskCenter'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader,
  AlertDialogMedia, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useInertMuiModals } from '@/lib/inertModals'
import { useReviewAction } from '@/query/riskCenter'
import { useSetServiceStatus } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import type { Translate } from '@/utils/geoAnomaly'
import { useRiskT } from '../useRiskT'
import {
  expectedLevels, holdLabelKey, pauseReason, type RiskActionKind, type RiskSubject,
} from './riskSubject'

const A = 'admin:risk_center.actions.'

/** The server caps a note (and a pause's message) at 200 characters. */
const NOTE_MAX = 200

/** What the admin typed and ticked in an action's dialog. */
interface DialogInput {
  note: string
  /** The dialog's one checkbox: "also trust" on a resume, "also resume" on a
   *  trust. */
  also: boolean
}

/** Everything one dialog says, decided from the action and the account. */
interface DialogCopy {
  title: string
  paragraphs: string[]
  confirm: string
  destructive: boolean
  note?: { label: string; hint?: string }
  check?: { label: string; initial: boolean }
  /** The closing line on what the action does to access and whether it can be undone; with a checkbox, the
   *  line for the box ticked as well, because the box changes what the action does. */
  reversible: { unticked: string; ticked?: string }
}

/**
 * What the dialog says: the action's own explanation, then one line on whether it can be undone — every
 * action here changes how an account is judged or served, so the admin is told what a mistake costs.
 */
function dialogCopy(kind: RiskActionKind, s: RiskSubject, t: Translate): DialogCopy {
  const copy = baseCopy(kind, s, t)
  const key = `${A}reversible_${kind === 'redismiss' ? 'dismiss' : kind}`
  // Trusting a held account offers to resume it, and resuming an untrusted held one offers to trust it:
  // the line follows the box, so it never says the service is untouched when confirming resumes it.
  const reversible = copy.check && (kind === 'trust' || kind === 'resume')
    ? { unticked: t(key), ticked: t(`${key}_${kind === 'trust' ? 'resume' : 'trust'}`) }
    : { unticked: t(key) }
  return { ...copy, reversible }
}

function baseCopy(kind: RiskActionKind, s: RiskSubject, t: Translate): Omit<DialogCopy, 'reversible'> {
  const upn = { upn: s.upn }
  switch (kind) {
    case 'pause': {
      // The dialog names the state the pause will record, so the admin knows
      // whether it counts as a location hold before confirming.
      const state = t(pauseReason(s) === 'geo_anomaly' ? 'admin:users.status.geo_manual' : 'admin:users.status.service_suspended')
      return {
        title: t(`${A}pause_title`, upn), paragraphs: [t(`${A}pause_message`, { state })],
        confirm: t(`${A}pause`), destructive: true,
        // The detail reaches the user's portal and mail: labelled as such,
        // and never prefilled — an empty one keeps the mail's own wording.
        note: { label: t(`${A}pause_note`), hint: t(`${A}pause_note_hint`) },
      }
    }
    case 'convert_manual':
      return {
        title: t(`${A}convert_title`, upn), paragraphs: [t(`${A}convert_message`)],
        confirm: t(`${A}convert_manual`), destructive: true,
        note: { label: t(`${A}pause_note`), hint: t(`${A}pause_note_hint`) },
      }
    case 'resume': {
      const geoAuto = s.service_disabled_reason === 'geo_auto'
      return {
        title: t(`${A}resume_title`, upn),
        paragraphs: [
          t(`${A}resume_message`, { reason: t(holdLabelKey(s.service_disabled_reason)) }),
          // Lifting the detector's hold does not change what it sees: a
          // sustained multi-location use is suspended again, and a confirmed
          // false positive wants trust, offered right here.
          ...(geoAuto ? [t(`${A}resume_geo_auto_warning`)] : []),
        ],
        confirm: t(`${A}resume`), destructive: false,
        check: geoAuto && !s.review.trusted ? { label: t(`${A}resume_also_trust`), initial: false } : undefined,
      }
    }
    case 'dismiss':
    case 'redismiss':
      return {
        title: t(`${A}dismiss_title`, upn), paragraphs: [t(`${A}dismiss_message`), t(`${A}dismiss_trust_hint`)],
        confirm: t(`${A}${kind}`), destructive: false,
        note: { label: t(`${A}dismiss_note`) },
      }
    case 'undismiss':
      return {
        title: t(`${A}undismiss_title`, upn), paragraphs: [t(`${A}undismiss_message`)],
        confirm: t(`${A}undismiss`), destructive: false,
      }
    case 'trust':
      return {
        title: t(`${A}trust_title`, upn), paragraphs: [t(`${A}trust_message`)],
        confirm: t(`${A}trust`), destructive: false,
        // Trusting an account the detector holds would leave it held for
        // nothing: the resume is offered, and on by default.
        check: s.service_disabled_reason === 'geo_auto' ? { label: t(`${A}trust_resume`), initial: true } : undefined,
      }
    case 'untrust':
      return {
        title: t(`${A}untrust_title`, upn), paragraphs: [t(`${A}untrust_message`)],
        confirm: t(`${A}untrust`), destructive: true,
      }
  }
}

/** The server's words for a failure, else the transport's. */
function errorText(err: unknown): string {
  if (isAxiosError(err)) {
    const body = err.response?.data as { error?: string } | undefined
    return String(body?.error || err.message)
  }
  return String(err)
}

/** The 409 codes that say more than "someone else changed this". */
const CONFLICT_KEYS: Record<string, string> = {
  nothing_to_dismiss: `${A}nothing_to_dismiss`,
  changed: `${A}changed`,
  reason_changed: `${A}reason_changed`,
}

/**
 * Reports a refused action and says whether its dialog should close. A 409
 * means the dialog showed a state that is gone — every view is being
 * refreshed, and the dialog's facts with them — so it closes; any other
 * failure keeps it open with what the admin typed, to try again.
 */
function reportFailure(err: unknown, t: Translate): boolean {
  const status = isAxiosError(err) ? err.response?.status : undefined
  const code = isAxiosError(err) ? (err.response?.data as { code?: string } | undefined)?.code : undefined
  if (status === 409) {
    pushSnack(t(CONFLICT_KEYS[code ?? ''] ?? `${A}conflict`), 'info')
    return true
  }
  if (status === 400 && code === 'note_too_long') {
    pushSnack(t(`${A}note_too_long`), 'error')
    return false
  }
  pushSnack(errorText(err), 'error')
  return false
}

/** A trust's outcome, resume included: a lift that went wrong is a warning,
 *  never a success toast beside a still-held account. */
function reportTrust(res: TrustResult, t: Translate) {
  if (res.resume_error) pushSnack(t(`${A}resume_failed`, { error: res.resume_error }), 'warning')
  else if (res.resume_warning) pushSnack(t(`${A}resume_warning`, { error: res.resume_warning }), 'warning')
  else pushSnack(t(res.resumed ? `${A}done_trust_resumed` : `${A}done_trust`), 'success')
}

export interface RiskActions {
  /** Opens the action's dialog for this account; nothing is sent before the
   *  admin confirms. */
  start: (kind: RiskActionKind, subject: RiskSubject) => void
  /** Closes an open dialog without acting (the drawer closing under it). */
  cancel: () => void
  busy: boolean
  /** The dialogs. Render once, anywhere under the caller. */
  dialogs: ReactElement
}

/**
 * The risk center's actions on one account — pause, keep suspended manually,
 * resume, dismiss (again), undo a dismiss, trust, stop trusting — each behind
 * its dialog, shared by the drawer and the queue's row menu.
 *
 * Every request skips the global error toast and every outcome is reported
 * here in its own words: done, each 409 code, a trust whose resume went
 * wrong. Every settled action refreshes the risk center, the users and the
 * bell (invalidateAfterRiskAction), a refusal included: a 409 means this view
 * was stale.
 */
export function useRiskActions(): RiskActions {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const review = useReviewAction(scope)
  const service = useSetServiceStatus(scope)
  // `current` outlives `open`, so the dialog keeps its text through the exit
  // transition; `seq` remounts its fields on every start, so nothing typed
  // for one action carries into the next.
  const [current, setCurrent] = useState<{ kind: RiskActionKind; subject: RiskSubject; seq: number } | null>(null)
  const [open, setOpen] = useState(false)
  const busy = review.isPending || service.isPending

  const start = useCallback((kind: RiskActionKind, subject: RiskSubject) => {
    setCurrent(prev => ({ kind, subject, seq: (prev?.seq ?? 0) + 1 }))
    setOpen(true)
  }, [])

  async function perform(kind: RiskActionKind, s: RiskSubject, input: DialogInput) {
    const note = input.note.trim() || undefined
    switch (kind) {
      case 'pause':
        await service.mutateAsync({ userId: s.id, enabled: false, reason: pauseReason(s), detail: note })
        pushSnack(t(`${A}done_pause`), 'success')
        return
      case 'convert_manual':
        // The backend records the detector's hold as replaced (auto_replaced).
        await service.mutateAsync({ userId: s.id, enabled: false, reason: 'geo_anomaly', detail: note })
        pushSnack(t(`${A}done_convert`), 'success')
        return
      case 'resume':
        if (input.also && s.service_disabled_reason === 'geo_auto' && !s.review.trusted) {
          reportTrust(await review.mutateAsync({ kind: 'trust', userId: s.id, resume: true }) as TrustResult, t)
          return
        }
        // Only while the hold is still the one shown: a hold another admin
        // or the detector wrote since is left alone (409 reason_changed).
        await service.mutateAsync({ userId: s.id, enabled: true, expectReason: s.service_disabled_reason })
        pushSnack(t(`${A}done_resume`), 'success')
        return
      case 'dismiss':
      case 'redismiss':
        await review.mutateAsync({ kind: 'dismiss', userId: s.id, note, expected: expectedLevels(s) })
        pushSnack(t(`${A}done_dismiss`), 'success')
        return
      case 'undismiss':
        await review.mutateAsync({ kind: 'undismiss', userId: s.id })
        pushSnack(t(`${A}done_undismiss`), 'success')
        return
      case 'trust':
        reportTrust(await review.mutateAsync({
          kind: 'trust', userId: s.id, resume: input.also && s.service_disabled_reason === 'geo_auto',
        }) as TrustResult, t)
        return
      case 'untrust':
        await review.mutateAsync({ kind: 'untrust', userId: s.id })
        pushSnack(t(`${A}done_untrust`), 'success')
        return
    }
  }

  async function confirm(input: DialogInput) {
    if (!current) return
    try {
      await perform(current.kind, current.subject, input)
      setOpen(false)
    } catch (err) {
      if (reportFailure(err, t)) setOpen(false)
    }
  }

  const dialogs = (
    <>
      {current && (
        <ActionDialog key={current.seq} open={open} busy={busy}
          copy={dialogCopy(current.kind, current.subject, t)} cancelText={t('common:actions.cancel')}
          onCancel={() => setOpen(false)} onConfirm={confirm} />
      )}
    </>
  )
  const cancel = useCallback(() => setOpen(false), [])
  return { start, cancel, busy, dialogs }
}

/** Above every other layer the page may still have open (the risk drawer included), and below the toasts. */
const ABOVE_EVERY_MODAL = 'z-[1600]'

/**
 * One action's confirmation. It is an alert dialog whose focus opens ON Cancel: every action here changes
 * how an account is judged or served, so a stray Enter must not be able to confirm it. The confirm button
 * shows its own wait and the dialog stays open on a refusal other than a conflict, with what was typed.
 */
function ActionDialog({ open, busy, copy, cancelText, onCancel, onConfirm }: {
  open: boolean
  busy: boolean
  copy: DialogCopy
  cancelText: string
  onCancel: () => void
  onConfirm: (input: DialogInput) => Promise<void>
}) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [note, setNote] = useState('')
  const [also, setAlso] = useState(copy.check?.initial ?? false)
  // The Users page still hosts MUI dialogs on this branch; they are made inert while this one is over them.
  useInertMuiModals(open)
  return (
    <AlertDialog open={open} onOpenChange={next => { if (!next && !busy) onCancel() }}>
      <AlertDialogContent initialFocus={cancelRef} overlay={{ className: ABOVE_EVERY_MODAL }}
        className={cn(ABOVE_EVERY_MODAL, 'data-[size=default]:max-w-md')}>
        <AlertDialogHeader>
          {copy.destructive && (
            <AlertDialogMedia className="bg-destructive/10 text-destructive"><IconWarning aria-hidden /></AlertDialogMedia>
          )}
          <AlertDialogTitle>{copy.title}</AlertDialogTitle>
          <AlertDialogDescription render={<div />}>
            <div className="flex flex-col gap-2">
              {[...copy.paragraphs, also && copy.reversible.ticked ? copy.reversible.ticked : copy.reversible.unticked]
                .map((text, i) => <p key={i}>{text}</p>)}
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        {(copy.note || copy.check) && (
          <div className="flex flex-col gap-3">
            {copy.note && (
              <Field label={copy.note.label} hint={copy.note.hint}>
                <Textarea value={note} rows={2} maxLength={NOTE_MAX} onChange={e => setNote(e.target.value)} />
              </Field>
            )}
            {copy.check && (
              <Field label={copy.check.label} orientation="horizontal">
                <Checkbox checked={also} onCheckedChange={c => setAlso(c === true)} />
              </Field>
            )}
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef} disabled={busy}>{cancelText}</AlertDialogCancel>
          <AsyncButton variant={copy.destructive ? 'destructive' : 'default'} disabled={busy}
            onClick={() => onConfirm({ note, also })}>
            {copy.confirm}
          </AsyncButton>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
