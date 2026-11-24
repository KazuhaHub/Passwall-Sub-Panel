import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { RiskLevels } from '@/api/riskCenter'
import type { Role, User } from '@/api/types'
import { Meter } from '@/components/common/Meter'
import { useObjectLink } from '@/components/common/objectReturn'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { accountAxis, serviceAxis, type Axis } from './status'
import { bytesToGB } from './model'
import { userPath } from './userRoutes'
import { useAdminT } from './useAdminT'

/** One axis as words: a healthy state is a quiet dot, anything else a badge that says why on hover. */
export function AxisBadge({ axis, className }: { axis: Axis; className?: string }) {
  const t = useAdminT()
  const label = t(axis.label, { defaultValue: axis.label })
  const why = [axis.explain && t(axis.explain, axis.vars), axis.detail].filter(Boolean).join(' · ')
  return (
    <div className={cn('flex max-w-full min-w-0', className)} title={why || label}>
      <StatusBadge tone={axis.tone} label={label} className="min-w-0 max-w-full" />
    </div>
  )
}

/**
 * Both switches, one under the other: the account (signing in) and the service (the subscription). They are
 * independent, so neither stands in for the other: an over-quota user reads "account active" and "traffic
 * exhausted", never a single "disabled".
 */
export function StatusCell({ user }: { user: User }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <AxisBadge axis={accountAxis(user)} />
      <AxisBadge axis={serviceAxis(user)} />
    </div>
  )
}

export function RoleBadge({ role }: { role: Role }) {
  const t = useAdminT()
  return <Badge variant={role === 'admin' ? 'default' : role === 'operator' ? 'secondary' : 'outline'}>{t(`users.role.${role}`)}</Badge>
}

/** The SSO binding: "saml" / "oidc", nothing for a local account. The title carries the provider and subject, so an admin can check which IdP identity a row is bound to before unlinking. */
export function SsoBadge({ user }: { user: User }) {
  const provider = user.sso_provider || 'local'
  if (provider === 'local') return null
  const protocol = provider.split(':', 1)[0]
  return <Badge variant="secondary" title={`${provider}${user.sso_subject ? `  (${user.sso_subject})` : ''}`}>{protocol.toUpperCase()}</Badge>
}

/**
 * Who: the login name first, the display name under it; the role only when it is not the ordinary one. The login
 * name is a link to the user's page (so it opens in a new tab like any link), carrying the list's place for the back link.
 */
export function UserCell({ user }: { user: User }) {
  const link = useObjectLink()
  return (
    <div className="flex min-w-0 flex-col">
      <div className="flex min-w-0 items-center gap-1.5">
        <Link {...link(userPath(user.id))} className="min-w-0 truncate font-medium underline-offset-2 hover:underline focus-visible:underline" title={user.upn}>{user.upn}</Link>
        {user.role !== 'user' && <RoleBadge role={user.role} />}
        <SsoBadge user={user} />
      </div>
      {user.display_name && <span className="truncate text-xs text-muted-foreground" title={user.display_name}>{user.display_name}</span>}
    </div>
  )
}

/** The bar of a user's period usage. The numbers beside it say the same, so it is decoration for the eye: amber from 90%, red at the limit. */
export function UsageMeter({ used, limit, className }: { used: number; limit: number; className?: string }) {
  const ratio = limit > 0 ? used / limit : 0
  return <Meter value={used} max={limit} className={cn('h-1', className)} barClassName={ratio >= 1 ? 'bg-destructive' : ratio >= 0.9 ? 'bg-warning' : 'bg-brand'} />
}

/**
 * Used / limit for the current period, with a compact meter. The usage comes from the Top-N leaderboard, so a
 * user who is not in it has UNKNOWN usage ("—"), not zero: a 0 would understate a real figure and read as a fact.
 */
