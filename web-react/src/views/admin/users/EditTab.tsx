import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { User } from '@/api/types'
import { getLimitEnforcement } from '@/api/limitEnforcement'
import { setUserTraffic } from '@/api/traffic'
import { previewGroupChange, setEnabled, updateUser, type GroupChangePreview } from '@/api/users'
import { AsyncButton } from '@/components/common/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Alert, AlertAction, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { previewHint, useDraftPreview } from '@/hooks/useDraftPreview'
import { useAllServers } from '@/query/nodes'
import { userKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useAuthStore } from '@/stores/auth'
import { accountEnabledForEdit } from '@/utils/userAccess'
import { useCapHints } from './capHints'
import { useUsers } from './context'
import { draftOfUser, periodUsedMoved, updateRequest } from './draft'
import { EmergencyWindow, emergencyActive } from './emergency'
import { bytesToGB, rowFingerprint } from './model'
import { PreviewNote } from './PreviewNote'
import { UserForm } from './UserForm'
import { useAdminT } from './useAdminT'
import { useDraftForm } from './useDraftForm'

/** The emergency-access block of the Access section: how often it was used, a way to reset the count, and the open window if there is one. */
function EmergencyBlock({ user }: { user: User }) {
  const t = useAdminT()
  const { act } = useUsers()
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center gap-3">
        <p className="min-w-0 flex-1 text-sm">{t('users.field.emergency_used_count', { count: user.emergency_used_count })}</p>
        <AsyncButton variant="outline" size="sm" disabled={user.emergency_used_count === 0 && !emergencyActive(user)} onClick={() => act.resetEmergency(user)}>
          {t('users.more_menu.reset_emergency')}
        </AsyncButton>
      </div>
      <EmergencyWindow user={user} />
    </div>
  )
}

/**
 * The profile form of one existing user. The save is several sequential requests (profile, then the usage
 * baseline if it moved, then the account switch if it flipped), so when a later one fails the earlier ones have
 * already been stored: the list is re-read and the form stays open with the draft, to retry.
 */
export function EditTab({ user, onDirtyChange }: { user: User; onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { groups, usedBytesOf, selfId, role, admin, reload, patchRow, act } = useUsers()
  const auth = useAuthStore()
  const used = usedBytesOf(user.id)
  const base = useMemo(() => draftOfUser(user, bytesToGB(used ?? 0)), [user, used])
  const form = useDraftForm(base, 'edit')
  const [pending, setPending] = useState(false)
  // The stored row as it was when the first edit began: compared with the live row so the form can say "this
  // changed" instead of silently leaving the admin editing a stale snapshot. Surfaced, never acted on.
  const [seen, setSeen] = useState<string | null>(null)
  const moved = form.edited && seen !== null && rowFingerprint(user) !== seen

  useEffect(() => { onDirtyChange(form.dirty) }, [form.dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  // What moving to the drafted group would change (ADR 0042 section 3), asked once typing pauses.
  const draftGroup = form.draft.group_id
  const groupKey = typeof draftGroup === 'number' && draftGroup !== user.group_id ? JSON.stringify([user.id, draftGroup]) : null
  const change = useDraftPreview<GroupChangePreview>(groupKey, signal => previewGroupChange(user.id, draftGroup as number, signal))
  const sep = t('nodes.line_labels.group_separator')

  // The fleet-wide cap hints need the servers and the per-user ones need the cap facts; both are admin reads, so an operator gets neither (and no 403).
  const servers = useAllServers(scope, { enabled: admin })
  const facts = useQuery({ queryKey: [...userKeys.detail(scope, user.id), 'limit-enforcement'], queryFn: () => getLimitEnforcement(user.id), enabled: admin, retry: false })
  const hints = useCapHints(servers.data ?? [], facts.data ?? null)

  async function save() {
    if (form.hasProblems) { form.tryAgain(); return }
    const d = form.draft
    if (user.id === selfId && user.role === 'admin' && d.role === 'user') {
      const ok = await confirm({
        title: t('users.confirm.demote_title'),
        message: t('users.validate.demote_self'),
        destructive: true,
        initialFocus: 'cancel',
      })
      if (!ok) return
    }
    setPending(true)
    try {
      let saved = await updateUser(user.id, updateRequest(d, user))
      // Only when the admin actually moved the usage box.
      if (periodUsedMoved(d)) {
        await setUserTraffic(user.id, d.period_used)
        // The leaderboard is a shared cached read, so drop it rather than patching a local copy the next refresh would contradict.
        act.invalidateUsage()
      }
      // The account switch rides its own endpoint (updateUser has no `enabled`), only when flipped, and never for your own account, which would lock you out.
      if (d.enabled !== accountEnabledForEdit(user) && user.id !== selfId) {
        await setEnabled(user.id, d.enabled)
        saved = { ...saved, enabled: d.enabled }
      }
      patchRow(saved)
      if (user.id === selfId) { auth.setDisplayName(d.display_name || ''); auth.setUPN(saved.upn) }
      pushSnack(t('users.toast.saved'), 'success')
      form.reset(); setSeen(null)
    } catch {
      // The request layer already toasted it; re-read so the list shows the true post-failure state, and keep the draft.
      await reload()
    } finally { setPending(false) }
  }

  const patch: typeof form.patch = p => {
    if (!form.edited) setSeen(rowFingerprint(user))
    form.patch(p)
  }

  return (
    <FormValidityProvider>
      <form className="flex flex-col gap-4" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
        {moved && (
          <Alert>
            <AlertDescription>{t('users.edit.row_changed')}</AlertDescription>
            <AlertAction>
              <Button size="sm" variant="outline" disabled={pending} onClick={() => { form.reset(); setSeen(null) }}>{t('users.edit.reload_saved')}</Button>
            </AlertAction>
          </Alert>
        )}
        <UserForm
          mode="edit"
          draft={form.draft}
          patch={patch}
          error={form.error}
          blur={form.blur}
          groups={groups}
          hints={hints}
          groupNote={groupKey && (
            <PreviewNote testId="group-change-preview" hint={previewHint(change, d => [...d.added.map(n => `+${n.name}`), ...d.removed.map(n => `-${n.name}`)])}
              ready={() => change.status === 'ready'
                ? t('users.group_preview.change', {
                  added: change.data.added.length, removed: change.data.removed.length,
                  added_names: change.data.added.map(n => n.name).join(sep) || '—',
                  removed_names: change.data.removed.map(n => n.name).join(sep) || '—',
                }) : ''}
              none={t('users.group_preview.same')} unknown={t('users.group_preview.unknown')} />
          )}
          roleLocked={role === 'operator'}
          selfAccount={user.id === selfId}
          disabledReason={user.auto_disabled_reason}
          emergency={<EmergencyBlock user={user} />}
        />
        <SaveBar dirty={form.dirty} pending={pending} onSave={() => void save()} onDiscard={() => { form.reset(); setSeen(null) }} />
      </form>
    </FormValidityProvider>
  )
}
