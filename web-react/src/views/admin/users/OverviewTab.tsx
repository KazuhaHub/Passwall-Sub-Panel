import { Link } from 'react-router'
import type { User } from '@/api/types'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyText } from '@/components/common/CopyButton'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import SyncStatusCard from '@/components/SyncStatusCard'
import { Button } from '@/components/ui/button'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { accountEnabledForEdit } from '@/utils/userAccess'
import { Block, Fact, Facts } from './blocks'
import { AxisBadge, ExpiryCell, LastSeenCell, Meter, RoleBadge, SsoBadge } from './cells'
import { useUsers } from './context'
import { EmergencyWindow } from './emergency'
import { bytesToGB, canQuickRenew, canResumeService, canSuspendService } from './model'
import { accountAxis, serviceAxis, type Axis } from './status'
import { useAdminT } from './useAdminT'

/** One of the two switches: its state in words, what that means, why when the server said so, and what an admin can do about it. */
function AxisRow({ title, caption, axis, extra, actions }: { title: string; caption: string; axis: Axis; extra?: string[]; actions?: React.ReactNode }) {
  const t = useAdminT()
  const lines = [axis.explain ? t(axis.explain, axis.vars) : '', axis.detail, ...(extra ?? [])].filter(Boolean)
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
        <div className="min-w-0">
          <div className="text-xs text-muted-foreground">{title} · {caption}</div>
          <div className="mt-1"><AxisBadge axis={axis} /></div>
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {lines.length > 0 && <p className="text-sm text-muted-foreground">{lines.join(' ')}</p>}
    </div>
  )
}

/**
 * The first tab: who the account is and whether it works, told per switch. The ACCOUNT switch (signing in to the
 * panel) and the SERVICE switch (the subscription) are independent, so each has its own state and its own
 * actions: disabling the account also turns the subscription off, but an over-quota or expired user can still
 * sign in, and suspending the service leaves the account alone.
 */
