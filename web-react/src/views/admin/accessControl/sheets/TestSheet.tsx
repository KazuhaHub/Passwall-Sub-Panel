import { useState, type FormEvent } from 'react'
import {
  Alert, Autocomplete, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Skeleton, TextField, ToggleButton,
  ToggleButtonGroup, Typography, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestTestResult } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import UserAutocomplete from '@/components/UserAutocomplete'
import { nodeStateText } from '@/components/NodePolicyStatusRow'
import type { DrawerPrefill } from '@/hooks/useDrawerParam'
import { useAddDestException, useDestStatus, usePublishDest, useTestDestination } from '@/query/accessControl'
import { useNodesList } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError } from '../destErrors'
import EvalTrace, { terminatingStep, VerdictLine } from './EvalTrace'
import SheetDrawer from './SheetDrawer'
import { normalizeTarget, validIp, validPort } from './testInput'

export interface TestSheetProps {
  open: boolean
  onClose: () => void
  /** From history state: a destination, a port, an account. Never from the URL. */
  prefill?: DrawerPrefill
  /** Closes the sheet, switches to the policies tab, highlights and opens that policy. */
  onOpenPolicy: (policyId: number) => void
}

const T = 'admin:access_control.test.'

/** S13: what the published policy does with one connection, step by step. Nothing typed here reaches the URL. */
export default function TestSheet({ open, onClose, prefill, onOpenPolicy }: TestSheetProps) {
  const { t } = useTranslation(['admin'])
  return (
    <SheetDrawer open={open} onClose={onClose} title={t(`${T}title`)} headingId="test-heading">
      <TestBody prefill={prefill} onOpenPolicy={onOpenPolicy} />
    </SheetDrawer>
  )
}

