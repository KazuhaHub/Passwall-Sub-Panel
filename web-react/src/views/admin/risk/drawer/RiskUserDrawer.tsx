import { useId, useState } from 'react'
import { Link } from 'react-router'
import { isAxiosError } from 'axios'
import { XIcon } from 'lucide-react'

import { MoreMenu } from '@/components/common/MoreMenu'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Button } from '@/components/ui/button'
import { Sheet, SheetClose, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TOUCH_TARGET } from '@/components/common/touchTarget'
import { useRiskUser } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import {
  availableActions, DESTRUCTIVE_ACTIONS, otherHold, subjectOfSummary, type RiskActionKind, type RiskSubject,
} from '../actions/riskSubject'
import { useRiskActions } from '../actions/useRiskActions'
import { useRiskT } from '../useRiskT'
import AccessTab from './AccessTab'
import ConnectionsTab from './ConnectionsTab'
import DevicesTab from './DevicesTab'
import DrawerHeader from './DrawerHeader'
import OverviewTab from './OverviewTab'
import TimelineTab from './TimelineTab'

export type DrawerTab = 'overview' | 'connections' | 'devices' | 'timeline' | 'access'
/** The fifth tab is the access-control one: only an admin is offered it. */
const TABS: readonly DrawerTab[] = ['overview', 'connections', 'devices', 'timeline', 'access']

export type DrawerHost = 'risk' | 'users' | 'access'

/** The actions shown as buttons; the rest (undo, trust) sit behind ⋯. */
const PRIMARY: readonly RiskActionKind[] = ['pause', 'convert_manual', 'resume', 'dismiss', 'redismiss']

function ActionBar({ subject, host, onStart }: {
  subject: RiskSubject
  host: DrawerHost
  onStart: (kind: RiskActionKind, subject: RiskSubject) => void
}) {
  const { t } = useRiskT()
  const actions = availableActions(subject, 'drawer')
  const primary = actions.filter(a => PRIMARY.includes(a))
  const more = actions.filter(a => !PRIMARY.includes(a))
  const label = (k: RiskActionKind) => t(`admin:risk_center.actions.${k}`)
  return (
    <div className="flex flex-wrap items-center gap-2">
      {primary.map(k => (
        <Button key={k} variant="outline" size="sm"
          className={DESTRUCTIVE_ACTIONS.includes(k) ? 'text-destructive hover:text-destructive' : undefined}
          onClick={() => onStart(k, subject)}>
          {label(k)}
        </Button>
      ))}
      {/* A hold the risk center did not write is not its to lift: the Users page owns it. Not offered on
          the Users page itself. */}
      {otherHold(subject) && host !== 'users' && (
        <Button variant="link" size="sm" render={<Link to={`/admin/users?q=${encodeURIComponent(subject.upn)}`} />}>
          {t('admin:risk_center.drawer.open_users')}
        </Button>
      )}
      {more.length > 0 && (
        <MoreMenu size="icon-sm" label={t('admin:risk_center.actions.more')}
          items={more.map(k => ({
            id: k, label: label(k), destructive: DESTRUCTIVE_ACTIONS.includes(k), onSelect: () => onStart(k, subject),
          }))} />
      )}
    </div>
  )
}

/** The close button, in the corner of the header like every detail sheet's. */
function CloseButton() {
  const { t } = useRiskT()
  return (
    <SheetClose
      render={<Button variant="ghost" size="icon-sm" className={`${TOUCH_TARGET} absolute top-3 right-3`}
        aria-label={t('admin:risk_center.drawer.close')} title={t('admin:risk_center.drawer.close')} />}
    >
      <XIcon />
    </SheetClose>
  )
}

/**
 * ONE ACCOUNT'S RISK DETAIL, over whichever page opened it: who it is and where its two access axes
 * stand, what can be done about it (the one action matrix the queue's row menu also uses), and tabs —
 * every detector's verdict, where it connected from, the devices behind its fetches, its timeline and,
 * for who may see it, its access-control standing.
 *
 * Permanently mounted, like AccountSecurityDrawer: `shown` keeps the last account so the close
 * transition has content, and each open — or a switch to another account — starts again on the
 * overview with no dialog left open. A refresh of the summary replaces its data in place: the sheet and
 * the tab the admin is on stay. It reads nothing while closed.
 */
