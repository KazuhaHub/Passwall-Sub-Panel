import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Box, Button, Card, Typography, useTheme } from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import ReportProblemOutlinedIcon from '@mui/icons-material/ReportProblemOutlined'
import { useTranslation } from 'react-i18next'

interface State {
  error: Error | null
}

interface Props {
  children: ReactNode
}

// ErrorBoundary catches any render-time error thrown by its descendants
// and shows a friendly fallback page instead of letting the SPA crash
// to a blank white screen. Async errors (fetch rejections, setTimeout
// throws, event handlers) bypass React's boundary mechanism by design —
// those are handled by the global axios error interceptor + the
// SnackbarHost, not here.
//
// Boundaries MUST be class components because the hook surface (which
// can read context, translate strings, theme, etc.) is unavailable
// inside getDerivedStateFromError / componentDidCatch. The render path
// hands off to a function component as soon as the error is captured.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Stash to console with the component stack so developers debugging
    // a customer report can copy the trace out of the browser. The
    // backend has no /api/client-errors endpoint today; if we add one
    // later this is the right place to ship the report.
    // eslint-disable-next-line no-console
    console.error('Unhandled render error', error, info.componentStack)
  }

  handleReload = () => {
    // Full reload rather than setState({error: null}) — the broken
    // component might have torn state we can't recover from in-process.
    // Refresh is the contract the user actually wants.
    window.location.reload()
  }

  render() {
    if (!this.state.error) return this.props.children
    return <ErrorFallback error={this.state.error} onReload={this.handleReload} />
  }
}

function ErrorFallback({ error, onReload }: { error: Error; onReload: () => void }) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('common')
  return (
    <Box sx={{
      position: 'fixed', inset: 0,
      display: 'grid', placeItems: 'center',
      bgcolor: md.surface, p: 3, overflow: 'auto',
    }}>
      <Card sx={{
        maxWidth: 520, width: '100%',
        p: { xs: 3, sm: 4 },
        bgcolor: md.surfaceContainer,
        borderRadius: 4,
        textAlign: 'center',
        border: `1px solid ${md.outlineVariant}`,
      }}>
        <ReportProblemOutlinedIcon sx={{ fontSize: 56, color: md.error, mb: 2 }} />
        <Typography variant="h5" sx={{ fontWeight: 500, mb: 1.5, color: md.onSurface }}>
          {t('error_boundary.title')}
        </Typography>
        <Typography sx={{ color: md.onSurfaceVariant, mb: 3, fontSize: 14, lineHeight: 1.6 }}>
          {t('error_boundary.body')}
        </Typography>
        <Button variant="contained" size="large" startIcon={<RefreshIcon />} onClick={onReload}>
          {t('error_boundary.refresh')}
        </Button>
        {error.message && (
          <Box sx={{
            mt: 3, p: 1.5,
            borderRadius: 2,
            bgcolor: md.surfaceContainerHighest,
            textAlign: 'left',
          }}>
            <Typography sx={{
              fontSize: 11, color: md.onSurfaceVariant,
              fontFamily: 'monospace', wordBreak: 'break-word',
              whiteSpace: 'pre-wrap',
            }}>
              {error.message}
            </Typography>
          </Box>
        )}
      </Card>
    </Box>
  )
}
