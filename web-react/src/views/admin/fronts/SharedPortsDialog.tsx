import { useCallback, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { IconArrowLeft } from '@/lib/icons'
import { deleteFront, frontErrorOf, type Front, type FrontError } from '@/api/fronts'
import type { Server } from '@/api/servers'
import { HelpTip } from '@/components/common/HelpTip'
import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { confirm } from '@/components/ConfirmHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { useFrontOptions, useFronts } from '@/query/fronts'
import { frontKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { Notice } from '@/components/common/Notice'
import { useAdminT } from '../useAdminT'
import FrontEditor from './FrontEditor'
import FrontGraphPanel from './FrontGraphPanel'
import FrontList from './FrontList'
import FrontPlanDialog from './FrontPlanDialog'
import PortUsageTable from './PortUsageTable'
import { frontErrorText } from './frontErrors'
import { useFrontNames } from './useFrontNames'

type View = { kind: 'list' } | { kind: 'edit'; front: Front | null }

/**
 * The shared ports (SNI fronts) of one Passwall Node server, and the view of every port the server uses, in a
 * wide dialog (a bottom sheet on a phone) over the servers page: the list with its picture, and the editor of one port as a page inside the
 * dialog (so a long form has room, a bar and an unsaved-changes guard). Mount only while open: it reads on mount.
 */
export default function SharedPortsDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const fronts = useFronts(scope, server.id)
  const options = useFrontOptions(scope, server.id)
  const names = useFrontNames(options.data)
  const phone = useMediaQuery('(max-width: 639px)')
  const [tab, setTab] = useState<'fronts' | 'ports'>('fronts')
  const [view, setView] = useState<View>({ kind: 'list' })
  const [dirty, setDirty] = useState(false)
  // The server's plan for a delete that needs agreement, waiting for the admin's answer.
  const [plan, setPlan] = useState<{ error: FrontError; front: Front; answer: (ok: boolean) => void } | null>(null)

  const data = fronts.data
  const supported = data?.support.supported ?? false
  const full = data ? data.fronts.length >= data.max : false
  const unsupportedReason = data && !supported ? t(`admin:servers.fronts.unsupported.${data.support.reason ?? 'no_agent'}`) : undefined
  // The inactive ones: everything is when the server cannot run fronts, otherwise those the server names as withheld.
  const inactive = useMemo(() => new Set((data?.fronts ?? []).filter(f => !supported || data?.withheld.some(w => w.front_id === f.id)).map(f => f.id)), [data, supported])
  const addOffReason = unsupportedReason ?? (full ? t('admin:servers.fronts.full', { max: data?.max ?? 0 }) : undefined)
  const canAdd = !!data && supported && !full && !!options.data

  const editing = view.kind === 'edit'
  const guardClose = useDirtyClose(editing && dirty, useMemo(() => ({
    title: t('admin:servers.fronts.discard_title'), message: t('admin:servers.fronts.discard_message'),
    confirmText: t('admin:servers.fronts.discard_confirm'), destructive: true, initialFocus: 'cancel' as const,
  }), [t]))
  const backToList = useCallback(() => { setDirty(false); setView({ kind: 'list' }) }, [])

  async function remove(front: Front) {
    const ok = await confirm({
      title: t('admin:servers.fronts.delete_title', { port: front.port }),
      message: t('admin:servers.fronts.delete_text'),
      confirmText: t('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    try {
      try {
        await deleteFront(server.id, front.id)
      } catch (err) {
        // A front with listeners or lines behind it asks first: the server's plan, as its own dialog.
        const e = frontErrorOf(err)
        if (!e || e.status !== 409 || e.code !== 'confirm_required') throw err
        const agreed = await new Promise<boolean>(resolve => setPlan({ error: e, front, answer: resolve }))
        setPlan(null)
        if (!agreed) return
        await deleteFront(server.id, front.id, true)
      }
      await queryClient.invalidateQueries({ queryKey: frontKeys.all(scope) })
      pushSnack(t('admin:servers.fronts.deleted'), 'success')
    } catch (err) {
      pushSnack(frontErrorText(t, err), 'error')
    }
  }

  const addButton = (
    <Button aria-disabled={!canAdd || undefined} className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      onClick={() => { if (canAdd) setView({ kind: 'edit', front: null }) }}>
      {t('admin:servers.fronts.add')}
    </Button>
  )
  // Off WITH its reason, in a tooltip a keyboard can reach: a button that does nothing says why before the click.
  const add = addOffReason ? <Tooltip><TooltipTrigger render={addButton} /><TooltipContent>{addOffReason}</TooltipContent></Tooltip> : addButton

  const withheld = data && supported ? data.withheld : []
  const title = view.kind === 'edit'
    ? (view.front ? t('admin:servers.fronts.edit_title', { port: view.front.port }) : t('admin:servers.fronts.new_title'))
    : t('admin:servers.fronts.title', { name: server.name })

  return (
    <>
      <DetailDialog
        open
        onClose={() => guardClose(onClose)}
        title={title}
        status={view.kind === 'list' ? <HelpTip text={t('admin:servers.fronts.help.what')} /> : undefined}
        actions={view.kind === 'edit'
          ? <Button variant="outline" size="sm" onClick={() => guardClose(backToList)}><IconArrowLeft />{t('admin:servers.fronts.back')}</Button>
          : tab === 'fronts' ? add : undefined}
        size="xl"
      >
        {view.kind === 'edit' && data && options.data ? (
          <FrontEditor key={view.front?.id ?? 'new'} panelId={server.id} front={view.front} options={options.data}
            defaults={data.defaults} onDone={backToList} onDirtyChange={setDirty} />
        ) : (
          <div className="flex flex-col gap-4">
            <Tabs value={tab} onValueChange={v => setTab(v as 'fronts' | 'ports')}>
              <TabsList>
                <TabsTrigger value="fronts">{t('admin:servers.fronts.tab_fronts')}</TabsTrigger>
                <TabsTrigger value="ports">{t('admin:servers.fronts.tab_ports')}</TabsTrigger>
              </TabsList>
            </Tabs>
            {tab === 'ports' ? <PortUsageTable panelId={server.id} /> : (
              <>
                {unsupportedReason && <Notice>{unsupportedReason}</Notice>}
                {withheld.map(w => (
                  <Notice key={w.front_id}>
                    {t('admin:servers.fronts.withheld.text', { port: w.port, why: t(`admin:servers.fronts.withheld.reason.${w.reason}`, { defaultValue: w.reason }), detail: w.detail ?? '' })}
                  </Notice>
                ))}
                {fronts.isPending ? <Skeleton aria-label={t('admin:servers.fronts.loading')} role="status" className="h-40 rounded-xl" /> : fronts.isError ? (
                  <ErrorState compact title={t('admin:servers.fronts.load_failed')} retry={() => fronts.refetch()} />
                ) : data && (
                  <>
                    {data.fronts.length > 0 && !phone && <FrontGraphPanel fronts={data.fronts} inactive={inactive} names={names} />}
                    <FrontList
                      fronts={data.fronts} inactive={inactive} names={names}
                      canEdit={supported && !!options.data} editOffReason={unsupportedReason ?? t('admin:servers.fronts.unsupported_short')}
                      loading={false} error={null} onRetry={() => void fronts.refetch()}
                      empty={<EmptyState compact title={t('admin:servers.fronts.empty_title')} description={t('admin:servers.fronts.empty')} />}
                      onEdit={f => setView({ kind: 'edit', front: f })} onDelete={f => void remove(f)}
                    />
                    {/* On a phone the picture stacks its three columns into one long list, so the rows come first and it opens on request. */}
                    {data.fronts.length > 0 && phone && (
                      <details className="rounded-lg border px-3 py-2">
                        <summary className="cursor-pointer text-sm font-medium">{t('admin:servers.fronts.graph.title')}</summary>
                        <div className="mt-2"><FrontGraphPanel fronts={data.fronts} inactive={inactive} names={names} /></div>
                      </details>
                    )}
                  </>
                )}
              </>
            )}
          </div>
        )}
      </DetailDialog>
      {plan && (
        <FrontPlanDialog
          error={plan.error}
          title={t('admin:servers.fronts.delete_title', { port: plan.front.port })}
          confirmLabel={t('admin:servers.fronts.delete_confirm', { port: plan.front.port })}
          onAnswer={plan.answer}
        />
      )}
    </>
  )
}
