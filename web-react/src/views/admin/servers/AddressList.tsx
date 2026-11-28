import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/servers'
import { CopyText } from '@/components/common/CopyButton'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'
import { addressHeadline, serverAddresses, type ServerAddresses } from './addresses'

/** One address with its copy action; the one the panel sees the node connect from is marked in words, not by colour alone. */
function AddressRow({ address, observed }: { address: string; observed: boolean }) {
  const { t } = useTranslation('admin')
  return (
    <li className="flex min-w-0 items-center justify-between gap-3">
      <CopyText value={address} name={address} className="min-w-0" />
      {observed && <span className="shrink-0 text-xs text-muted-foreground">{t('admin:servers.addresses.observed')}</span>}
    </li>
  )
}

function Group({ label, items, observed }: { label: string; items: string[]; observed: string }) {
  if (items.length === 0) return null
  return (
    <section className="flex flex-col gap-1">
      <h3 className="text-xs font-medium text-muted-foreground">{label}</h3>
      <ul className="flex flex-col gap-1">{items.map(a => <AddressRow key={a} address={a} observed={a === observed} />)}</ul>
    </section>
  )
}

/**
 * The complete list, IPv4 then IPv6, each copyable. The observed address is marked where it is in the list; when it is
 * not (the node is seen from an address none of its interfaces carries) it is said on its own line so the operator is
 * never left wondering which address the panel actually reaches.
 */
export function AddressGroups({ addresses }: { addresses: ServerAddresses }) {
  const { t } = useTranslation('admin')
  const listed = addresses.observed !== '' && [...addresses.v4, ...addresses.v6].includes(addresses.observed)
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Group label={t('admin:servers.addresses.ipv4')} items={addresses.v4} observed={addresses.observed} />
      <Group label={t('admin:servers.addresses.ipv6')} items={addresses.v6} observed={addresses.observed} />
      {addresses.observed !== '' && !listed && (
        <section className="flex flex-col gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">{t('admin:servers.addresses.seen_from')}</h3>
          <ul><AddressRow address={addresses.observed} observed={false} /></ul>
        </section>
      )}
    </div>
  )
}

/**
 * The addresses of a server on one line: the first IPv4 and the first IPv6 (clipped with an ellipsis, the full text in
 * the popover), and "+N" for the rest. It is a button, so a keyboard reaches it and Enter opens the full list; a pointer
 * opens it on hover. Renders nothing for a server that reported no addresses: the caller keeps its own fallback.
 */
export function AddressSummary({ s, className }: { s: Pick<Server, 'address' | 'interface_addresses'>; className?: string }) {
  const { t } = useTranslation('admin')
  const addresses = serverAddresses(s)
  if (!addresses.reported) return null
  const { shown, more } = addressHeadline(addresses)
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={200}
        aria-label={t('admin:servers.addresses.open', { count: addresses.v4.length + addresses.v6.length })}
        className={cn('flex w-full min-w-0 items-center gap-1 rounded-sm text-left text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50', className)}
      >
        {shown.map((a, i) => (
          <span key={a} className={cn('font-mono', a.includes(':') ? 'min-w-0 truncate' : 'shrink-0')}>
            {i > 0 && <span aria-hidden className="mr-1 font-sans">·</span>}{a}
          </span>
        ))}
        {more > 0 && <span className="shrink-0 font-sans">+{more}</span>}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto min-w-56 max-w-sm">
        <AddressGroups addresses={addresses} />
      </PopoverContent>
    </Popover>
  )
}
