import { useTranslation } from 'react-i18next'
import type { NodeNetworkInterface, NodeNetworkInterfaces } from '@/api/nodeMetrics'
import { CopyText } from '@/components/common/CopyButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { isIPv6 } from '../addresses'

/** The operational states worth their own word; anything else on an enabled link reads as up (tun and wg say "unknown"). */
const DOWN_STATES = new Set(['down', 'lowerlayerdown', 'dormant', 'notpresent', 'testing'])

export function interfaceState(i: Pick<NodeNetworkInterface, 'up' | 'operational_state'>): { key: string; tone: Tone } {
  if (!i.up) return { key: 'disabled', tone: 'neutral' }
  if (DOWN_STATES.has(i.operational_state)) return { key: `state_${i.operational_state}`, tone: 'warning' }
  return { key: 'up', tone: 'success' }
}

/** The compact line: the first IPv4 and the first IPv6, and how many more sit behind "+N". */
export function interfaceHeadline(addresses: string[]): { shown: string[]; more: number } {
  const shown = [addresses.find(a => !isIPv6(a)), addresses.find(isIPv6)].filter((a): a is string => !!a)
  return { shown, more: addresses.length - shown.length }
}

function Addresses({ i, reported }: { i: NodeNetworkInterface; reported: boolean }) {
  const { t } = useTranslation('admin')
  if (!reported) return <span className="text-muted-foreground">—</span>
  if (i.addresses.length === 0) {
    return <span className="text-muted-foreground">{t(i.addresses_truncated ? 'admin:servers.interfaces.truncated' : 'admin:servers.interfaces.no_address')}</span>
  }
  const { shown, more } = interfaceHeadline(i.addresses)
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={200}
        aria-label={t('admin:servers.interfaces.open', { name: i.name, count: i.addresses.length })}
        className="flex min-w-0 max-w-full items-center gap-1 rounded-sm text-left font-mono text-xs outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        {shown.map((a, n) => (
          <span key={a} className={isIPv6(a) ? 'min-w-0 truncate' : 'shrink-0'}>
            {n > 0 && <span aria-hidden className="mr-1 font-sans text-muted-foreground">·</span>}{a}
          </span>
        ))}
        {more > 0 && <span className="shrink-0 font-sans text-muted-foreground">+{more}</span>}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto min-w-56 max-w-sm">
        <ul className="flex flex-col gap-1">
          {i.addresses.map(a => <li key={a}><CopyText value={a} name={a} className="min-w-0" /></li>)}
        </ul>
        {i.addresses_truncated && <p className="mt-2 text-xs text-muted-foreground">{t('admin:servers.interfaces.truncated')}</p>}
      </PopoverContent>
    </Popover>
  )
}

/**
 * Every interface of the node's latest host sample: name, whether it is up, which family's default route it carries,
 * and its addresses (public and private, with prefix length). One line each; the full address list is on hover or
 * keyboard focus. A node that does not report addresses (older version, or a failed read) shows a dash and says why,
 * so an empty list is never mistaken for "no address".
 */
export function InterfaceList({ network }: { network?: NodeNetworkInterfaces }) {
  const { t } = useTranslation('admin')
  const interfaces = network?.interfaces ?? []
  if (interfaces.length === 0) return <p className="text-sm text-muted-foreground">{t('admin:servers.interfaces.empty')}</p>
  const reported = network?.addresses_reported ?? false
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ul className="divide-y rounded-lg border" aria-label={t('admin:servers.interfaces.title')}>
        {interfaces.map(i => {
          const state = interfaceState(i)
          return (
            <li key={`${i.index}-${i.name}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
              <span className="w-28 shrink-0 truncate font-mono text-xs" title={i.name}>{i.name}</span>
              <StatusBadge tone={state.tone} label={t(`admin:servers.interfaces.${state.key}`)} quiet={state.tone === 'success'} />
              {i.is_default_ipv4 && <span className="text-xs text-muted-foreground">{t('admin:servers.interfaces.default_v4')}</span>}
              {i.is_default_ipv6 && <span className="text-xs text-muted-foreground">{t('admin:servers.interfaces.default_v6')}</span>}
              <div className="flex min-w-0 flex-1 basis-48 justify-end"><Addresses i={i} reported={reported} /></div>
            </li>
          )
        })}
      </ul>
      {!reported && <p className="text-xs text-muted-foreground">{t('admin:servers.interfaces.not_reported')}</p>}
    </div>
  )
}
