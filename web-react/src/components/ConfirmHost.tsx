import { useEffect, useState } from 'react'
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Typography,
  useTheme,
} from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

export interface ConfirmOpts {
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  destructive?: boolean
}

interface InternalState {
  open: boolean
  opts: ConfirmOpts | null
}

let resolver: ((v: boolean) => void) | null = null
let setStateExternal: ((s: InternalState) => void) | null = null

// Promise-style confirmation. Resolves true when user confirms,
// false when they cancel or close the dialog.
export function confirm(opts: ConfirmOpts): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    if (!setStateExternal) {
      console.warn('ConfirmHost not mounted')
      resolve(false)
      return
    }
    resolver = resolve
    setStateExternal({ open: true, opts })
  })
}

export default function ConfirmHost() {
  const { t } = useTranslation('common')
  const theme = useTheme()
  const md = theme.palette.md
  const [state, setState] = useState<InternalState>({ open: false, opts: null })

  useEffect(() => {
    setStateExternal = setState
    return () => { setStateExternal = null }
  }, [])

  function close(answer: boolean) {
    setState({ open: false, opts: state.opts })
    resolver?.(answer)
    resolver = null
  }

  const opts = state.opts
  const destructive = opts?.destructive
  return (
    <Dialog
      open={state.open}
      onClose={() => close(false)}
      PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, minWidth: 320, maxWidth: 480 } }}
    >
      <DialogTitle sx={{ display: 'flex', gap: 1.5, alignItems: 'center', pt: 3 }}>
        {destructive && (
          <Box sx={{
            width: 40, height: 40, borderRadius: '50%',
            display: 'grid', placeItems: 'center', flexShrink: 0,
            bgcolor: md.errorContainer, color: md.onErrorContainer,
          }}>
            <WarningAmberIcon />
          </Box>
        )}
        <Typography variant="h6" component="span">{opts?.title ?? ''}</Typography>
      </DialogTitle>
      <DialogContent>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, whiteSpace: 'pre-line' }}>
          {opts?.message ?? ''}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => close(false)} variant="text">
          {opts?.cancelText ?? t('actions.cancel')}
        </Button>
        <Button
          onClick={() => close(true)}
          variant="contained"
          autoFocus
          sx={destructive ? { bgcolor: md.error, color: md.onError, '&:hover': { bgcolor: md.error } } : undefined}
        >
          {opts?.confirmText ?? t('actions.ok')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
