import type { User } from '@/api/types'
import { CopyButton } from '@/components/common/CopyButton'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useAdminT } from './useAdminT'

/**
 * What an admin must record, shown once: the initial password of a new account, or the one a reset set. The
 * password is shown as it is (it has to be read out or typed somewhere), the subscription link masked until asked
 * for, like everywhere else a link is shown to staff.
 */
export function SecretDialog({ user, password, created, onClose }: { user: User; password: string; created: boolean; onClose: () => void }) {
  const t = useAdminT()
  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(created ? 'users.result_title' : 'users.result.reset_title')}</DialogTitle>
          <DialogDescription>{t(created ? 'users.result.intro' : 'users.result.reset_intro', { upn: user.upn })}</DialogDescription>
        </DialogHeader>
        <Field label={t('users.result.password_label')}>
          <div className="flex items-center gap-2 rounded-lg border bg-muted/40 py-1 pr-1 pl-3">
            <code className="min-w-0 flex-1 font-mono text-sm break-all select-all">{password}</code>
            <CopyButton value={password} name={t('users.result.password_label')} />
          </div>
        </Field>
        {user.sub_url && (
          <Field label={t('users.result.sub_url_label')}>{p => <SecretField id={p.id} readOnly value={user.sub_url} />}</Field>
        )}
        <DialogFooter>
          <Button onClick={onClose}>{t('common:actions.ok')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
