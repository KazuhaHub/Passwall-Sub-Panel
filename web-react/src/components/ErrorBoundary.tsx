import { Component, useState, type ErrorInfo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { IconChevronDown, IconCopy, IconRefresh, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'

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
// toast layer, not here.
//
// NOTE: this React boundary does NOT catch errors thrown inside routed
// components — react-router's createBrowserRouter intercepts those at the
// route level and renders the route's `errorElement`. The router wires its
// own errorElement to <RouteError/>, which reuses ErrorFallback below, so
// page-level crashes get the same UI as this boundary.
//
// Boundaries MUST be class components because the hook surface (which
// can read context, translate strings, etc.) is unavailable inside
// getDerivedStateFromError / componentDidCatch. The render path hands off
// to a function component as soon as the error is captured.
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
//
// It must work when whatever broke is a provider (query client, router, a store):
// so it takes nothing from context. Only the global i18n instance (useTranslation
// without a provider reads it) and the CSS variables on <html>, which are plain
// CSS and exist before React mounts.
export function ErrorFallback({ error, onReload, componentStack }: {
  error: Error
  onReload: () => void
  componentStack?: string
}) {
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
    <div role="alert" className="fixed inset-0 grid place-items-center overflow-auto bg-background p-6 text-foreground">
      <div className="w-full max-w-xl rounded-xl bg-card p-6 text-center ring-1 ring-foreground/10 sm:p-8">
        <IconWarning aria-hidden className="mx-auto mb-4 size-12 text-destructive" />
        <h1 className="mb-3 text-xl font-medium">{t('error_boundary.title')}</h1>
        <p className="mb-6 text-sm/relaxed text-muted-foreground">{t('error_boundary.body')}</p>
        <Button size="lg" onClick={onReload}>
          <IconRefresh />
          {t('error_boundary.refresh')}
        </Button>

        {detail && (
          <div className="mt-6 text-left">
            <Button variant="ghost" size="sm" aria-expanded={showDetails} onClick={() => setShowDetails(v => !v)}>
              {t('error_boundary.details')}
              <IconChevronDown className={cn('transition-transform', showDetails && 'rotate-180')} />
            </Button>
            {showDetails && (
              <div className="relative mt-2 max-h-72 overflow-auto rounded-lg bg-muted p-3">
                <Button variant="ghost" size="xs" className="absolute top-1 right-1" onClick={copyDetail}>
                  <IconCopy />
                  {copied ? t('error_boundary.copied') : t('error_boundary.copy')}
                </Button>
                <pre className="m-0 font-mono text-[11px] break-words whitespace-pre-wrap text-muted-foreground">{detail}</pre>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