function TestBody({ prefill, onOpenPolicy }: { prefill?: DrawerPrefill; onOpenPolicy: (id: number) => void }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const test = useTestDestination(scope)
  const publish = usePublishDest(scope)
  const status = useDestStatus(scope)
  const nodes = useNodesList(scope, { page: 1, page_size: 200 })

  const [target, setTarget] = useState(prefill?.target ?? '')
  const [port, setPort] = useState(prefill?.port ? String(prefill.port) : '')
  const [network, setNetwork] = useState<'tcp' | 'udp'>(prefill?.network === 'udp' ? 'udp' : 'tcp')
  const [userId, setUserId] = useState<number | null>(prefill?.userId ?? null)
  const [inbound, setInbound] = useState<number | null>(null)
  const [sourceIp, setSourceIp] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [submitted, setSubmitted] = useState(false)
  const [result, setResult] = useState<{ r: DestTestResult; host: string } | null>(null)
  const [failed, setFailed] = useState(false)
  const [exception, setException] = useState(false)

  const norm = normalizeTarget(target)
  const targetBad = submitted && norm.host === ''
  const portBad = !validPort(port)
  const ipBad = !validIp(sourceIp)

  const run = async (e?: FormEvent) => {
    e?.preventDefault()
    setSubmitted(true)
    setFieldError(null)
    if (norm.host === '' || portBad || ipBad) return
    // A URL pasted whole: the host is what is tested, and a port in it fills the port field.
    const usePort = port.trim() !== '' ? Number(port) : norm.port ?? undefined
    if (norm.port && port.trim() === '') setPort(String(norm.port))
    setFailed(false)
    try {
      const r = await test.mutateAsync({
        target: norm.host, port: usePort, network, user_id: userId ?? undefined,
        inbound_node_id: inbound ?? undefined, source_ip: sourceIp.trim() || undefined,
      })
      setResult({ r, host: norm.host })
    } catch (err) {
      const d = destErrorOf(err)
      if (d?.code === 'dest_test_invalid' && d.field) setFieldError(d.field)
      else setFailed(true)
    }
  }

  const doPublish = async () => {
    try { await publish.mutateAsync() } catch (err) { reportDestError(t, err) }
  }

  const options = nodes.data?.items ?? []
  const hit = result ? terminatingStep(result.r) : undefined
  const isIp = result ? /^[0-9.]+$/.test(result.host) || result.host.includes(':') : false

  return (
    <Box component="form" onSubmit={run} noValidate>
      <Box sx={{ display: 'grid', gap: 1.5 }}>
        <Box sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: 'minmax(0, 1fr) 110px' } }}>
          <TextField label={t(`${T}target`)} value={target} onChange={e => setTarget(e.target.value)} required size="small"
            error={targetBad || fieldError === 'target'}
            helperText={targetBad ? t(`${T}target_required`) : norm.stripped ? t(`${T}host_only`) : undefined}
            slotProps={{ htmlInput: { style: { fontFamily: 'ui-monospace, monospace', fontSize: 13 } } }} />
          <TextField label={t(`${T}port`)} value={port} onChange={e => setPort(e.target.value)} size="small" error={portBad || fieldError === 'port'}
            helperText={portBad ? t(`${T}port_bad`) : undefined} slotProps={{ htmlInput: { inputMode: 'numeric' } }} />
        </Box>
        <ToggleButtonGroup exclusive size="small" value={network} onChange={(_, v: 'tcp' | 'udp' | null) => { if (v) setNetwork(v) }} aria-label={t(`${T}network`)}>
          <ToggleButton value="tcp">TCP</ToggleButton>
          <ToggleButton value="udp">UDP</ToggleButton>
        </ToggleButtonGroup>
        <UserAutocomplete value={userId} onChange={setUserId} label={t(`${T}account`)} fullWidth />
        <Autocomplete size="small" options={options} loading={nodes.isFetching}
          value={options.find(n => n.id === inbound) ?? null} onChange={(_, v) => setInbound(v?.id ?? null)}
          getOptionLabel={n => n.display_name || n.panel_name} isOptionEqualToValue={(a, b) => a.id === b.id}
          renderInput={p => <TextField {...p} label={t(`${T}inbound`)} />} />
        <TextField label={t(`${T}source_ip`)} value={sourceIp} onChange={e => setSourceIp(e.target.value)} size="small"
          error={ipBad || fieldError === 'source_ip'} helperText={ipBad ? t(`${T}source_ip_bad`) : t(`${T}source_ip_hint`)} />
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <AsyncButton type="submit" variant="contained" onClick={run}>{t(`${T}run`)}</AsyncButton>
        </Box>
      </Box>

      <Box sx={{ mt: 2, pt: 2, borderTop: `1px solid ${md.outlineVariant}` }} aria-live="polite">
        {test.isPending && <Skeleton variant="rounded" height={120} />}
        {failed && !test.isPending && (
          <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void run()}>{t('admin:access_control.retry')}</Button>}>
            {t(`${T}failed`)}
          </Alert>
        )}
        {!result && !failed && !test.isPending && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${T}initial`)}</Typography>}
        {result && !test.isPending && (
          <Box sx={{ display: 'grid', gap: 1.5 }}>
            {result.r.unpublished && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', p: 1, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
                <Typography variant="body2" sx={{ flex: 1, color: md.onSurface }}>{t(`${T}unpublished`)}</Typography>
                <AsyncButton size="small" onClick={doPublish}>{t('admin:access_control.verdict.action.publish')}</AsyncButton>
              </Box>
            )}
            <VerdictLine result={result.r} />
            <EvalTrace result={result.r} />
            <Box sx={{ display: 'grid', gap: 0.5 }}>
              {result.r.nodes.map(n => {
                const st = status.data?.nodes.find(x => x.panel_id === n.panel_id)
                const text = st ? nodeStateText(t, st, Date.now()) : null
                return text && text.key !== 'applied' && text.key !== 'applied_exec_only'
                  ? <Typography key={n.panel_id} variant="body2">{t(`${T}node_hint`, { node: n.name, note: text.note })}</Typography>
                  : <Typography key={n.panel_id} variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${T}node_applied`, { node: n.name })}</Typography>
              })}
              <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${T}note_published`)}</Typography>
              <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${T}note_no_dns`)}</Typography>
              {isIp && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${T}note_ip`)}</Typography>}
              {result.r.notes.includes('hostname_skips_ip_rules') && <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${T}note_hostname_skips_ip`)}</Typography>}
              {result.r.conditional && <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${T}note_conditional`)}</Typography>}
            </Box>
            <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', justifyContent: 'space-between' }}>
              {result.r.verdict === 'block' && <Button size="small" onClick={() => setException(true)}>{t(`${T}add_exception`)}</Button>}
              {hit?.policy_id ? (
                <Button size="small" onClick={() => onOpenPolicy(hit.policy_id!)}>{t(`${T}open_policy`, { name: hit.name || t(`${T}deleted_policy`) })}</Button>
              ) : null}
            </Box>
          </Box>
        )}
      </Box>
      <ExceptionDialog open={exception} host={result?.host ?? ''} onClose={() => setException(false)} />
    </Box>
  )
}

/** A global allow exception for the tested destination: the site (and its subdomains) or just that host. */
function ExceptionDialog({ open, host, onClose }: { open: boolean; host: string; onClose: () => void }) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const add = useAddDestException(scope)
  const [entry, setEntry] = useState(host)
  const [match, setMatch] = useState<'site' | 'host'>('site')
  const [error, setError] = useState(false)
  const [seen, setSeen] = useState(host)
  if (open && seen !== host) { setSeen(host); setEntry(host) }

  const save = async () => {
    setError(false)
    try {
      const r = await add.mutateAsync({ target: entry.trim(), match })
      pushSnack(t(`${T}exception_saved`, { entry: r.entry }), 'success')
      onClose()
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_exception_invalid') setError(true)
      else reportDestError(t, err)
    }
  }
  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t(`${T}exception.title`)}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 2, pt: '8px !important' }}>
        <Typography variant="body2">{t(`${T}exception.lead`)}</Typography>
        <TextField label={t(`${T}target`)} value={entry} onChange={e => setEntry(e.target.value)} size="small" error={error}
          helperText={error ? t(`${T}exception.invalid`) : undefined} />
        <ToggleButtonGroup exclusive size="small" value={match} onChange={(_, v: 'site' | 'host' | null) => { if (v) setMatch(v) }} aria-label={t(`${T}exception.match`)}>
          <ToggleButton value="site">{t(`${T}exception.site`)}</ToggleButton>
          <ToggleButton value="host">{t(`${T}exception.host`)}</ToggleButton>
        </ToggleButtonGroup>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t(`${T}exception.cancel`)}</Button>
        <AsyncButton variant="contained" onClick={save}>{t(`${T}exception.save`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
