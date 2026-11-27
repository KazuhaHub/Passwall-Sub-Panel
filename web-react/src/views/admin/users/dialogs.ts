import type { ReconcileReport } from '@/api/reconcile'
import type { User } from '@/api/types'

/** The short tasks and confirmations that open over the list or a user's page, one at a time. */
export type DialogState =
  /** Enable or disable the ACCOUNT with an optional reason: one user, or the rows of a batch. */
  | { kind: 'reason'; users: User[]; enable: boolean; batch: boolean }
  | { kind: 'password'; user: User }
  | { kind: 'rules'; user: User }
  | { kind: 'passkeys'; user: User }
  /** What an admin must record: the initial password after create, the new one after a reset. */
  | { kind: 'secret'; user: User; password: string; created: boolean }
  | { kind: 'reconcile'; report: ReconcileReport }
