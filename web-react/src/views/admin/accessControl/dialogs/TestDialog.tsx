import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestTestResult } from '@/api/accessControl'
import { ModeRadioGroup } from '@/components/access/ModeRadioGroup'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { nodeStateText } from '@/components/NodePolicyStatusRow'
import { OptionCombobox } from '@/components/OptionCombobox'
import { DetailDialog } from '@/components/data/DetailDialog'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertAction } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import UserAutocomplete from '@/components/UserAutocomplete'
import type { DrawerPrefill } from '@/hooks/useDrawerParam'
import { useAddDestException, useDestStatus, usePublishDest, useTestDestination } from '@/query/accessControl'
import { useNodesList } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError } from '../destErrors'
import EvalTrace, { terminatingStep, VerdictLine } from './EvalTrace'
import { normalizeTarget, parseResolvedIps, resolvedIpsPayload, validIp, validPort } from './testInput'

export interface TestDialogProps {
  open: boolean
  onClose: () => void
  /** From history state: a destination, a port, an account. Never from the URL. */
  prefill?: DrawerPrefill
  /** Closes the dialog, switches to the policies tab, highlights and opens that policy. */
  onOpenPolicy: (policyId: number) => void
}

const T = 'admin:access_control.test.'

/** S13: what the published policy does with one connection, step by step. Nothing typed here reaches the URL. */
export default function TestDialog({ open, onClose, prefill, onOpenPolicy }: TestDialogProps) {
  const { t } = useTranslation(['admin'])
  return (
    <DetailDialog open={open} onClose={onClose} size="lg" title={t(`${T}title`)}>
      <TestBody prefill={prefill} onOpenPolicy={onOpenPolicy} />
    </DetailDialog>
  )
}

