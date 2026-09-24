import { useEffect, useState } from 'react'
import { LinearProgress } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useWriteInProgress } from '@/api/requestProgress'

// Past this long a write is a wait worth showing; below it the bar would only
// flicker on every fast save.
const SHOW_AFTER_MS = 250

// A thin bar across the top of the window while a write is on the wire — the
// app-wide floor under every button that has no pending state of its own.
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
  return <LinearProgress aria-label={t('common:status.working')}
    sx={{ position: 'fixed', top: 0, left: 0, right: 0, height: 3, zIndex: theme => theme.zIndex.tooltip + 1 }} />
}
