import { setEnabled } from '@/api/users'
import type { User } from '@/api/types'
import { pushSnack } from '@/components/feedback/toasts'
import type { DialogState } from './dialogs'
import { PasskeysDialog } from './PasskeysDialog'
import { PasswordDialog } from './PasswordDialog'
import { ReasonDialog } from './ReasonDialog'
import { ReconcileDialog } from './ReconcileDialog'
import { RulesDialog } from './RulesDialog'
import { SecretDialog } from './SecretDialog'
import { useAdminT } from './useAdminT'

/** Mounts the one open short task or confirmation. Each is mounted when opened and gone when closed, so none keeps a draft from the last time. */
export function UserDialogs({ state, setState, reload, toggleBatch }: {
  state: DialogState | null
  setState: (s: DialogState | null) => void
  reload: () => Promise<unknown>
  /** The bulk bar's enable / disable, run after the reason dialog closed. */
  toggleBatch: (rows: User[], enable: boolean, reason: string) => Promise<void>
}) {
  const t = useAdminT()
  const close = () => setState(null)
  if (!state) return null

  switch (state.kind) {
    case 'reason': {
      const { users, enable, batch } = state
      return (
        <ReasonDialog
          subject={batch ? { count: users.length } : { upn: users[0].upn }}
          enable={enable}
          onClose={close}
          onSubmit={async reason => {
            // A batch shows its progress in the bulk bar, so its dialog closes at once; one account's stays until it is answered.
            if (batch) { void toggleBatch(users, enable, reason); return true }
            try {
              await setEnabled(users[0].id, enable, reason.trim() || undefined)
              pushSnack(t(enable ? 'users.toast.enabled' : 'users.toast.disabled'), 'success')
              await reload()
              return true
            } catch { return false }
          }}
        />
      )
    }
    case 'password':
      return <PasswordDialog user={state.user} onClose={close} onDone={password => setState({ kind: 'secret', user: state.user, password, created: false })} />
    case 'rules':
      return <RulesDialog user={state.user} onClose={close} />
    case 'passkeys':
      return <PasskeysDialog user={state.user} onClose={close} />
    case 'secret':
      return <SecretDialog user={state.user} password={state.password} created={state.created} onClose={close} />
    case 'reconcile':
      return <ReconcileDialog report={state.report} onClose={close} />
  }
}
