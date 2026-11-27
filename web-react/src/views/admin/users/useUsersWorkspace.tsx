import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import type { ListResponse, User } from '@/api/types'
import { useObjectLink } from '@/components/common/objectReturn'
import { useDrawerParam } from '@/hooks/useDrawerParam'
import { useAllGroups } from '@/query/groups'
import { userKeys } from '@/query/keys'
import { useRiskLevels } from '@/query/riskCenter'
import { useTopTraffic } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { useAuthStore } from '@/stores/auth'
import { useCan } from '@/utils/permissions'
import type { UsersCtx } from './context'
import type { DialogState } from './dialogs'
import { userPath } from './userRoutes'
import { useUserActions } from './useUserActions'

const NO_GROUPS: never[] = []

/**
 * Everything the Users list and a user's page have in common: the groups, the usage leaderboard, the risk levels,
 * the single-user actions with their dialogs, the `?risk=` drawer, and the context the tabs and row cells read.
 * Each page calls it once, so a page is mounted on its own (a test, a route) with nothing above it.
 *
 * A write re-reads whatever users query is on screen (`reload`), rather than the list's own refetch: the list and a
 * user's page are different pages now, and each must see its own data move.
 */
export function useUsersWorkspace({ usageLimit = 25, afterDelete }: {
  /** How many users the usage leaderboard is asked for; the list passes its page size so a whole page has figures. */
  usageLimit?: number
  /** The account is gone: the page that was open on it leaves. */
  afterDelete?: (id: number) => void
} = {}) {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const link = useObjectLink()
  const auth = useAuthStore()
  const admin = useCan('config.write')
  const canElevate = useCan('users.elevate')
  // The risk center is admin-only: an operator is shown neither its column nor its drawer, and never reads /levels, which would answer 403.
  const canRisk = useCan('risk.view')
  // The risk center's drawer over the page (`?risk=<id>`): a copied link reopens it and Back closes it. It keeps every other param, so the page (and its tab) is what Back returns to.
  const riskDrawer = useDrawerParam('risk')

  const groups = useAllGroups(scope).data ?? NO_GROUPS
  // The risk column: ONE read for the whole list, keyed by account id. Decoration only: a failed read leaves it blank and the page works as before.
  const levels = useRiskLevels(scope, { enabled: canRisk }).data
  // Per-user period usage, from the Top-N leaderboard through the shared cache. It walks every user server-side, so it never rides a list refresh.
  const top = useTopTraffic(scope, Math.max(usageLimit, 25))
  const usage = useMemo(() => new Map((top.data ?? []).map(r => [r.user_id, r.period_used_bytes])), [top.data])
  const usedBytesOf = useCallback((id: number) => usage.get(id), [usage])

  const [dialog, setDialog] = useState<DialogState | null>(null)

  const reload = useCallback(() => queryClient.invalidateQueries({ queryKey: userKeys.all(scope) }), [queryClient, scope])
  const patchRow = useCallback((u: User) => {
    queryClient.setQueriesData<ListResponse<User>>({ queryKey: userKeys.lists(scope) }, prev => (prev ? { ...prev, items: prev.items.map(x => (x.id === u.id ? u : x)) } : prev))
    queryClient.setQueryData(userKeys.detail(scope, u.id), u)
  }, [queryClient, scope])

  const act = useUserActions({ reload, setDialog, afterDelete: id => afterDelete?.(id) })

  const ctx: UsersCtx = {
    act, groups, usedBytesOf, levels, selfId: auth.userId, role: auth.role, canElevate, canRisk, admin, reload, patchRow,
    openRisk: id => riskDrawer.open(id),
    openUser: (id, tab) => { const l = link(userPath(id, tab)); void navigate(l.to, { state: l.state }) },
    setDialog,
  }
  return { ctx, dialog, setDialog, riskDrawer, canRisk }
}