export default function RiskUserDrawer({ userId, onClose, host, initialTab }: {
  userId: number | null
  onClose: () => void
  /** 'users' when the Users page hosts it: its link back to that page is
   *  left out. 'access' is the access-control page, which opens on the
   *  access tab. */
  host: DrawerHost
  /** The tab each open starts on; the access host defaults to 'access', the
   *  others to the overview. */
  initialTab?: DrawerTab
}) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const headingId = useId()
  const actions = useRiskActions()
  const open = userId !== null

  const [shown, setShown] = useState<number | null>(userId)
  if (userId !== null && userId !== shown) setShown(userId)

  const canAccess = useCan('access.view')
  const tabs = TABS.filter(k => k !== 'access' || canAccess)
  const startTab: DrawerTab = initialTab ?? (host === 'access' ? 'access' : 'overview')
  const landing: DrawerTab = tabs.includes(startTab) ? startTab : 'overview'
  const [tabState, setTab] = useState<DrawerTab>(landing)
  // A tab the role can no longer see (the permission went while it was open) reads as the overview.
  const tab: DrawerTab = tabs.includes(tabState) ? tabState : 'overview'
  // Each open edge and each switch of account resets the transient UI.
  const openKey = open ? String(userId) : ''
  const [seenKey, setSeenKey] = useState(openKey)
  if (openKey !== seenKey) {
    setSeenKey(openKey)
    setTab(landing)
    actions.cancel()
  }

  const q = useRiskUser(scope, shown ?? 0, { enabled: open })
  const data = q.data
  const status = isAxiosError(q.error) ? q.error.response?.status : undefined

  let body
  if (data) {
    body = (
      <Tabs value={tab} onValueChange={v => setTab(v as DrawerTab)} className="min-h-0 flex-1 gap-0">
        <SheetHeader className="gap-3 border-b p-4 pr-14">
          <DrawerHeader summary={data} />
          <ActionBar subject={subjectOfSummary(data)} host={host} onStart={actions.start} />
          <CloseButton />
        </SheetHeader>
        {/* The tab strip stays put while a long tab scrolls under it. */}
        <div className="overflow-x-auto border-b px-4 py-2">
          <TabsList aria-label={t('admin:risk_center.drawer.title')}>
            {tabs.map(k => <TabsTrigger key={k} value={k}>{t(`admin:risk_center.drawer.tab_${k}`)}</TabsTrigger>)}
          </TabsList>
        </div>
        {/* Only the open tab is mounted, so only its lists are read. */}
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          <TabsContent value="overview"><OverviewTab summary={data} /></TabsContent>
          <TabsContent value="connections"><ConnectionsTab summary={data} /></TabsContent>
          <TabsContent value="devices"><DevicesTab summary={data} /></TabsContent>
          {canAccess && <TabsContent value="access"><AccessTab userId={data.user.id} /></TabsContent>}
          <TabsContent value="timeline"><TimelineTab userId={data.user.id} upn={data.user.upn} /></TabsContent>
        </div>
      </Tabs>
    )
  } else {
    // Before the summary arrives, or when it cannot: the sheet still has a name and a way out.
    body = (
      <>
        <SheetHeader className="border-b p-4 pr-14">
          <SheetTitle id={headingId} className="text-base">{t('admin:risk_center.drawer.title')}</SheetTitle>
          <CloseButton />
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {q.isError
            ? status === 404 ? <EmptyState compact title={t('admin:risk_center.drawer.not_found')} />
              : status === 503 ? <EmptyState compact title={t('admin:risk_center.unwired')} />
                : <ErrorState compact reason={q.error} retry={() => q.refetch()} />
            : <CardSkeleton lines={6} />}
        </div>
      </>
    )
  }

  return (
    <Sheet open={open} onOpenChange={next => { if (!next) onClose() }}>
      <SheetContent side="right" showCloseButton={false}
        className="gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:w-[46rem] data-[side=right]:sm:max-w-[46rem]">
        {shown !== null && body}
        {actions.dialogs}
      </SheetContent>
    </Sheet>
  )
}
