import { useEffect, useRef } from 'react'
import { Navigate, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { User } from '@/api/types'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { ObjectTabs, useObjectLeaveGuard, type ObjectTab } from '@/components/common/ObjectTabs'
import { useBackTarget } from '@/components/common/objectReturn'
import { useDirtyTabs } from '@/components/common/useDirtyTabs'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { userDetailQuery } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { UserActivity } from '../../UserActivity'
import { UserNodeUsage } from '../../UserNodeUsage'
import { UserServerUsage } from '../../UserServerUsage'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import RiskUserDialog from '../../risk/dialog/RiskUserDialog'
import { RoleBadge, SsoBadge } from '../cells'
import { UsersContext, useUsers } from '../context'
import { EditTab } from '../EditTab'
import { canManageUser } from '../model'
import { OverviewTab } from '../OverviewTab'
import { SecurityTab } from '../SecurityTab'
import { takeSecret } from '../secretHandoff'
import { UserDialogs } from '../UserDialogs'
import { useAdminT } from '../useAdminT'
import { useUsersWorkspace } from '../useUsersWorkspace'
import { USER_TABS, USERS_PATH, type UserTab } from '../userRoutes'
import { useUserHeader } from './useUserHeader'

/** Only Settings holds a draft; the other tabs act at once (and ask first where it matters). */
const EDITABLE = ['settings'] as const
const NOOP_BATCH = async () => {}

function ReadOnlyNote() {
  const t = useAdminT()
  return <Alert><AlertDescription>{t('users.readonly_operator')}</AlertDescription></Alert>
}

/**
 * One loaded user. Split from the page so the hooks that need the user (the header's actions, the guard) are not
 * called conditionally above the loading and error states.
 */
function LoadedUser({ user, back, leaving }: { user: User; back: string; leaving: { current: boolean } }) {
  const t = useAdminT()
  const { canElevate } = useUsers()
  const manageable = canManageUser(user, canElevate)
  const { dirty, report } = useDirtyTabs(EDITABLE)
  const names = dirty.map(d => t(`users.tabs.${d}`)).join(t('users.detail.tab_separator'))
  useObjectLeaveGuard({ dirty: dirty.length > 0, copy: { ...discardChangesConfirm(t), message: t('users.detail.unsaved_in', { tabs: names }) }, bypass: leaving })
  const header = useUserHeader(user, manageable)

  const tabs: ObjectTab<UserTab>[] = USER_TABS.map(id => ({
    id,
    label: t(`users.tabs.${id}`),
    dirty: id === 'settings' && dirty.includes('settings'),
    // The two tabs of tables and charts use the page's width; the rest are a readable column.
    width: id === 'usage' || id === 'activity' ? 'full' : 'form',
  }))
  const panels: Record<UserTab, React.ReactNode> = {
    overview: <OverviewTab user={user} manageable={manageable} />,
    settings: manageable ? <EditTab user={user} onDirtyChange={report.settings} /> : <ReadOnlyNote />,
    usage: <div className="flex flex-col gap-6"><UserServerUsage userId={user.id} /><UserNodeUsage userId={user.id} /></div>,
    activity: <UserActivity userId={user.id} />,
    security: manageable ? <SecurityTab user={user} /> : <ReadOnlyNote />,
  }

  return (
    <ObjectPage
      back={{ to: back, label: t('users.title') }}
      title={user.upn}
      badges={<><RoleBadge role={user.role} /><SsoBadge user={user} /></>}
      subtitle={[user.display_name, user.email].filter(Boolean).join(' · ') || undefined}
      status={<>{header.status}{!manageable && <span className="text-xs text-muted-foreground">{t('users.readonly_operator')}</span>}</>}
      secondary={header.secondary}
      more={header.more}
    >
      <ObjectTabs tabs={tabs} panels={panels} unsavedLabel={t('users.tabs.unsaved')} />
    </ObjectPage>
  )
}

/**
 * `/admin/users/:id`: one user as a page. The tabs are the five questions about an account (who and how it is doing,
 * its settings, its usage, its sign-ins, its security); the settings tab keeps its draft while another tab is open,
 * and leaving the page asks once. The user is read by id (the list's row is not needed), so a link opens it cold.
 * A delete made here leaves for the list without asking, because the draft is moot.
 */
export default function UserDetailPage({ id }: { id: number }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const navigate = useNavigate()
  const back = useBackTarget(USERS_PATH)
  // Set when this page deleted the user: the page then leaves for the list, whatever is unsaved, instead of saying "not found".
  const leaving = useRef(false)
  const ws = useUsersWorkspace({ afterDelete: gone => { if (gone === id) { leaving.current = true; void navigate(back, { replace: true }) } } })
  const query = useQuery(userDetailQuery(scope, id))
  const user = query.data
  const { setDialog } = ws

  // A create hands over its one-time result here (initial password, subscription link), shown as the dialog it always was.
  useEffect(() => {
    const secret = takeSecret(id)
    if (secret) setDialog({ kind: 'secret', user: secret.user, password: secret.password, created: true })
  }, [id, setDialog])

  const kind = query.isError && !user ? objectErrorKind(query.error) : null
  if (kind === 'not_found' && leaving.current) return <Navigate to={back} replace />
  const state: ObjectPageState | undefined = kind === 'not_found' ? { kind, title: t('users.detail.not_found') }
    : kind === 'forbidden' ? { kind, title: t('users.detail.forbidden') }
    : kind === 'error' ? { kind, reason: query.error, retry: () => query.refetch() }
    : !user ? { kind: 'loading' } : undefined

  return (
    <UsersContext.Provider value={ws.ctx}>
      {state || !user
        ? <ObjectPage back={{ to: back, label: t('users.title') }} state={state} />
        // Keyed by who is opened, so another user starts from clean forms.
        : <LoadedUser key={user.id} user={user} back={back} leaving={leaving} />}
      <UserDialogs state={ws.dialog} setState={ws.setDialog} reload={ws.ctx.reload} toggleBatch={NOOP_BATCH} />
      {/* Above every modal. Not mounted for an operator: a link to ?risk= would only read a 403. */}
      {ws.canRisk && <RiskUserDialog userId={ws.riskDrawer.id} onClose={ws.riskDrawer.close} host="users" />}
    </UsersContext.Provider>
  )
}
