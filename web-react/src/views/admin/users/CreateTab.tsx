import { useEffect, useMemo, useState } from 'react'
import type { User } from '@/api/types'
import { createUser } from '@/api/users'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { GuardedForm } from './GuardedForm'
import { useCapHints } from './capHints'
import { useUsers } from './context'
import { blankDraft, createRequest } from './draft'
import { dateInNDays } from './model'
import { UserForm } from './UserForm'
import { useAdminT } from './useAdminT'
import { useDraftForm } from './useDraftForm'
import { useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'

/**
 * The create form: the same fields as the edit form, from the blank state (the first group, 30 days of
 * validity). The save hands the new user and the initial password to the page, which goes to the new user's page
 * and shows the one-time result there. The fleet-wide cap hints apply here (the user has no clients yet, so there
 * is nothing per-user to say).
 */
export function CreateTab({ onDirtyChange, onCreated }: { onDirtyChange: (dirty: boolean) => void; onCreated: (user: User, initialPassword: string) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { groups, admin, reload } = useUsers()
  const servers = useAllServers(scope, { enabled: admin })
  const hints = useCapHints(servers.data ?? [], null)
  const base = useMemo(() => blankDraft(groups[0]?.id ?? '', dateInNDays(30)), [groups])
  const form = useDraftForm(base, 'create')
  const [pending, setPending] = useState(false)

  useEffect(() => { onDirtyChange(form.dirty) }, [form.dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  async function save() {
    if (form.hasProblems) { form.tryAgain(); return }
    setPending(true)
    try {
      const res = await createUser(createRequest(form.draft))
      form.reset()
      onCreated(res.user, res.initial_password)
      await reload()
    } catch { /* reported by the request layer; the form stays as it was */ } finally { setPending(false) }
  }

  return (
    <FormValidityProvider>
      <GuardedForm onSubmit={() => void save()}>
        <UserForm mode="create" draft={form.draft} patch={form.patch} error={form.error} blur={form.blur} groups={groups} hints={hints} />
        <SaveBar dirty={form.dirty} pending={pending} saveLabel={t('users.create')} onSave={() => void save()} onDiscard={form.reset} />
      </GuardedForm>
    </FormValidityProvider>
  )
}
