import { useCallback, useEffect, useRef, useState } from 'react'
import { IconSpinner } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Checkbox } from '@/components/ui/checkbox'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  NODE_DIAGNOSTIC_SECTIONS, getNodeDiagnostic, isTerminalNodeDiagnostic, requestNodeDiagnostic,
  type NodeDiagnostic, type NodeDiagnosticSection,
} from '@/api/nodeDiagnostics'
import type { Server } from '@/api/servers'
import { Notice } from '../../Notice'
import { useAdminT } from '../../useAdminT'

/**
 * Administrator-requested remote diagnostics, in a sheet over the servers page.
 *
 * THE LIFECYCLE IS THE FIRST THING SHOWN, because a diagnostic is asynchronous:
 * queued and offered mean the node has not answered yet, and a component that
 * rendered an empty result table for them would be reporting "the machine is
 * clean" about a machine nobody has looked at.
 *
 * A SECTION NOBODY ASKED FOR IS NOT RENDERED AT ALL. The node omits it, and
 * drawing an empty box for it would say the panel looked and found nothing.
 */

const POLL_INTERVAL_MS = 3000

type Severity = 'info' | 'warning' | 'error'
const SEVERITY_TONE: Record<Severity, Tone> = { info: 'neutral', warning: 'warning', error: 'danger' }
const CHECK_TONE: Record<string, Tone> = { ok: 'success', warning: 'warning', failed: 'danger', unavailable: 'neutral' }
const STATUS_TONE: Record<string, Tone> = { succeeded: 'success', failed: 'danger', indeterminate: 'warning' }

