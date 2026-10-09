import { client } from './client'

export type LegalKind = 'terms' | 'privacy'

export interface DataCollection {
  sub_log_retention_days: number
  auth_event_retention_days: number
  connection_retention_days: number
  hwid_captured: boolean
  hwid_retention_days: number
  flag_record_retention_days: number
  risk_assessment_refresh_minutes: number
  risk_review_purge_after_deletion_minutes: number
  access: { kind: 'hits' | 'trial' | 'usage'; nodes: number; retention_days: number }[]
}

export interface LegalPublicDocument {
  version: number
  consent_version: number
  locale: string
  fallback_from?: string
  content: string
  published_at: string
  data_collection: DataCollection
}

export async function getLegalDocument(kind: LegalKind, lang: string, signal?: AbortSignal): Promise<LegalPublicDocument> {
  const { data } = await client.get<LegalPublicDocument>(`/legal/${kind}`, {
    params: { lang }, signal, _skipErrorToast: true, _skipRefresh: true,
  })
  return data
}

export async function acceptLegalConsent(consentVersion: number): Promise<void> {
  await client.post('/user/me/legal/accept', { consent_version: consentVersion }, { _skipErrorToast: true })
}

export interface LegalAdminDocument {
  id: number
  kind: LegalKind
  locale: string
  version: number
  content: string
  consent_bump: boolean
  published_at: string
  published_by: number
}

export interface LegalPublication { document: LegalAdminDocument; consent_version: number }
export interface LegalHistory { items: LegalAdminDocument[]; next_before_id: number }

export async function getLegalLatest(kind: LegalKind, lang: string, signal?: AbortSignal): Promise<LegalAdminDocument | null> {
  try {
    return (await client.get<LegalAdminDocument>(`/admin/legal/${kind}/latest`, {
      params: { lang }, signal, _skipErrorToast: true,
    })).data
  } catch (error) {
    if ((error as { response?: { status?: number } }).response?.status === 404) return null
    throw error
  }
}

export async function getLegalHistory(kind: LegalKind, beforeId = 0, signal?: AbortSignal): Promise<LegalHistory> {
  return (await client.get<LegalHistory>(`/admin/legal/${kind}`, {
    params: { before_id: beforeId, limit: 50 }, signal, _skipErrorToast: true,
  })).data
}

export async function getLegalCollection(signal?: AbortSignal): Promise<DataCollection> {
  return (await client.get<DataCollection>('/admin/legal/data-collection', { signal, _skipErrorToast: true })).data
}

export async function getLegalAffectedUsers(signal?: AbortSignal): Promise<number> {
  return (await client.get<{ count: number }>('/admin/legal/affected-users', { signal, _skipErrorToast: true })).data.count
}

export async function publishLegal(kind: LegalKind, locale: string, content: string, consentBump: boolean, expectedVersion: number): Promise<LegalPublication> {
  return (await client.post<LegalPublication>(`/admin/legal/${kind}`, {
    locale, content, consent_bump: consentBump, expected_version: expectedVersion,
  }, { _skipErrorToast: true })).data
}
