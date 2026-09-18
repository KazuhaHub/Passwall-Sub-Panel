import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Alert, Box, Button, Chip, CircularProgress, Dialog, DialogActions, DialogContent,
  DialogTitle, Divider, FormControlLabel, Checkbox, Stack, Table, TableBody, TableCell,
  TableHead, TableRow, Typography,
} from '@mui/material'

import {
  NODE_DIAGNOSTIC_SECTIONS, getNodeDiagnostic, isTerminalNodeDiagnostic, requestNodeDiagnostic,
  type NodeDiagnostic, type NodeDiagnosticSection,
} from '@/api/nodeDiagnostics'
import type { Server } from '@/api/servers'

/**
 * Administrator-requested remote diagnostics.
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
const SEVERITY_CHIP: Record<Severity, 'default' | 'warning' | 'error'> = {
  info: 'default', warning: 'warning', error: 'error',
}

export default function NodeDiagnosticsDialog({ server, open, onClose }: {
  server: Server | null
  open: boolean
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
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
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="md">
      <DialogTitle>{t('admin:nodeDiagnostics.title', { name: server?.name ?? '' })}</DialogTitle>
      <DialogContent dividers>
        <Stack spacing={2}>
          <Box>
            <Typography variant="subtitle2" gutterBottom>{t('admin:nodeDiagnostics.sections')}</Typography>
            {/* Stack takes layout through sx in MUI v9: flexWrap and
                alignItems are not top-level props. */}
            <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>
              {NODE_DIAGNOSTIC_SECTIONS.map(section => (
                <FormControlLabel
                  key={section}
                  control={<Checkbox
                    size="small"
                    disabled={diagnostic !== null}
                    checked={sections.includes(section)}
                    onChange={event => setSections(current =>
                      event.target.checked ? [...current, section] : current.filter(s => s !== section))}
                  />}
                  label={t(`admin:nodeDiagnostics.section.${section}`)}
                />
              ))}
            </Stack>
          </Box>

          {diagnostic && (
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
              <Chip
                size="small"
                color={diagnostic.status === 'succeeded' ? 'success'
                  : diagnostic.status === 'failed' ? 'error'
                    : diagnostic.status === 'indeterminate' ? 'warning' : 'default'}
                label={t(`admin:nodeDiagnostics.status.${diagnostic.status}`)}
              />
              {/* The sections actually being collected, which can differ from
                  what was asked: the panel merges to one active collection. */}
              {diagnostic.sections.map(section => (
                <Chip key={section} size="small" variant="outlined"
                  label={t(`admin:nodeDiagnostics.section.${section}`)} />
              ))}
              {diagnostic.truncated && (
                <Chip size="small" color="warning" label={t('admin:nodeDiagnostics.truncated')} />
              )}
              {diagnostic.recovered && (
                <Chip size="small" variant="outlined" label={t('admin:nodeDiagnostics.recovered')} />
              )}
            </Stack>
          )}

          {waiting && (
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <CircularProgress size={16} />
              <Typography variant="body2">{t('admin:nodeDiagnostics.waiting')}</Typography>
            </Stack>
          )}

          {error && <Alert severity="error">{error}</Alert>}

          {diagnostic?.status === 'failed' && (
            <Alert severity="error">{t('admin:nodeDiagnostics.failed')}</Alert>
          )}
          {diagnostic?.status === 'indeterminate' && (
            <Alert severity="warning">{t('admin:nodeDiagnostics.indeterminate')}</Alert>
          )}

          {result && (
            <>
              <Divider />
              <Typography variant="subtitle2">{t('admin:nodeDiagnostics.checks')}</Typography>
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell>{t('admin:nodeDiagnostics.check.code')}</TableCell>
                    <TableCell>{t('admin:nodeDiagnostics.check.status')}</TableCell>
                    <TableCell>{t('admin:nodeDiagnostics.check.summary')}</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {result.checks.map(check => (
                    <TableRow key={check.code}>
                      <TableCell>{check.code}</TableCell>
                      <TableCell>{t(`admin:nodeDiagnostics.checkStatus.${check.status}`)}</TableCell>
                      <TableCell>{check.summary}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              {/* Absent sections are absent: no empty box, no zero. */}
              {result.state && (
                <Typography variant="body2">
                  {t('admin:nodeDiagnostics.state', {
                    quickCheck: result.state.sqlite_quick_check,
                    outbox: result.state.outbox_pending,
                    tasks: result.state.tasks_queued,
                  })}
                </Typography>
              )}
              {result.runtime && (
                <Typography variant="body2">
                  {t('admin:nodeDiagnostics.runtime', {
                    core: result.runtime.core_state,
                    digest: result.runtime.core_config_digest,
                  })}
                </Typography>
              )}

              {result.events && (
                <>
                  <Typography variant="subtitle2">{t('admin:nodeDiagnostics.events')}</Typography>
                  {result.events.length === 0
                    ? <Typography variant="body2">{t('admin:nodeDiagnostics.noEvents')}</Typography>
                    : (
                      <Table size="small">
                        <TableBody>
                          {result.events.map((event, index) => (
                            <TableRow key={`${event.code}-${event.at_ms}-${index}`}>
                              <TableCell>{event.code}</TableCell>
                              <TableCell>
                                <Chip size="small" color={SEVERITY_CHIP[event.severity]}
                                  label={t(`admin:nodeDiagnostics.severity.${event.severity}`)} />
                              </TableCell>
                              <TableCell>{new Date(event.at_ms).toLocaleString()}</TableCell>
                              <TableCell>{event.summary}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    )}
                </>
              )}
            </>
          )}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:actions.close')}</Button>
        <Button
          variant="contained"
          disabled={busy || sections.length === 0 || (waiting && diagnostic !== null)}
          onClick={request}
        >
          {t('admin:nodeDiagnostics.collect')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
