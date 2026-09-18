import { queryOptions, useQuery } from '@tanstack/react-query'
import {
  listACMEAccounts,
  listCertEvents,
  listCerts,
  listDNSCreds,
  type ACMEAccount,
  type Cert,
  type CertEvent,
  type DNSCredential,
} from '@/api/certs'
import { certKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

export interface CertEventPage {
  events: CertEvent[]
  total: number
}

/**
 * Certificate issuance/renewal activity. Through the cache a failed read is a
 * value the tab can see — previously the loader swallowed it and the table
 * rendered the empty state, which reads as "nothing was ever issued".
 */
export function certEventsQuery(scope: QueryScope, page: number, pageSize: number) {
  return queryOptions({
    queryKey: certKeys.events(scope, page, pageSize),
    queryFn: ({ signal }): Promise<CertEventPage> => listCertEvents(page, pageSize, { signal }),
    ...freshness(policies.certEvents),
  })
}

export function useCertEvents(scope: QueryScope, page: number, pageSize: number) {
  return useQuery(certEventsQuery(scope, page, pageSize))
}

/** The Certificates page's certificate list. */
export function certsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: certKeys.list(scope),
    queryFn: ({ signal }): Promise<Cert[]> => listCerts({ signal }),
    ...freshness(policies.certList),
  })
}

export function useCerts(scope: QueryScope) {
  return useQuery(certsQuery(scope))
}

/** DNS credentials used by the DNS-01 challenge. */
export function dnsCredsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: certKeys.creds(scope),
    queryFn: ({ signal }): Promise<DNSCredential[]> => listDNSCreds({ signal }),
    ...freshness(policies.certList),
  })
}

export function useDNSCreds(scope: QueryScope) {
  return useQuery(dnsCredsQuery(scope))
}

/** ACME accounts (one per CA/environment). */
export function acmeAccountsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: certKeys.accounts(scope),
    queryFn: ({ signal }): Promise<ACMEAccount[]> => listACMEAccounts({ signal }),
    ...freshness(policies.certList),
  })
}

export function useACMEAccounts(scope: QueryScope) {
  return useQuery(acmeAccountsQuery(scope))
}