function TestBody({ prefill, onOpenPolicy }: { prefill?: DrawerPrefill; onOpenPolicy: (id: number) => void }) {
  const { t } = useTranslation(['admin'])
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
  const [resolvedIps, setResolvedIps] = useState('')
  const [lookupFailed, setLookupFailed] = useState(false)
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [submitted, setSubmitted] = useState(false)
  const [result, setResult] = useState<{ r: DestTestResult; host: string } | null>(null)
  const [failed, setFailed] = useState(false)
  const [exception, setException] = useState(false)

  const inboundName = (id: number) => { const n = nodes.data?.items.find(x => x.id === id); return n ? (n.display_name || n.panel_name) : undefined }

  const norm = normalizeTarget(target)
  const targetBad = submitted && norm.host === ''
  const portBad = !validPort(port)
  const ipBad = !validIp(sourceIp)
  const resolved = lookupFailed ? { ips: [], bad: false } : parseResolvedIps(resolvedIps)

  const run = async (e?: FormEvent) => {
    e?.preventDefault()
    setSubmitted(true)
    setFieldError(null)
    if (norm.host === '' || portBad || ipBad || resolved.bad) return
    // A URL pasted whole: the host is what is tested, and a port in it fills the port field.
    const usePort = port.trim() !== '' ? Number(port) : norm.port ?? undefined
    if (norm.port && port.trim() === '') setPort(String(norm.port))
    setFailed(false)
    try {
      const r = await test.mutateAsync({
        target: norm.host, port: usePort, network, user_id: userId ?? undefined,
        inbound_node_id: inbound ?? undefined, source_ip: sourceIp.trim() || undefined,
        ...(() => { const ips = resolvedIpsPayload(resolvedIps, lookupFailed); return ips ? { resolved_ips: ips } : {} })(),
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

  const options = (nodes.data?.items ?? []).map(n => ({ value: n.id, label: n.display_name || n.panel_name }))
  const hit = result ? terminatingStep(result.r) : undefined
  const isIp = result ? /^[0-9.]+$/.test(result.host) || result.host.includes(':') : false
  const mono = 'font-mono text-[13px] md:text-[13px]'

  return (
    <form onSubmit={run} noValidate className="grid gap-4">
      <div className="grid gap-3">
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_7rem]">
          <Field label={t(`${T}target`)} required
            error={targetBad ? t(`${T}target_required`) : fieldError === 'target' ? t(`${T}target_required`) : undefined}
            hint={norm.stripped ? t(`${T}host_only`) : undefined}>
            <Input value={target} onChange={e => setTarget(e.target.value)} className={mono} />
          </Field>
          <Field label={t(`${T}port`)} error={portBad ? t(`${T}port_bad`) : fieldError === 'port' ? t(`${T}port_bad`) : undefined}>
            <Input value={port} onChange={e => setPort(e.target.value)} inputMode="numeric" />
          </Field>
        </div>
        <Field label={t(`${T}network`)}>
          {p => <SegmentedControl<'tcp' | 'udp'> value={network} onChange={setNetwork} aria-labelledby={p.labelId}
            options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />}
        </Field>
        <Field label={t(`${T}account`)}>
          {() => <UserAutocomplete value={userId} onChange={setUserId} label={t(`${T}account`)} fullWidth />}
        </Field>
        <Field label={t(`${T}inbound`)}>
          <OptionCombobox options={options} value={inbound} onChange={setInbound} emptyText={t('admin:access_control.user_picker.no_match')} />
        </Field>
        <Field label={t(`${T}source_ip`)} error={ipBad ? t(`${T}source_ip_bad`) : fieldError === 'source_ip' ? t(`${T}source_ip_bad`) : undefined}
          hint={t(`${T}source_ip_hint`)}>
          <Input value={sourceIp} onChange={e => setSourceIp(e.target.value)} />
        </Field>
        <Field label={t(`${T}resolved_ips`)} error={resolved.bad ? t(`${T}resolved_ips_bad`) : fieldError === 'resolved_ips' ? t(`${T}resolved_ips_bad`) : undefined}
          hint={t(`${T}resolved_ips_hint`)}>
          <Input value={resolvedIps} onChange={e => setResolvedIps(e.target.value)} disabled={lookupFailed} className={mono} />
        </Field>
        <Field label={t(`${T}resolved_failed`)} orientation="horizontal">
          <Checkbox checked={lookupFailed} onCheckedChange={c => { setLookupFailed(c); if (c) setResolvedIps('') }} />
        </Field>
        <div className="flex justify-end">
          <AsyncButton type="submit" pending={test.isPending}>{t(`${T}run`)}</AsyncButton>
        </div>
      </div>

      <div className="border-t pt-4" aria-live="polite">
        {test.isPending && <Skeleton className="h-28 w-full" />}
        {failed && !test.isPending && (
          <Alert variant="destructive">
            {t(`${T}failed`)}
            <AlertAction><Button size="xs" variant="outline" onClick={() => void run()}>{t('admin:access_control.retry')}</Button></AlertAction>
          </Alert>
        )}
        {!result && !failed && !test.isPending && <p className="text-sm text-muted-foreground">{t(`${T}initial`)}</p>}
        {result && !test.isPending && (
          <div className="grid gap-3">
            {result.r.unpublished && (
              <div className="flex flex-wrap items-center gap-2 rounded-lg bg-muted p-2">
                <p className="flex-1 text-sm">{t(`${T}unpublished`)}</p>
                <AsyncButton size="sm" variant="outline" onClick={doPublish}>{t('admin:access_control.verdict.action.publish')}</AsyncButton>
              </div>
            )}
            <VerdictLine result={result.r} />
            <EvalTrace result={result.r} inboundName={inboundName} />
            <div className="grid gap-1 text-sm">
              {result.r.nodes.map(n => {
                const st = status.data?.nodes.find(x => x.panel_id === n.panel_id)
                const text = st ? nodeStateText(t, st, Date.now()) : null
                return text && text.key !== 'applied' && text.key !== 'applied_exec_only'
                  ? <p key={n.panel_id}>{t(`${T}node_hint`, { node: n.name, note: text.note })}</p>
                  : <p key={n.panel_id} className="text-muted-foreground">{t(`${T}node_applied`, { node: n.name })}</p>
              })}
              <p className="text-muted-foreground">{t(`${T}note_published`)}</p>
              <p className="text-muted-foreground">{t(`${T}note_no_dns`)}</p>
              {isIp && <p className="text-muted-foreground">{t(`${T}note_ip`)}</p>}
              {result.r.notes.includes('hostname_skips_ip_rules') && <p>{t(`${T}note_hostname_skips_ip`)}</p>}
              {result.r.notes.includes('hostname_needs_resolution') && <p>{t(`${T}note_hostname_needs_resolution`)}</p>}
              {result.r.notes.includes('resolution_failed_engine_dependent') && <p>{t(`${T}note_resolution_failed`)}</p>}
              {result.r.conditional && <p>{t(`${T}note_conditional`)}</p>}
            </div>
            <div className="flex flex-wrap justify-between gap-2">
              {result.r.verdict === 'block' && <Button size="sm" variant="outline" type="button" onClick={() => setException(true)}>{t(`${T}add_exception`)}</Button>}
              {hit?.policy_id ? (
                <Button size="sm" variant="outline" type="button" onClick={() => onOpenPolicy(hit.policy_id!)}>{t(`${T}open_policy`, { name: hit.name || t(`${T}deleted_policy`) })}</Button>
              ) : null}
            </div>
          </div>
        )}
      </div>
      <ExceptionDialog key={`${hit?.group_id ?? 0}:${result?.host ?? ''}`} open={exception} host={result?.host ?? ''} group={hit?.kind === 'group' && hit.group_id ? { id: hit.group_id, name: hit.name ?? '' } : null} onClose={() => setException(false)} />
    </form>
  )
}

/**
 * An allow exception for the tested destination: the site (and its subdomains) or just that host. When an
 * allowlist group refused it, the default is that group's supplement list (only its members); everyone is a
 * deliberate choice and carries the warning the records dialog gives.
 */
function ExceptionDialog({ open, host, group, onClose }: { open: boolean; host: string; group: { id: number; name: string } | null; onClose: () => void }) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const add = useAddDestException(scope)
  const [entry, setEntry] = useState(host)
  const [match, setMatch] = useState<'site' | 'host'>('site')
  const [error, setError] = useState(false)
  // The parent keys this by group and host, so a new destination starts fresh; the choice is reset once the
  // close transition has finished, so the content stays on screen while the dialog fades out.
  const [everyone, setEveryone] = useState(false)
  const reset = () => { setEntry(host); setMatch('site'); setEveryone(false); setError(false) }
  // A group step with no name is a group that was deleted while its rule is still published: nothing to add to, and no default.
  const gone = group !== null && !group.name
  const toGroup = group !== null && !gone && !everyone
  const none = gone && !everyone

  const save = async () => {
    setError(false)
    try {
      const r = await add.mutateAsync({ target: entry.trim(), match, ...(toGroup && group ? { group_id: group.id } : {}) })
      pushSnack(toGroup && group
        ? t('admin:access_control.records.exception.saved_group', { group: group.name, entry: r.entry })
        : t(`${T}exception_saved`, { entry: r.entry }), 'success')
      onClose()
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_exception_invalid') setError(true)
      else reportDestError(t, err)
    }
  }
  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }} onOpenChangeComplete={o => { if (!o) reset() }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader><DialogTitle>{t(`${T}exception.title`)}</DialogTitle></DialogHeader>
        <div className="grid gap-4">
          {!none && <p className="text-sm">{toGroup && group ? t(`${T}exception.lead_group`, { group: group.name }) : t(`${T}exception.lead`)}</p>}
          {none && <p className="text-sm text-destructive">{t('admin:access_control.records.exception.group_gone')}</p>}
          {group && (
            <div className="grid gap-2">
              <ModeRadioGroup<'group' | 'global'> value={none ? null : toGroup ? 'group' : 'global'} aria-label={t('admin:access_control.records.exception.where')}
                onChange={v => setEveryone(v === 'global')}
                options={[
                  ...(!gone ? [{ value: 'group' as const, children: t('admin:access_control.records.exception.group', { group: group.name }) }] : []),
                  { value: 'global' as const, children: t('admin:access_control.records.exception.global') },
                ]} />
              {everyone && <FieldHint tone="warning" summary={t('admin:access_control.records.exception.global_hint.summary')} detail={t('admin:access_control.records.exception.global_hint.detail')} />}
            </div>
          )}
          <Field label={t(`${T}target`)} error={error ? t(`${T}exception.invalid`) : undefined}>
            <Input value={entry} onChange={e => setEntry(e.target.value)} />
          </Field>
          <Field label={t(`${T}exception.match`)}>
            {p => <SegmentedControl<'site' | 'host'> value={match} onChange={setMatch} aria-labelledby={p.labelId}
              // Two long labels do not fit a phone side by side: they stack there, full width.
              className="w-full flex-col items-stretch sm:w-fit sm:flex-row [&>button]:h-auto [&>button]:min-h-8 [&>button]:whitespace-normal [&>button]:py-1"
              options={[{ value: 'site', label: t(`${T}exception.site`) }, { value: 'host', label: t(`${T}exception.host`) }]} />}
          </Field>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t(`${T}exception.cancel`)}</Button>
          <AsyncButton disabled={none} onClick={save}>{t(`${T}exception.save`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
