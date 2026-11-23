import { useCallback, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { User } from '@/api/types'
import { DetailSheet } from '@/components/data/DetailSheet'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { userDetailQuery } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { UserNodeUsage } from '../UserNodeUsage'
import { UserServerUsage } from '../UserServerUsage'
import { UserActivity } from '../UserActivity'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { AxisBadge } from './cells'
import { useUsers } from './context'
import { CreateTab } from './CreateTab'
import { EditTab } from './EditTab'
import { canManageUser } from './model'
import { OverviewTab } from './OverviewTab'
import { SecurityTab } from './SecurityTab'
import { accountAxis, serviceAxis } from './status'
import { useAdminT } from './useAdminT'
import { SHEET_TABS, type SheetTab, type SheetTarget, type UserSheetControl } from './useUserSheet'

const WIDE = 'data-[side=right]:sm:w-[46rem] data-[side=right]:sm:max-w-[46rem]'

function ReadOnlyNote() {
  const t = useAdminT()
  return <Alert><AlertDescription>{t('users.readonly_operator')}</AlertDescription></Alert>
}

/**
 * One user in a wide sheet: overview, settings, usage, activity and security as tabs; or, for `?user=new`, the
 * create form on its own. The opened user and the tab are in the URL (see useUserSheet).
 *
 * The settings tab keeps its draft while you look at another tab (a tab is built on first visit and then kept),
 * and anything that would drop it asks first: closing the sheet, Back, opening another user, leaving the page.
 * Deleting the user, or finishing a create, are the two ways out that do not ask, because the draft is moot.
 */
export function UserSheet({ sheet, rows, loading }: { sheet: UserSheetControl; rows: readonly User[]; loading: boolean }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { canElevate } = useUsers()
  const { target } = sheet
  const creating = target === 'new'
  const id = typeof target === 'number' ? target : 0
  const listed = rows.find(r => r.id === id)
  // A user who is not on this page (a link, the palette) is read by id; one the list holds is never read twice.
  const detail = useQuery({ ...userDetailQuery(scope, id), enabled: id > 0 && !listed })
  const user = listed ?? detail.data

  const [dirty, setDirty] = useState(false)
  const setDirtyStable = useCallback((v: boolean) => setDirty(v), [])

  useLeaveGuard(dirty, discardChangesConfirm(t), (next, current) => {
    if (sheet.bypass.current) return false
    if (next.pathname !== current.pathname) return true
    return new URLSearchParams(next.search).get('user') !== new URLSearchParams(current.search).get('user')
  })

  // A tab is built on first visit and kept, so the settings draft survives a look at another tab.
  const tab = sheet.tab
  const [seen, setSeen] = useState<{ of: SheetTarget | null; tabs: ReadonlySet<SheetTab> }>({ of: target, tabs: new Set([tab]) })
  const tabs: ReadonlySet<SheetTab> = seen.of === target ? (seen.tabs.has(tab) ? seen.tabs : new Set(seen.tabs).add(tab)) : new Set([tab])
  const mounted = (name: SheetTab) => tabs.has(name)
  const go = (name: SheetTab) => { setSeen({ of: target, tabs: new Set(tabs).add(name) }); sheet.setTab(name) }

  let title: ReactNode = ''
  let status: ReactNode
  let description: string | undefined
  let body: ReactNode = null

  if (target === null) {
    body = null
  } else if (creating) {
    title = t('users.create')
    body = <CreateTab onDirtyChange={setDirtyStable} onCreated={sheet.exit} />
  } else if (!user) {
    title = `#${id}`
    body = detail.isError
      ? ((detail.error as { response?: { status?: number } } | null)?.response?.status === 404
        ? <EmptyState compact title={t('users.detail.not_found')} action={<Button variant="outline" onClick={sheet.close}>{t('users.detail.close')}</Button>} />
        : <ErrorState compact reason={detail.error} retry={() => void detail.refetch()} />)
      : (loading || detail.isPending ? <LoadingState /> : null)
  } else {
    const manageable = canManageUser(user, canElevate)
    title = user.upn
    description = [user.display_name, user.email].filter(Boolean).join(' · ') || undefined
    status = <span className="flex flex-wrap items-center gap-x-3 gap-y-1"><AxisBadge axis={accountAxis(user)} /><AxisBadge axis={serviceAxis(user)} /></span>
    body = (
      <Tabs value={tab} onValueChange={v => go(v as SheetTab)}>
        <TabsList>
          {SHEET_TABS.map(name => (
            <TabsTrigger key={name} value={name}>
              {t(`users.tabs.${name}`)}
              {name === 'settings' && dirty && <span role="img" aria-label={t('users.tabs.unsaved')} title={t('users.tabs.unsaved')} className="ml-1 size-1.5 rounded-full bg-warning" />}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="overview" className="mt-4"><OverviewTab user={user} manageable={manageable} /></TabsContent>
        {/* keepMounted: the form holds the draft, which the Tabs component cannot see, so it would unmount it. */}
        <TabsContent value="settings" keepMounted className="mt-4">
          {manageable ? (mounted('settings') && <EditTab user={user} onDirtyChange={setDirtyStable} />) : <ReadOnlyNote />}
        </TabsContent>
        <TabsContent value="usage" className="mt-4">
          <div className="flex flex-col gap-6"><UserServerUsage userId={user.id} /><UserNodeUsage userId={user.id} /></div>
        </TabsContent>
        <TabsContent value="activity" className="mt-4"><UserActivity userId={user.id} /></TabsContent>
        <TabsContent value="security" className="mt-4">{manageable ? <SecurityTab user={user} /> : <ReadOnlyNote />}</TabsContent>
      </Tabs>
    )
  }

  return (
    <DetailSheet open={target !== null} onClose={sheet.close} title={title} status={status} description={description} className={WIDE}>
      {/* Keyed by who is opened, so another user starts from a clean form. */}
      <div key={String(target)}>{body}</div>
    </DetailSheet>
  )
}
