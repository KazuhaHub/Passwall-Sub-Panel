import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useWriteInProgress } from '@/api/requestProgress'

// Past this long a write is a wait worth showing; below it the bar would only
// flicker on every fast save.
const SHOW_AFTER_MS = 250

// A thin bar across the top of the window while a write is on the wire — the
// app-wide floor under every button that has no pending state of its own.
//
// Indeterminate by nature (nothing says how far a request is), so a short segment
// slides along a faint track. The track alone stays visible under reduced motion,
// where the slide is switched off globally (styles/ui.css), so the bar still says
// "working" without moving. z-index sits above the modal layers (1600, see
// ConfirmHost) so it also shows while a dialog is saving.
export default function RequestProgressBar() {
  const { t } = useTranslation('common')
  const busy = useWriteInProgress()
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    if (!busy) {
      setVisible(false)
      return
    }
    const timer = setTimeout(() => setVisible(true), SHOW_AFTER_MS)
    return () => clearTimeout(timer)
  }, [busy])

  if (!visible) return null
  return (
    <div
      role="progressbar"
      aria-label={t('common:status.working')}
      className="pointer-events-none fixed inset-x-0 top-0 z-[1700] h-[3px] overflow-hidden bg-brand/25"
    >
      <div className="h-full w-2/5 animate-request-progress bg-brand" />
    </div>
  )
}
