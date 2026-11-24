import { isLegalKind, type LegalKind } from '@/api/legal'

/**
 * The addresses of the privacy-and-terms section. The list is Settings' own tab; a document's editor is a page under
 * the Settings prefix (so the sidebar keeps "Settings" lit while it is open) keyed by the document's kind, the
 * same `terms` / `privacy` the API and the public `/legal/:kind` page use. There is no "new document": both exist
 * from the start and an unpublished one simply opens empty (see LegalEditorPage).
 */
export const LEGAL_LIST_PATH = '/admin/settings?tab=legal'
const SETTINGS_PATH = '/admin/settings'

export function legalEditorPath(kind: LegalKind): string {
  return `${SETTINGS_PATH}/legal/${kind}`
}

/** The route parameter as a kind. Compared case-sensitively on purpose: the API paths are lower-case, and only these two exist. */
export function parseLegalKind(param: string | undefined): LegalKind | null {
  return isLegalKind(param) ? param : null
}

/** Where the editor's back link goes: the settings page as the person left it, else its Privacy and terms tab. */
export function legalBackTarget(remembered: string): string {
  return remembered === SETTINGS_PATH ? LEGAL_LIST_PATH : remembered
}

export { SETTINGS_PATH }
