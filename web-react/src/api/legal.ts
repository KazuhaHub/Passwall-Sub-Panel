import { client } from './client'

export type LegalKind = 'terms' | 'privacy'
export const LEGAL_KINDS: readonly LegalKind[] = ['terms', 'privacy']

export function isLegalKind(v: string | undefined): v is LegalKind {
  return v === 'terms' || v === 'privacy'
}

/** The largest document the server accepts (domain.LegalContentMaxBytes). */
export const LEGAL_CONTENT_MAX_BYTES = 60000

/** The line a document writes where the generated list goes. */
export const DATA_COLLECTION_MARKER = '[[data-collection]]'

/** One line of the "what we collect" list. The words are the front end's; the
 *  server only says what is true now (service/legal.Item). */
export interface DataCollectionItem {
  enabled: boolean
  /** Days kept; 0 is kept for ever. Absent where there is no retention to state. */
  retention_days?: number
  /** How many nodes record it (access-control items only). */
  nodes?: number
  includes_ip: boolean
  /** Risk signals that are off panel-wide but switched back on by this many groups. */
  enabled_groups?: number
}

export type DataCollection = Record<string, DataCollectionItem>

/** GET /api/legal/:kind. */
export interface PublicLegalDocument {
  kind: LegalKind
  version: number
  consent_version: number
  locale: string
  /** The language that was asked for when another one is served. */
  fallback_from?: string
  content: string
  published_at: string
  data_collection: DataCollection
}

/** The published page, or null when the feature is off or nothing is published
 *  (404). Any other failure throws. */
export async function getPublicLegal(kind: LegalKind, lang: string, opts: { signal?: AbortSignal } = {}): Promise<PublicLegalDocument | null> {
  try {
    const { data } = await client.get<PublicLegalDocument>(`/legal/${kind}`, {
      params: lang ? { lang } : undefined, signal: opts.signal, _skipErrorToast: true,
    })
    return data
  } catch (err) {
    if ((err as { response?: { status?: number } }).response?.status === 404) return null
    throw err
  }
}

export interface AdminLegalDocument {
  id: number
  locale: string
  version: number
  content: string
  consent_bump: boolean
  published_at: string
  published_by: number
}

export interface AdminLegalVersion {
  id: number
  locale: string
  version: number
  consent_bump: boolean
  published_at: string
  published_by: number
}

/** GET /api/admin/legal/:kind. */
export interface AdminLegal {
  kind: LegalKind
  enabled: boolean
  consent_version: number
  /** The newest version of the kind in each locale, with its text. */
  documents: AdminLegalDocument[]
  /** Every version, newest first as stored, without text. */
  versions: AdminLegalVersion[]
  /** The list the public page would show now; absent when it could not be built. */
  data_collection?: DataCollection
}

export async function getAdminLegal(kind: LegalKind, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<AdminLegal>(`/admin/legal/${kind}`, { signal: opts.signal })
  return data
}

export interface PublishLegalInput { locale: string; content: string; consent_bump: boolean }

export interface PublishLegalResult {
  document: AdminLegalVersion
  consent_version: number
  consent_bumped: boolean
}

/** Publishes the next version. The caller words the failures (_skipErrorToast). */
export async function publishLegal(kind: LegalKind, input: PublishLegalInput) {
  const { data } = await client.post<PublishLegalResult>(`/admin/legal/${kind}`, input, { _skipErrorToast: true })
  return data
}

export interface AffectedUsers {
  consent_version: number
  /** Accounts of role user: the ones a material change would ask again. */
  affected_users: number
  accepted_current: number
}

export async function getAffectedUsers(opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<AffectedUsers>('/admin/legal/affected-users', { signal: opts.signal })
  return data
}

/** POST /api/user/me/legal/accept. A stale version is a 409 legal_consent_outdated
 *  carrying the current one; the caller words it (_skipErrorToast). */
export async function acceptLegal(consentVersion: number) {
  await client.post('/user/me/legal/accept', { consent_version: consentVersion }, { _skipErrorToast: true })
}

/** The current consent_version out of a 409 legal_consent_outdated, or null when
 *  the error is anything else. */
export function outdatedConsentVersion(err: unknown): number | null {
  const res = (err as { response?: { status?: number; data?: { code?: string; consent_version?: number } } }).response
  if (res?.status === 409 && res.data?.code === 'legal_consent_outdated') return res.data.consent_version ?? 0
  return null
}
