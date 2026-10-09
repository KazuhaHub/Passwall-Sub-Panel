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
