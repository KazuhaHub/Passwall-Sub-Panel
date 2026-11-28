import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import type { Server } from '@/api/servers'
import { PageHeader } from '@/components/common/PageHeader'
import { EmptyState } from '@/components/feedback/EmptyState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { pushToast } from '@/components/feedback/toasts'
import { FormSection } from '@/components/form/FormSection'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useQueryClient } from '@tanstack/react-query'
import { nodeKeys } from '@/query/keys'
import { useAllNodes, useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { GeneralFields, generalProblems, regionOptionsOf, type GeneralDraft, type GeneralField } from '../general/GeneralFields'
import { InboundForm } from '../inbound/InboundForm'
import { serverHost } from '../inbound/inboundModel'
import { randomRealitySite } from '@/utils/xrayReality'
import { useNodesT } from '../shared/useNodesT'
import { splitTagsText } from '@/api/lineLabels'
import { EntriesStep } from './EntriesStep'
import { ExitStep } from './ExitStep'
import { ReviewStep } from './ReviewStep'
import { Stepper } from './Stepper'
import { STEPS, useCreateWizard } from './useCreateWizard'
import { writableServers } from './writable'

const NONE: never[] = []

/**
 * `/admin/nodes/new` (design 4.8): the node page's four sections as four steps, so creating and editing a node
 * are the same questions in the same words — the inbound and the node's basics, how users get in (lines), where
 * traffic leaves (exit), and a review of the plan the server drew. Nothing is created before the review is
 * confirmed. The page guards leaving while anything has been entered.
 */
function Wizard({ servers, writable }: { servers: Server[]; writable: Server[] }) {
  const t = useNodesT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const scope = useQueryScope()
  const nodes = useAllNodes(scope)
  const allow = useRef(false)
  const [touched, setTouched] = useState<ReadonlySet<GeneralField>>(new Set())
  const w = useCreateWizard({
    servers, writable, t: t as (k: string, o?: Record<string, unknown>) => string,
    onCreated: node => {
      pushToast({ message: t('nodes.create_dialog.created'), severity: 'success' })
      void queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })
      allow.current = true
      // A created node lands on its own page, where the lines that wait for their entry server show straight away; a queued creation has no node yet.
      navigate(node ? `/admin/nodes/${node.id}` : '/admin/nodes')
    },
  })
  useLeaveGuard(w.dirty && !allow.current, discardChangesConfirm(t), (next, current) => !allow.current && next.pathname !== current.pathname)

  const exits = servers.filter(s => s.panel_type === 'psp' && s.id !== w.form.panel_id)
  const general: GeneralDraft = { display_name: w.form.display_name, server_address: w.form.server_address, region: w.form.region, tags: splitTagsText(w.form.tags_text) }
  const problems = generalProblems(general)
  const tagOptions = [...new Set((nodes.data ?? NONE).flatMap(n => n.tags ?? []))].sort((a, b) => a.localeCompare(b))
  const regionOptions = useMemo(() => regionOptionsOf(nodes.data ?? NONE), [nodes.data])
  const names = STEPS.map(s => t(`nodes.create_page.step_${s}`))
  const label = w.step < 2 ? 'nodes.setup.next' : w.step === 2 || !w.plan || w.changed ? 'nodes.setup.review' : 'nodes.setup.confirm'

  return (
    <>
      <PageHeader title={t('nodes.create')} />
      <Stepper steps={names} active={w.step} label={t('nodes.create_page.steps')} />
      {w.error && <Alert variant="destructive" className="mb-4" role="alert"><AlertDescription>{w.error}</AlertDescription></Alert>}
      <form className="flex max-w-3xl flex-col gap-6" noValidate onSubmit={e => { e.preventDefault(); void w.next() }}>
        {w.step === 0 && (
          <>
            {w.jsonAllowed && (
              <SegmentedControl aria-label={t('nodes.inbound.mode')} value={w.json ? 'json' : 'form'} onChange={v => w.setJsonMode(v === 'json')}
                options={[{ value: 'form', label: t('nodes.inbound.mode_form') }, { value: 'json', label: t('nodes.inbound.mode_json') }]} />
            )}
            <InboundForm form={w.form} setForm={w.setForm} server={w.landing} servers={writable} json={w.json} onLeaveJson={() => w.setJsonMode(false)}
              onGenKeys={w.genKeys} onRandomRealitySite={w.randomizeSite} onGenSSPassword={w.genSSPassword} genKeysBusy={w.genBusy} />
            <FormSection title={t('nodes.create_page.basics')}>
              <GeneralFields draft={general} tagOptions={tagOptions} regionOptions={regionOptions}
                patch={({ tags, ...rest }) => w.setForm(f => ({ ...f, ...rest, ...(tags ? { tags_text: tags.join(', ') } : {}) }))}
                error={f => (w.attempted || touched.has(f) ? problems[f] : undefined)} blur={f => setTouched(s => (s.has(f) ? s : new Set(s).add(f)))} />
            </FormSection>
          </>
        )}
        {w.step === 1 && (
          <EntriesStep servers={servers.filter(s => s.panel_type === 'psp' && s.id !== w.form.panel_id)} recent={w.recent} entries={w.entries} onChange={w.setEntries}
            canTunnel={w.native} useDirect={w.direct} onDirectChange={w.setDirect} hostOf={serverHost} newKey={w.newKey} randomSite={randomRealitySite}
            tcpTLS={w.form.vless_network === 'tcp' && w.form.vless_security !== 'none'} onBusyChange={w.setEntriesBusy} showErrors={w.attempted} />
        )}
        {w.step === 2 && w.landing && (
          <ExitStep name={w.form.display_name} landing={w.landing} native={w.native} servers={exits} egress={w.egress} onEgress={w.setEgress} remote={w.remoteExit} onRemote={w.setRemoteExit} routes={w.routes} onRoutes={w.setRoutes} />
        )}
        {w.step === 3 && w.plan && w.landing && (
          <ReviewStep plan={w.plan} entries={w.entries} direct={w.direct} peers={servers} landingPort={w.form.port} routeCount={w.routes.length}
            editingLoopbacks={w.loopbacks} onLoopback={(server, port) => w.setLoopbacks(p => ({ ...p, [server]: port }))}
            editingLinkPorts={w.linkPorts} onLinkPort={(link, port) => w.setLinkPorts(p => ({ ...p, [link]: port }))} changed={w.changed}
            landingName={w.form.display_name} exitName={servers.find(s => s.id === w.egress)?.name ?? null} />
        )}
        <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-4">
          <Button type="button" variant="outline" disabled={w.creating} render={<Link to="/admin/nodes" />}>{t('common:actions.cancel')}</Button>
          <div className="flex items-center gap-2">
            {w.step > 0 && <Button type="button" variant="outline" disabled={w.busy} onClick={w.goBack}>{t('nodes.setup.back')}</Button>}
            <Button type="submit" disabled={w.busy}>{t(label)}</Button>
          </div>
        </div>
      </form>
    </>
  )
}

/** The page's own guards: there must be a server to create on, and a reason is given when there is not (design P3). */
export default function CreatePage() {
  const t = useNodesT()
  const scope = useQueryScope()
  const servers = useAllServers(scope)
  const back = (none: boolean) => <Button variant="outline" render={<Link to={none ? '/admin/servers' : '/admin/nodes'} />}>{t(none ? 'nodes.empty.no_servers' : 'nodes.detail.back')}</Button>
  if (servers.isPending) return <LoadingState />
  const list = servers.data ?? NONE
  const writable = writableServers(list)
  if (writable.length === 0) return <EmptyState title={t(list.length === 0 ? 'nodes.create_dialog.no_servers' : 'nodes.create_dialog.no_writable_servers')} action={back(list.length === 0)} />
  return <Wizard servers={list} writable={writable} />
}
