import { client } from './client'

// Cert mirrors the backend certDTO — note it NEVER carries the private key.
export interface Cert {
  id: number
  name: string
  domains: string[]
  status: string // pending | active | failed | renewing
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
  dns_credential_id: number
  auto_renew: boolean
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

// listDNSProviders returns the built-in lego provider codes for the searchable
// provider dropdown in the credential form.
export async function listDNSProviders(): Promise<string[]> {
  const { data } = await client.get<{ providers: string[] }>('/admin/dns-providers')
  return data.providers
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
