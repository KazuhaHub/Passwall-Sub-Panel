import { Component, useState, type ErrorInfo, type ReactNode } from 'react'
import { Box, Button, Card, Collapse, Typography, useTheme } from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import ReportProblemOutlinedIcon from '@mui/icons-material/ReportProblemOutlined'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'

interface State {
  error: Error | null
  componentStack: string | null
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
// NOTE: this React boundary does NOT catch errors thrown inside routed
// components — react-router's createBrowserRouter intercepts those at the
// route level and renders the route's `errorElement`. The router wires its
// own errorElement to <RouteError/>, which reuses ErrorFallback below, so
// page-level crashes get the same UI as this boundary.
//
// Boundaries MUST be class components because the hook surface (which
// can read context, translate strings, theme, etc.) is unavailable
// inside getDerivedStateFromError / componentDidCatch. The render path
// hands off to a function component as soon as the error is captured.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, componentStack: null }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ componentStack: info.componentStack ?? null })
    // Stash to console with the component stack so developers debugging
    // a customer report can copy the trace out of the browser.
    // eslint-disable-next-line no-console
    console.error('Unhandled render error', error, info.componentStack)
  }

  handleReload = () => {
    // Full reload rather than setState({error: null}) — the broken
    // component might have torn state we can't recover from in-process.
    window.location.reload()
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <ErrorFallback
        error={this.state.error}
        onReload={this.handleReload}
        componentStack={this.state.componentStack ?? undefined}
      />
    )
  }
}

// ErrorFallback is the shared crash UI, reused by both this React boundary
// and the router's <RouteError/>. The "view details" section is kept even in
// production: this is a self-hosted panel for a small group, so being able to
// ask a friend to expand + copy the trace beats hiding it.
export function ErrorFallback({ error, onReload, componentStack }: {
  error: Error
  onReload: () => void
  componentStack?: string
}) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('common')
  const [showDetails, setShowDetails] = useState(false)
  const [copied, setCopied] = useState(false)

  const detail = [error.message, error.stack, componentStack].filter(Boolean).join('\n\n')

  async function copyDetail() {
    try {
      await navigator.clipboard.writeText(detail)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked (insecure context / denied) — no-op */
    }
  }

  return (
    <Box sx={{
      position: 'fixed', inset: 0,
      display: 'grid', placeItems: 'center',
      bgcolor: md.surface, p: 3, overflow: 'auto',
    }}>
      <Card sx={{
        maxWidth: 560, width: '100%',
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

        {detail && (
          <Box sx={{ mt: 3, textAlign: 'left' }}>
            <Button
              size="small"
              variant="text"
              onClick={() => setShowDetails(v => !v)}
              endIcon={<ExpandMoreIcon sx={{ transform: showDetails ? 'rotate(180deg)' : 'none', transition: 'transform .2s' }} />}
              sx={{ color: md.onSurfaceVariant }}>
              {t('error_boundary.details', { defaultValue: '查看详情' })}
            </Button>
            <Collapse in={showDetails}>
              <Box sx={{
                mt: 1, p: 1.5,
                borderRadius: 2,
                bgcolor: md.surfaceContainerHighest,
                position: 'relative',
                maxHeight: 280, overflow: 'auto',
              }}>
                <Button
                  size="small"
                  startIcon={<ContentCopyIcon sx={{ fontSize: 14 }} />}
                  onClick={copyDetail}
                  sx={{ position: 'absolute', top: 4, right: 4, fontSize: 11, minWidth: 0 }}>
                  {copied ? t('error_boundary.copied', { defaultValue: '已复制' }) : t('error_boundary.copy', { defaultValue: '复制' })}
                </Button>
                <Typography component="pre" sx={{
                  m: 0, fontSize: 11, color: md.onSurfaceVariant,
                  fontFamily: 'monospace', wordBreak: 'break-word',
                  whiteSpace: 'pre-wrap',
                }}>
                  {detail}
                </Typography>
              </Box>
            </Collapse>
          </Box>
        )}
      </Card>
    </Box>
  )
}
