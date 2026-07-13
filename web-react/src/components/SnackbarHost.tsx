import { useEffect, useState } from 'react'
import { Alert, Snackbar } from '@mui/material'

// Module-level pub-sub so non-React code (axios interceptors) can push
// snackbars without prop drilling or context.
type Severity = 'info' | 'success' | 'warning' | 'error'
interface SnackEvent {
  id: number
  message: string
  severity: Severity
}

let nextId = 1
let listener: ((evt: SnackEvent) => void) | null = null

export function pushSnack(message: string, severity: Severity = 'info') {
  if (!message) return
  listener?.({ id: nextId++, message, severity })
}

export default function SnackbarHost() {
  // Queue events and show them one at a time (MUI "consecutive snackbars"
  // pattern). A burst of distinct errors — e.g. a Promise.allSettled batch —
  // no longer clobbers all-but-the-last toast.
  const [queue, setQueue] = useState<SnackEvent[]>([])
  const [current, setCurrent] = useState<SnackEvent | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    listener = (evt) => setQueue(q => [...q, evt])
    return () => { listener = null }
  }, [])

  useEffect(() => {
    if (queue.length === 0) return
    if (!current) {
      // Idle → show the next queued snack.
      setCurrent(queue[0])
      setQueue(q => q.slice(1))
      setOpen(true)
    } else if (open) {
      // A new snack arrived while one is showing → close it so the
      // onExited handler drains the next.
      setOpen(false)
    }
  }, [queue, current, open])

  return (
    <Snackbar
      key={current?.id}
      open={open}
      autoHideDuration={4000}
      onClose={(_, reason) => { if (reason !== 'clickaway') setOpen(false) }}
      anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      slotProps={{
        transition: { onExited: () => setCurrent(null) }
      }}
    >
      {current ? (
        <Alert onClose={() => setOpen(false)} severity={current.severity} variant="filled" sx={{ minWidth: 280 }}>
          {current.message}
        </Alert>
      ) : undefined}
    </Snackbar>
  );
}
