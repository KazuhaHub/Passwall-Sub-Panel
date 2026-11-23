import { useEffect, useMemo, useState } from 'react'
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
 * validity). The save shows the initial password and the subscription link once, in the dialog the page opens.
 * The fleet-wide cap hints apply here (the user has no clients yet, so there is nothing per-user to say).
 */
export function CreateTab({ onDirtyChange, onCreated }: { onDirtyChange: (dirty: boolean) => void; onCreated: () => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { groups, admin, reload, setDialog } = useUsers()
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
      onCreated()
      setDialog({ kind: 'secret', user: res.user, password: res.initial_password, created: true })
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
