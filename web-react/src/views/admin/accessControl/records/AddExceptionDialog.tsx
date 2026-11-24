import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useAddDestException, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { destErrorText } from '../destErrors'

const E = 'admin:access_control.records.exception.'

/** One native radio with its words; native, so the checked state is the browser's and a screen reader reads a radio group. */
function Choice({ name, value, checked, onChange, label, children }: {
  name: string
  value: string
  checked: boolean
  onChange: () => void
  label: string
  children?: ReactNode
}) {
  return (
    <div className="grid gap-1">
      <label className="flex min-h-8 items-center gap-2 text-sm">
        <input type="radio" name={name} value={value} checked={checked} onChange={onChange} className="size-4 accent-primary" />
        {label}
      </label>
      {children && <div className="grid gap-1 pl-6">{children}</div>}
    </div>
  )
}

export interface ExceptionTarget {
  /** The destination host of the hit (no port). */
  host: string
  /** The account that hit it; null when it is gone. */
  user: { id: number; upn: string } | null
  /** The allowlist group that refused it (a `g<id>` source), when one did: the exception can go to that group alone. */
  group?: { id: number; name: string } | null
  /** A `g<id>` source whose group no longer exists: there is no group to add to, and nothing is chosen for the admin. */
  groupGone?: boolean
}

/**
 * "Allow exception" from a hit (S12): let a destination through for everyone
 * (the GLOBAL exception: the server makes the exceptions list and the allow
 * policy over it the first time, in one transaction), or only exempt the
 * account that hit it. On a hit refused by an allowlist group the default is
 * that group's supplement list (only its members), never everyone. The global
 * choice says what it overrides; the exemption choice hands over to the exemption dialog,
 * which owns the reason and the expiry (S5), instead of copying its form here.
 */
export default function AddExceptionDialog({ target, onClose, onExempt }: {
  target: ExceptionTarget | null
  onClose: () => void
  /** The admin chose "exempt only this account": open the exemption dialog for it. */
  onExempt: (userId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const add = useAddDestException(scope)
  const status = useDestStatus(scope, { enabled: target !== null })
  const [match, setMatch] = useState<'site' | 'host'>('site')
  const [choice, setChoice] = useState<'global' | 'exempt' | 'group' | null>(null)
  const where: 'global' | 'exempt' | 'group' | 'none' = choice ?? (target?.group ? 'group' : target?.groupGone ? 'none' : 'global')
  const setWhere = (v: 'global' | 'exempt' | 'group') => setChoice(v)
  const [error, setError] = useState<string | null>(null)
  const minutes = applyMinutes(status.data)

  const close = () => { setError(null); setChoice(null); setMatch('site'); onClose() }

  const save = async () => {
    if (!target) return
    if (where === 'exempt' && target.user) { onExempt(target.user.id); close(); return }
    setError(null)
    try {
      const r = await add.mutateAsync({ target: target.host, match, ...(where === 'group' && target.group ? { group_id: target.group.id } : {}) })
      pushSnack(where === 'group' && target.group
        ? t(`${E}saved_group`, { group: target.group.name, entry: r.entry })
        : t(r.created ? `${E}saved_created` : `${E}saved`, { entry: r.entry }), 'success')
      close()
    } catch (err) {
      setError(destErrorOf(err)?.code === 'dest_exception_invalid' ? t(`${E}invalid`) : destErrorText(t, err))
    }
  }

  return (
    <Dialog open={target !== null} onOpenChange={open => { if (!open) close() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader><DialogTitle>{t(`${E}title`, { dest: target?.host ?? '' })}</DialogTitle></DialogHeader>
        <div className="grid gap-4">
          <fieldset className="grid gap-1" aria-label={t(`${E}where`)}>
            <legend className="mb-1 text-sm font-semibold">{t(`${E}where`)}</legend>
            {where === 'none' && <p className="text-sm text-destructive">{t(`${E}group_gone`)}</p>}
            {target?.group && (
              <Choice name="where" value="group" checked={where === 'group'} onChange={() => setWhere('group')} label={t(`${E}group`, { group: target.group.name })} />
            )}
            <Choice name="where" value="global" checked={where === 'global'} onChange={() => setWhere('global')} label={t(`${E}global`)}>
              {where === 'global' && (
                <>
                  <FieldHint summary={t(`${E}global_hint.summary`)} detail={t(`${E}global_hint.detail`)} />
                  <span className="text-xs text-muted-foreground">{t(`${E}first_time`)}</span>
                </>
              )}
            </Choice>
            {target?.user && (
              <Choice name="where" value="exempt" checked={where === 'exempt'} onChange={() => setWhere('exempt')} label={t(`${E}exempt`, { upn: target.user.upn })}>
                {where === 'exempt' && <FieldHint summary={t(`${E}exempt_hint.summary`)} detail={t(`${E}exempt_hint.detail`)} />}
              </Choice>
            )}
          </fieldset>
          {(where === 'global' || where === 'group') && (
            <fieldset className="grid gap-1" aria-label={t(`${E}match`)}>
              <legend className="mb-1 text-sm font-semibold">{t(`${E}match`)}</legend>
              <div className="flex flex-wrap gap-x-4">
                <Choice name="match" value="site" checked={match === 'site'} onChange={() => setMatch('site')} label={t('admin:access_control.test.exception.site')} />
                <Choice name="match" value="host" checked={match === 'host'} onChange={() => setMatch('host')} label={t('admin:access_control.test.exception.host')} />
              </div>
            </fieldset>
          )}
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          {where !== 'exempt' && where !== 'none' && (
            <p className="text-xs text-muted-foreground">{minutes === null ? t(`${E}eta_unknown`) : t(`${E}eta`, { count: minutes })}</p>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={close}>{t(`${E}cancel`)}</Button>
          <AsyncButton disabled={where === 'none'} onClick={save}>{where === 'exempt' ? t(`${E}exempt_action`) : t(`${E}allow`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
