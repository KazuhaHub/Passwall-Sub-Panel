import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { confirm } from '@/components/ConfirmHost'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardChangesConfirm } from '../accessControl/confirmCopy'

/** Which sheet the address names (`?edit=` and `?copy=`): a draft is lost when this changes, or when the page does. */
const sheetOf = (search: string) => { const p = new URLSearchParams(search); return `${p.get('edit')}|${p.get('copy')}` }

/**
 * The unsaved-changes guard of a rule-set or template sheet, which edits a long document: closing it (Esc, the
 * close button, the backdrop), going Back, following a link to another page and reloading all ask once before
 * the draft is dropped. `finish(done)` is for the sheet's own exits (after the question was answered, or after a
 * save) and lets the address change through without asking again. A close while a save is running does nothing.
 */
export function useSheetGuard({ dirty, pending, onClose }: { dirty: boolean; pending: boolean; onClose: () => void }) {
  const { t } = useTranslation(['admin'])
  const leave = useRef(false)
  const copy = discardChangesConfirm(t)
  useLeaveGuard(dirty, copy, (next, current) => !leave.current && (next.pathname !== current.pathname || sheetOf(next.search) !== sheetOf(current.search)))
  const finish = (done: () => void) => { leave.current = true; done() }
  const close = () => {
    if (pending) return
    if (!dirty) { finish(onClose); return }
    void confirm(copy).then(ok => { if (ok) finish(onClose) })
  }
  return { close, finish }
}