export function TrafficCell({ user, usedBytes }: { user: User; usedBytes: number | undefined }) {
  const t = useAdminT()
  const limit = bytesToGB(user.traffic_limit_bytes)
  const used = usedBytes === undefined ? null : bytesToGB(usedBytes)
  if (limit === 0) {
    return <span className="whitespace-nowrap text-muted-foreground tabular-nums">{used ?? '—'} GB / {t('users.status.unlimited')}</span>
  }
  const over = used !== null && used >= limit
  return (
    <div className="flex min-w-0 flex-col items-end gap-1">
      <span className={cn('whitespace-nowrap tabular-nums', over && 'font-medium text-destructive')}>{used ?? '—'} / {limit} GB</span>
      <UsageMeter used={used ?? 0} limit={limit} className="w-20" />
    </div>
  )
}

/** Permanent, a date, or how soon: the last week and the past are badges, because they need a look. */
export function ExpiryCell({ user }: { user: User }) {
  const t = useAdminT()
  if (!user.expire_at) return <span className="text-muted-foreground">{t('users.status.permanent')}</span>
  const days = Math.ceil((new Date(user.expire_at).getTime() - Date.now()) / 86400000)
  if (days < 0) return <StatusBadge tone="danger" label={t('users.status.expired_days', { days: Math.abs(days) })} />
  if (days === 0) return <StatusBadge tone="danger" label={t('users.status.today')} />
  if (days <= 7) return <StatusBadge tone="warning" label={t('users.status.expiring_soon', { days })} />
  // The panel-time-zone calendar day is the authoritative one; the browser's rendering is the fallback.
  return <span className="whitespace-nowrap tabular-nums">{user.expire_date ?? new Date(user.expire_at).toLocaleDateString()}</span>
}

/** A short relative time ("5m ago"); the exact instant, in both zones, is the title. Never seen reads as "—". */
export function LastSeenCell({ value }: { value?: string | null }) {
  const t = useAdminT()
  const tz = useSiteStore(s => s.timezone)
  if (!value) return <span className="text-muted-foreground">—</span>
  return <time dateTime={value} title={formatDualTz(value, tz)} className="whitespace-nowrap">{formatRelativeTimeShort(Date.now() - new Date(value).getTime(), t)}</time>
}

/**
 * The risk center's verdict for the account, as ONE chip that opens its drawer: the attention level, or (held
 * with no fresh verdict) "suspended". A dismissal draws it outlined and says so; a trusted account with nothing
 * at attention still shows "trusted", because trust is a standing exemption an admin should see from the list.
 * Any other account: blank. A chip whose word is not the full state names it on hover.
 */
export function RiskCell({ userId, levels, open }: { userId: number; levels: RiskLevels | undefined; open: (id: number) => void }) {
  const t = useAdminT()
  const entry = levels?.[String(userId)]
  if (!entry) return null
  const attention = entry.level !== '' || entry.auto_suspended
  if (!attention && !entry.trusted) return null
  const named = entry.level !== '' ? t(`risk_center.state.${entry.level}`) : t('users.status.geo_auto')
  const dismissed = attention && !entry.open
  const label = !attention
    ? t('risk_center.review.badge_trusted')
    : dismissed ? t('risk_center.review.badge_dismissed')
      : entry.level !== '' ? named : t('risk_center.flags.level.suspended')
  const hot = attention && !dismissed
  const notes = [
    attention && (dismissed || entry.level === '') ? named : '',
    attention && entry.trusted ? t('risk_center.review.badge_trusted') : '',
  ].filter(Boolean)
  const warn = entry.level === 'suspect' && !entry.auto_suspended
  return (
    <Badge
      variant={attention && entry.open ? (warn ? 'secondary' : 'destructive') : 'outline'}
      render={<button type="button" />}
      aria-label={t('users.risk_chip_hint')}
      title={notes.length > 0 ? notes.join(' · ') : t('users.risk_chip_hint')}
      onClick={() => open(userId)}
      className={cn('cursor-pointer', hot && 'font-semibold')}
    >
      {label}
    </Badge>
  )
}

/** A phone-width row: who, the two statuses, and the figures an admin scans. The card opens the user's page. */
export function UserCard({ user, usedBytes, actions }: { user: User; usedBytes: number | undefined; actions: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-0 flex-1"><UserCell user={user} /></div>
        {actions}
      </div>
      <StatusCell user={user} />
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <TrafficCell user={user} usedBytes={usedBytes} />
        <ExpiryCell user={user} />
      </div>
    </div>
  )
}
