import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listACMEKeyTypes, listDNSProviders } from '@/api/certs'
import { useACMEAccounts, useCerts, useDNSCreds } from '@/query/certs'
import { certKeys } from '@/query/keys'
import { useAllNodes } from '@/query/nodes'
import { useUISettings } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { DEFAULT_KEY_TYPES, DEFAULT_RENEW_BEFORE_DAYS, nodesByCert } from './model'

/**
 * Everything the page reads, in one place: the three lists it edits, the nodes that say where a
 * certificate is used, the renewal threshold the health words depend on, and the two dictionaries the forms
 * offer (DNS providers, ACME key types). The dictionaries have sensible defaults, so a failed read leaves a
 * field emptier rather than blanking a page whose own data loaded fine.
 */
export function useCertData() {
  const scope = useQueryScope()
  const certs = useCerts(scope)
  const creds = useDNSCreds(scope)
  const accounts = useACMEAccounts(scope)
  // Usage is context, not the page's data: when the nodes cannot be read the list still shows, without it.
  const nodes = useAllNodes(scope)
  const settings = useUISettings(scope)
  const providers = useQuery({ queryKey: [...certKeys.all(scope), 'dns-providers'], queryFn: () => listDNSProviders(), staleTime: 5 * 60_000 })
  const keyTypes = useQuery({ queryKey: [...certKeys.all(scope), 'key-types'], queryFn: () => listACMEKeyTypes(), staleTime: 5 * 60_000 })

  const usage = useMemo(() => nodesByCert(nodes.data ?? []), [nodes.data])
  const readQueries = [certs, creds, accounts]
  return {
    scope,
    certs, creds, accounts, settings, nodes,
    providers: providers.data ?? [],
    keyTypes: keyTypes.data?.length ? keyTypes.data : DEFAULT_KEY_TYPES,
    usage,
    usageKnown: nodes.data !== undefined,
    renewBeforeDays: settings.data?.cert_renew_before_days || DEFAULT_RENEW_BEFORE_DAYS,
    pending: readQueries.some(q => q.isPending),
    failed: readQueries.find(q => q.isError)?.error,
    isFailed: readQueries.some(q => q.isError),
    reload: () => Promise.all([certs.refetch(), creds.refetch(), accounts.refetch(), nodes.refetch()]),
  }
}

export type CertData = ReturnType<typeof useCertData>
