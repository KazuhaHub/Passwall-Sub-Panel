import type { TFunction } from 'i18next'
import type { Cert } from '@/api/certs'
import { StatusBadge } from '@/components/common/StatusBadge'
import { TimeCell } from '../logs/shared'
import { useCertsT } from './shared'
import type { CertHealth } from './model'

/** Why a certificate is in the state it is in, in a sentence; null when there is nothing to add (a healthy one). */
export function healthReason(t: TFunction, c: Pick<Cert, 'auto_renew' | 'last_error'>, h: CertHealth): string | null {
  const days = h.days
  switch (h.id) {
    case 'failed': return c.last_error || t('certs.reason.failed_unknown')
    case 'expired': return days === null || days >= 0 ? t('certs.reason.expired_today') : t('certs.reason.expired', { count: -days })
    case 'expiring': {
      const key = c.auto_renew ? 'auto' : 'manual'
      return days === null || days <= 0 ? t(`certs.reason.expiring_today_${key}`) : t(`certs.reason.expiring_${key}`, { count: days })
    }
    case 'issuing': return t('certs.reason.issuing')
    default: return null
  }
}

/**
 * The state in words, and the reason under it. A healthy certificate is a quiet dot; every other state is a
 * badge that says why (a failure shows the CA's own message), never colour alone.
 */
export function HealthCell({ cert, health, reason = true }: { cert: Cert; health: CertHealth; reason?: boolean }) {
  const t = useCertsT()
  const text = reason ? healthReason(t, cert, health) : null
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <StatusBadge tone={health.tone} label={t(`certs.health.${health.id}`)} />
      {text && <span className="line-clamp-2 text-xs break-words whitespace-normal text-muted-foreground" title={text}>{text}</span>}
    </div>
  )
}

/** The end date in the panel's time zone (absolute time with the browser's in `title`), and how far away it is. */
export function ExpiryCell({ cert, health }: { cert: Cert; health: CertHealth }) {
  const t = useCertsT()
  if (!cert.not_after) return <span className="text-muted-foreground">—</span>
  const days = health.days
  const away = days === null ? null : days > 0 ? t('certs.in_days', { count: days }) : days === 0 ? t('certs.today') : t('certs.days_ago', { count: -days })
  return (
    <div className="flex flex-col">
      <TimeCell value={cert.not_after} />
      {away && <span className="text-xs text-muted-foreground">{away}</span>}
    </div>
  )
}

/** The first domain and how many more there are; the whole list is in `title` and in the sheet. */
export function DomainsCell({ domains }: { domains: readonly string[] }) {
  const t = useCertsT()
  if (domains.length === 0) return <span className="text-muted-foreground">—</span>
  const rest = domains.length - 1
  return (
    <span className="block truncate font-mono text-[0.8125rem]" title={domains.join(', ')}>
      {domains[0]}
      {rest > 0 && <span className="font-sans text-muted-foreground"> {t('certs.used_more', { count: rest })}</span>}
    </span>
  )
}
