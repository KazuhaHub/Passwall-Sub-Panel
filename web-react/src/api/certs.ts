import { client } from './client'

// Cert mirrors the backend certDTO — note it NEVER carries the private key.
export interface Cert {
  id: number
  name: string
  domains: string[]
  status: string // pending | active | failed | renewing
  acme_account_id: number
  dns_credential_id: number
  not_before: string | null
  not_after: string | null
  fingerprint: string
  auto_renew: boolean
  last_error: string
  created_at: string
}

export interface CreateCertRequest {
  name: string
  domains: string[]
  acme_account_id: number
  dns_credential_id: number
  auto_renew: boolean
}

// ACMEAccount mirrors the backend acmeAccountDTO — it NEVER carries the account
// private key, registration JSON, or the EAB HMAC secret (only has_eab_hmac).
export interface ACMEAccount {
  id: number
  name: string
  email: string
  directory: string
  eab_key_id: string
  has_eab_hmac: boolean
  key_type: string
  registered: boolean
  created_at: string
}

export interface ACMEAccountRequest {
  name: string
  email: string
  directory: string
  eab_key_id: string
  eab_hmac: string // write-only; blank on edit = keep the stored secret
  key_type: string
}

// DNSCredential mirrors dnsCredDTO — only the credential KEY names come back,
// never the secret values.
export interface DNSCredential {
  id: number
  name: string
  provider: string
  keys: string[]
}

export interface DNSCredentialRequest {
  name: string
  provider: string
  credentials: Record<string, string>
}

export async function listCerts(): Promise<Cert[]> {
  const { data } = await client.get<{ certs: Cert[] }>('/admin/certs')
  return data.certs
}

export async function getCert(id: number): Promise<Cert> {
  const { data } = await client.get<{ cert: Cert }>(`/admin/certs/${id}`)
  return data.cert
}

export async function createCert(req: CreateCertRequest): Promise<Cert> {
  const { data } = await client.post<{ cert: Cert }>('/admin/certs', req)
  return data.cert
}

export async function deleteCert(id: number): Promise<void> {
  await client.delete(`/admin/certs/${id}`)
}

export async function renewCert(id: number): Promise<void> {
  await client.post(`/admin/certs/${id}/renew`)
}

// CertTask is the in-flight issue/renew sync-task surfaced on a pending cert's
// detail view (the closest thing to "progress" — lego's Obtain is one blocking
// call). null/absent when nothing is queued.
export interface CertTask {
  status: string // pending | running
  attempts: number
  next_run_at: string | null
  last_error: string
}

export interface CertDetail {
  cert: Cert
  task?: CertTask
}

export async function getCertDetail(id: number): Promise<CertDetail> {
  const { data } = await client.get<CertDetail>(`/admin/certs/${id}`)
  return data
}

// CertPEM is returned ONLY by the explicit per-cert download endpoint — it
// carries the full chain + private key. The list/detail DTOs never include PEMs.
export interface CertPEM {
  name: string
  cert_pem: string
  key_pem: string
}

export async function downloadCert(id: number): Promise<CertPEM> {
  const { data } = await client.get<CertPEM>(`/admin/certs/${id}/download`)
  return data
}

// CertEvent is one cert issuance/renewal activity entry (Logs → Certificates).
export interface CertEvent {
  id: number
  cert_id: number
  cert_name: string
  kind: string // issue | renew
  success: boolean
  message: string
  created_at: string
}

export async function listCertEvents(page: number, pageSize: number): Promise<{ events: CertEvent[]; total: number }> {
  const { data } = await client.get<{ events: CertEvent[]; total: number }>('/admin/cert-events', {
    params: { page, page_size: pageSize },
  })
  return data
}

export async function listDNSCreds(): Promise<DNSCredential[]> {
  const { data } = await client.get<{ credentials: DNSCredential[] }>('/admin/dns-credentials')
  return data.credentials
}

export async function createDNSCred(req: DNSCredentialRequest): Promise<DNSCredential> {
  const { data } = await client.post<{ credential: DNSCredential }>('/admin/dns-credentials', req)
  return data.credential
}

export async function updateDNSCred(id: number, req: DNSCredentialRequest): Promise<DNSCredential> {
  const { data } = await client.put<{ credential: DNSCredential }>(`/admin/dns-credentials/${id}`, req)
  return data.credential
}

export async function deleteDNSCred(id: number): Promise<void> {
  await client.delete(`/admin/dns-credentials/${id}`)
}

// DNSProviderField is one labeled credential input for a curated provider. key is
// the exact env var lego reads; secret marks values to mask + treat write-only.
export interface DNSProviderField {
  key: string
  label: string
  secret: boolean
  optional?: boolean
}

// DNSProviderInfo is one entry of the provider catalog. custom=true (exec/httpreq)
// means there's no fixed schema — the form falls back to a free-form KEY/VALUE
// editor; otherwise fields lists exactly the inputs to collect.
export interface DNSProviderInfo {
  name: string
  label: string
  custom: boolean
  fields?: DNSProviderField[]
}

// listDNSProviders returns the curated provider catalog (code + label + the
// credential field schema) so the credential form can render labeled inputs.
export async function listDNSProviders(): Promise<DNSProviderInfo[]> {
  const { data } = await client.get<{ providers: DNSProviderInfo[] }>('/admin/dns-providers')
  return data.providers
}

// ---- ACME accounts (multi-account: a cert issues under a chosen CA account) ----

export async function listACMEAccounts(): Promise<ACMEAccount[]> {
  const { data } = await client.get<{ accounts: ACMEAccount[] }>('/admin/acme-accounts')
  return data.accounts
}

export async function createACMEAccount(req: ACMEAccountRequest): Promise<ACMEAccount> {
  const { data } = await client.post<{ account: ACMEAccount }>('/admin/acme-accounts', req)
  return data.account
}

export async function updateACMEAccount(id: number, req: ACMEAccountRequest): Promise<ACMEAccount> {
  const { data } = await client.put<{ account: ACMEAccount }>(`/admin/acme-accounts/${id}`, req)
  return data.account
}

export async function deleteACMEAccount(id: number): Promise<void> {
  await client.delete(`/admin/acme-accounts/${id}`)
}

export async function listACMEKeyTypes(): Promise<string[]> {
  const { data } = await client.get<{ key_types: string[] }>('/admin/acme-key-types')
  return data.key_types
}

// PanelWebCert is the cert_source=from_panel result: the panel's own web TLS
// cert/key file PATHS (3X-UI 3.2.7+). supported=false means the panel is too
// old (the form greys out the "fetch from panel" button).
export interface PanelWebCert {
  supported: boolean
  cert_file?: string
  key_file?: string
}

export async function fetchPanelWebCert(serverId: number): Promise<PanelWebCert> {
  const { data } = await client.get<PanelWebCert>(`/admin/servers/${serverId}/web-cert`)
  return data
}

// setNodeCertSource records a node's certificate source ('manual' | 'from_panel'
// | 'psp_managed'). For psp_managed the backend deploys the bound cert.
export async function setNodeCertSource(nodeId: number, source: string, certId: number): Promise<void> {
  await client.put(`/admin/nodes/${nodeId}/cert-source`, { source, cert_id: certId })
}