export default function NodeDiagnosticsSheet({ server, open, onClose }: {
  server: Server | null
  open: boolean
  onClose: () => void
}) {
  const t = useAdminT()
  const [sections, setSections] = useState<NodeDiagnosticSection[]>(['host', 'runtime', 'state'])
  const [diagnostic, setDiagnostic] = useState<NodeDiagnostic | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const serverID = server?.id ?? 0
  // The id is kept in a ref as well: the poll closure must follow the task it
  // started, not whatever state the next render happens to hold.
  const taskID = useRef<string | null>(null)

  const request = useCallback(async () => {
    if (!serverID) return
    setBusy(true)
    setError(null)
    try {
      const body = await requestNodeDiagnostic(serverID, { sections, max_events: 0 })
      taskID.current = body.task_id
      setDiagnostic(body)
    } catch {
      setError(t('admin:nodeDiagnostics.requestFailed'))
    } finally {
      setBusy(false)
    }
  }, [serverID, sections, t])

  // Poll only while the task can still change, and stop the moment it cannot.
  useEffect(() => {
    if (!open || !diagnostic || isTerminalNodeDiagnostic(diagnostic.status)) return
    const current = diagnostic.task_id
    const timer = setInterval(async () => {
      try {
        setDiagnostic(await getNodeDiagnostic(serverID, current))
      } catch {
        setError(t('admin:nodeDiagnostics.requestFailed'))
      }
    }, POLL_INTERVAL_MS)
    return () => clearInterval(timer)
  }, [open, diagnostic, serverID, t])

  useEffect(() => {
    if (!open) {
      setDiagnostic(null)
      setError(null)
      taskID.current = null
    }
  }, [open])

  const result = diagnostic?.result
  const waiting = diagnostic !== null && !isTerminalNodeDiagnostic(diagnostic.status)

  return (
    <DetailSheet
      open={open}
      onClose={onClose}
      title={t('admin:nodeDiagnostics.title', { name: server?.name ?? '' })}
      status={diagnostic && <StatusBadge tone={STATUS_TONE[diagnostic.status] ?? 'neutral'} label={t(`admin:nodeDiagnostics.status.${diagnostic.status}`)} />}
      footer={(
        // The POST alone can take a few seconds, and a button that is only disabled for that wait
        // is indistinguishable from one that silently ate the click: AsyncButton shows its own busy
        // state, driven by `busy` as well so a request started elsewhere in this component counts.
        <AsyncButton pending={busy} disabled={sections.length === 0 || waiting} onClick={request}>
          {t('admin:nodeDiagnostics.collect')}
        </AsyncButton>
      )}
      className="data-[side=right]:sm:w-[min(48rem,96vw)] data-[side=right]:sm:max-w-[min(48rem,96vw)]"
    >
      <div className="flex flex-col gap-4">
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-sm font-medium">{t('admin:nodeDiagnostics.sections')}</legend>
          <div className="flex flex-wrap gap-x-5 gap-y-2">
            {NODE_DIAGNOSTIC_SECTIONS.map(section => (
              <label key={section} className="flex min-h-8 cursor-pointer items-center gap-2 text-sm">
                <Checkbox
                  disabled={waiting || busy}
                  checked={sections.includes(section)}
                  onCheckedChange={checked =>
                    setSections(current => checked ? [...current, section] : current.filter(s => s !== section))}
                />
                {t(`admin:nodeDiagnostics.section.${section}`)}
              </label>
            ))}
          </div>
        </fieldset>

        {diagnostic && (
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            {/* The sections actually being collected, which can differ from
                what was asked: the panel merges to one active collection. */}
            {diagnostic.sections.map(section => (
              <span key={section} className="rounded-md border px-1.5 py-0.5">{t(`admin:nodeDiagnostics.section.${section}`)}</span>
            ))}
            {diagnostic.truncated && <StatusBadge tone="warning" label={t('admin:nodeDiagnostics.truncated')} />}
            {diagnostic.recovered && <span>{t('admin:nodeDiagnostics.recovered')}</span>}
          </div>
        )}

        {waiting && (
          <div role="status" className="flex items-center gap-2 text-sm">
            <IconSpinner aria-hidden className="size-4 animate-spin" />
            {t('admin:nodeDiagnostics.waiting')}
          </div>
        )}

        {error && <Notice tone="danger" role="alert">{error}</Notice>}
        {diagnostic?.status === 'failed' && <Notice tone="danger" role="alert">{t('admin:nodeDiagnostics.failed')}</Notice>}
        {diagnostic?.status === 'indeterminate' && <Notice>{t('admin:nodeDiagnostics.indeterminate')}</Notice>}

        {result && (
          <>
            <section className="flex flex-col gap-2">
              <h3 className="text-sm font-medium">{t('admin:nodeDiagnostics.checks')}</h3>
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('admin:nodeDiagnostics.check.code')}</TableHead>
                      <TableHead>{t('admin:nodeDiagnostics.check.status')}</TableHead>
                      <TableHead>{t('admin:nodeDiagnostics.check.summary')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {result.checks.map(check => (
                      <TableRow key={check.code}>
                        <TableCell className="font-mono text-xs">{check.code}</TableCell>
                        <TableCell><StatusBadge tone={CHECK_TONE[check.status] ?? 'neutral'} label={t(`admin:nodeDiagnostics.checkStatus.${check.status}`)} /></TableCell>
                        <TableCell className="break-words whitespace-normal">{check.summary}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>

            {/* Absent sections are absent: no empty box, no zero. */}
            {result.state && (
              <p className="text-sm">
                {t('admin:nodeDiagnostics.state', {
                  quickCheck: result.state.sqlite_quick_check,
                  outbox: result.state.outbox_pending,
                  tasks: result.state.tasks_queued,
                })}
              </p>
            )}
            {result.runtime && (
              <p className="text-sm break-words">
                {t('admin:nodeDiagnostics.runtime', {
                  core: result.runtime.core_state,
                  digest: result.runtime.core_config_digest,
                })}
              </p>
            )}

            {result.events && (
              <section className="flex flex-col gap-2">
                <h3 className="text-sm font-medium">{t('admin:nodeDiagnostics.events')}</h3>
                {result.events.length === 0
                  ? <p className="text-sm text-muted-foreground">{t('admin:nodeDiagnostics.noEvents')}</p>
                  : (
                    <div className="rounded-lg border">
                      <Table>
                        <TableBody>
                          {result.events.map((event, index) => (
                            <TableRow key={`${event.code}-${event.at_ms}-${index}`}>
                              <TableCell className="font-mono text-xs">{event.code}</TableCell>
                              <TableCell><StatusBadge tone={SEVERITY_TONE[event.severity]} label={t(`admin:nodeDiagnostics.severity.${event.severity}`)} /></TableCell>
                              <TableCell className="tabular-nums">{new Date(event.at_ms).toLocaleString()}</TableCell>
                              <TableCell className="break-words whitespace-normal">{event.summary}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  )}
              </section>
            )}
          </>
        )}
      </div>
    </DetailSheet>
  )
}
