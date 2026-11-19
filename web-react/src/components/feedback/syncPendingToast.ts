import i18n from '@/i18n'

import { pushToast } from './toasts'

// THE X-Sync-Pending TOAST. The backend answers a write with that header when the
// change was saved but the push to the upstream server had to be queued for a
// retry (sync_task rows, replayed every minute). api/client.ts decides WHEN to
// show this (the header, the throttle, "a status area for this user is already on
// screen"); this module only says it. It goes through the same toast store as every
// other toast: it counts toward the visible cap, is announced like any warning, and a
// burst of writes is one toast with a counter (the stable key).
//
// It links to the sync tasks page, which the staff shell can open and the user
// shell cannot, so the link comes from whoever can honour it: the staff shell
// registers a navigator, and with none registered the toast is just the message.
// (A plain location change would reload the SPA and drop whatever the admin was
// in the middle of; the router's own navigate does not.)

export const SYNC_TASKS_ROUTE = '/admin/sync-tasks'

let navigator: ((to: string) => void) | null = null

/** Registers the function that opens a route; returns the unregister. */
export function setSyncPendingNavigator(fn: (to: string) => void): () => void {
  navigator = fn
  return () => { if (navigator === fn) navigator = null }
}

export function notifySyncPending(): void {
  const go = navigator
  pushToast({
    message: i18n.t('common:ui.feedback.sync_pending'),
    severity: 'warning',
    key: 'sync-pending',
    action: go ? { label: i18n.t('common:ui.feedback.sync_pending_action'), onClick: () => go(SYNC_TASKS_ROUTE) } : undefined,
  })
}
