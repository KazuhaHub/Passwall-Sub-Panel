import { createContext, useContext } from 'react'
import type { Group, Role, User } from '@/api/types'
import type { DialogState } from './dialogs'
import type { UserActions } from './useUserActions'
import type { SheetTab } from './useUserSheet'

/** What the sheet's tabs share with the page, so none of them is passed ten props. */
export interface UsersCtx {
  act: UserActions
  groups: readonly Group[]
  /** Period usage in bytes from the leaderboard; undefined for a user who is not in it (unknown, not zero). */
  usedBytesOf: (userId: number) => number | undefined
  /** This administrator's own account and role. */
  selfId: number | null
  role: Role | ''
  /** May act on admin and operator accounts. */
  canElevate: boolean
  /** May open the risk center (admins). */
  canRisk: boolean
  /** Admins only: reads the risk center, the servers and the per-user cap facts, which an operator's token cannot. */
  admin: boolean
  /** Re-reads the list (and the opened user) after a write. */
  reload: () => Promise<unknown>
  /** Puts a saved row into the cached list and detail, so nothing shows the stale one while a refetch is under way. */
  patchRow: (u: User) => void
  openRisk: (userId: number) => void
  /** Opens the sheet on a user, on a tab. */
  openUser: (userId: number, tab?: SheetTab) => void
  setDialog: (d: DialogState) => void
}

export const UsersContext = createContext<UsersCtx | null>(null)

export function useUsers(): UsersCtx {
  const ctx = useContext(UsersContext)
  if (!ctx) throw new Error('useUsers must be used inside the Users page')
  return ctx
}