export function OverviewTab({ user, manageable }: { user: User; manageable: boolean }) {
  const t = useAdminT()
  const tz = useSiteStore(s => s.timezone)
  const { act, selfId, usedBytesOf, canRisk, openRisk } = useUsers()
  const groupName = useGroupName(user.group_id)
  const accountOn = accountEnabledForEdit(user)
  const self = user.id === selfId
  const used = usedBytesOf(user.id)
  const usedGB = used === undefined ? null : bytesToGB(used)
  const limitGB = bytesToGB(user.traffic_limit_bytes)
  const accountExtra: string[] = []
  const serviceExtra: string[] = []
  if (user.block_violation_count) serviceExtra.push(t('users.field.block_violations', { count: user.block_violation_count }))

  return (
    <div className="flex flex-col gap-4">
      <Block title={t('users.detail.status')} description={t('users.axis.intro')}>
        <AxisRow title={t('users.axis.account_title')} caption={t('users.axis.account_caption')} axis={accountAxis(user)} extra={accountExtra}
          actions={manageable && (
            accountOn
              ? <Button size="sm" variant="outline" aria-disabled={self || undefined} title={self ? t('users.field.account_self_locked') : undefined}
                  className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50" onClick={() => { if (!self) act.askToggleAccount(user, false) }}>{t('users.action.disable')}</Button>
              : <Button size="sm" variant="outline" onClick={() => act.askToggleAccount(user, true)}>{t('users.action.enable')}</Button>
          )} />
        <div className="border-t" />
        <AxisRow title={t('users.axis.service_title')} caption={t('users.axis.service_caption')} axis={serviceAxis(user)} extra={serviceExtra}
          actions={manageable && (
            <>
              {canResumeService(user) && <AsyncButton size="sm" variant="outline" onClick={() => act.resume(user)}>{t('users.more_menu.resume_service')}</AsyncButton>}
              {canSuspendService(user) && <AsyncButton size="sm" variant="outline" onClick={() => act.suspend(user)}>{t('users.more_menu.suspend_service')}</AsyncButton>}
            </>
          )} />
        <EmergencyWindow user={user} />
      </Block>

      <Block title={t('users.detail.usage')}
        actions={<Button size="sm" variant="outline" render={<Link to={`/admin/traffic?tab=trend&scope=user&user=${user.id}`} />}>{t('users.detail.view_usage')}</Button>}>
        {limitGB > 0 && <Meter used={usedGB ?? 0} limit={limitGB} className="h-2" />}
        <Facts>
          <Fact label={t('users.detail.period_used')}><span className="tabular-nums">{usedGB ?? '—'} GB</span></Fact>
          <Fact label={t('users.detail.limit')}><span className="tabular-nums">{limitGB > 0 ? `${limitGB} GB` : t('users.detail.unlimited')}</span></Fact>
          <Fact label={t('users.detail.lifetime')}>
            <span className="tabular-nums">{bytesToGB(user.lifetime_total_bytes ?? 0)} GB</span>
            <span className="ml-2 text-xs text-muted-foreground tabular-nums">↑ {bytesToGB(user.lifetime_up_bytes ?? 0)} GB · ↓ {bytesToGB(user.lifetime_down_bytes ?? 0)} GB</span>
          </Fact>
        </Facts>
      </Block>

      <Block title={t('users.detail.validity')}
        actions={manageable && (
          <AsyncButton size="sm" variant="outline" aria-disabled={!canQuickRenew(user) || undefined} title={canQuickRenew(user) ? undefined : t('users.toast.renew_permanent_skip')}
            className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50" onClick={() => { if (canQuickRenew(user)) return act.renew(user) }}>
            {t('users.action.renew')}
          </AsyncButton>
        )}>
        <Facts>
          <Fact label={t('users.field.expire_mode')}><ExpiryCell user={user} /></Fact>
          <Fact label={t('users.table.last_online')}><LastSeenCell value={user.last_online_at} /></Fact>
        </Facts>
      </Block>

      <Block title={t('users.detail.subscription')}
        actions={manageable && <Button size="sm" variant="outline" onClick={() => act.openRules(user)}>{t('users.more_menu.personal_rules')}</Button>}>
        {user.sub_url
          ? <Field label={t('users.result.sub_url_label')}>{p => <SecretField id={p.id} readOnly value={user.sub_url} />}</Field>
          : <p className="text-sm text-muted-foreground">{t('users.detail.sub_hidden')}</p>}
        <SyncStatusCard userId={user.id} />
      </Block>

      <Block title={t('users.detail.account')}
        // Hands over to the risk center's drawer for this account (the sheet's parameters go, and closing the drawer comes back here). Admins only: an operator's token cannot read the risk center.
        actions={canRisk && <Button size="sm" variant="outline" onClick={() => openRisk(user.id)}>{t('users.risk_lookup')}</Button>}>
        <Facts>
          <Fact label={t('users.field.display_name')} hidden={!user.display_name}>{user.display_name}</Fact>
          <Fact label={t('users.field.email')} hidden={!user.email}>{user.email}</Fact>
          <Fact label={t('users.field.role')}><span className="inline-flex flex-wrap items-center gap-1.5"><RoleBadge role={user.role} /><SsoBadge user={user} /></span></Fact>
          <Fact label={t('users.field.group')}>{groupName}</Fact>
          <Fact label="ID" mono>{user.id}</Fact>
          <Fact label="UUID" mono hidden={!user.uuid}><CopyText value={user.uuid} name="UUID" /></Fact>
          <Fact label={t('users.detail.created_at')}>{user.created_at ? formatDualTz(user.created_at, tz) : '—'}</Fact>
          <Fact label={t('users.field.remark')} hidden={!user.remark}>{user.remark}</Fact>
        </Facts>
      </Block>
    </div>
  )
}

function useGroupName(id: number): string {
  const { groups } = useUsers()
  return groups.find(g => g.id === id)?.name ?? String(id)
}
