import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { putUISettings, type UISettings } from '@/api/settings'
import { getAuthMethods } from '@/api/auth'
import { pushSnack } from '@/components/feedback/toasts'
import { settingsKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { panelPath } from '@/panelPath'
import { useSiteStore } from '@/stores/site'
import { useAdminT } from '../useAdminT'
import { normalizeSettings, sameValue, settingId, WRITE_ONLY_KEYS } from './model'

interface UiData {
  /** The latest record the server has given (or accepted), normalised. */
  saved: UISettings
  /** Writes the whole record and adopts the server's answer. */
  write: (body: UISettings) => Promise<UISettings>
}

const UiDataContext = createContext<UiData | null>(null)

function useUiData(): UiData {
  const data = useContext(UiDataContext)
  if (!data) throw new Error('useUiSection needs a UiDataProvider')
  return data
}

/**
 * Holds the panel-wide settings record for the sections. `raw` is what the query cache holds; sections only ever see
 * it normalised. A write goes through here so that what follows a save (the live branding, the legal flags, the
 * reload after a panel path change) happens once, whichever section saved.
 */
export function UiDataProvider({ raw, children }: { raw: UISettings; children: ReactNode }) {
  const queryClient = useQueryClient()
  const scope = useQueryScope()
  const site = useSiteStore()
  const saved = useMemo(() => normalizeSettings(raw), [raw])

  const write = useCallback(async (body: UISettings) => {
    const answer = normalizeSettings(await putUISettings(body))
    queryClient.setQueryData(settingsKeys.ui(scope), answer)
    // Mirror the brand-relevant fields into the live site store so the header and title follow without a reload.
    site.update({
      siteTitle: answer.site_title || 'Kazuha Hub Passwall',
      appTitle: answer.app_title || 'Passwall',
      logoUrl: answer.logo_url || '',
      logoUrlDark: answer.logo_url_dark || '',
      iconUrl: answer.icon_url || '',
      footerText: answer.footer_text || '© Kazuha Hub Passwall',
      themeColor: answer.theme_color || undefined,
    })
    // Whether the legal links and the consent dialog are live is the server's call (the switch AND a published
    // document), so it is asked rather than read off the switch: a switch turned on before anything is published
    // would link to a 404.
    void getAuthMethods({ quiet: true })
      .then(m => site.update({ legalEnabled: !!m.legal?.enabled, legalConsentVersion: m.legal?.consent_version ?? 0 }))
      .catch(() => {})
    // panel_path moves the SPA and API mount: reload from the new one now, so this tab never keeps using routes
    // that no longer exist.
    if (answer.panel_path !== panelPath) {
      sessionStorage.setItem('psp_panel_path_changed', answer.panel_path || '/')
      window.location.replace(`${answer.panel_path || ''}/admin/settings`)
    }
    return answer
  }, [queryClient, scope, site])

  const value = useMemo(() => ({ saved, write }), [saved, write])
  return <UiDataContext.Provider value={value}>{children}</UiDataContext.Provider>
}

/** The saved record, for a section that only needs to read it. */
export function useSavedSettings(): UISettings {
  return useUiData().saved
}

type Problems = Record<string, string>

/**
 * One section's draft of the panel-wide settings record.
 *
 * The draft holds only the EDITS, over the saved record: a field the admin has not touched follows the server (a
 * refocus that refetches does not reset anything), and a field they have is never overwritten by a read. `dirty` is
 * a comparison with what is saved, so typing a value back to what it was is not an unsaved change.
 *
 * `save` sends the latest saved record with this section's keys replaced by the draft, the same body the old
 * whole-page save sent for the same edits, minus the other sections' unsaved edits. Secrets that were not typed are
 * left out of the body (empty means "keep the stored one" on the server).
 */
export function useUiSection<K extends keyof UISettings>(
  keys: readonly K[],
  report: (dirty: boolean) => void,
  validate: (value: Pick<UISettings, K>, t: ReturnType<typeof useAdminT>) => Problems,
) {
  const { saved, write } = useUiData()
  const t = useAdminT()
  const [edits, setEdits] = useState<Partial<Pick<UISettings, K>>>({})
  const [touched, setTouched] = useState<ReadonlySet<string>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)

  const value = useMemo(() => {
    const out: Partial<UISettings> = {}
    for (const k of keys) out[k] = (k in edits ? edits[k] : saved[k]) as never
    return out as Pick<UISettings, K>
  }, [keys, saved, edits])

  // A secret is dirty once something is typed in it; every other key by comparison with what is saved.
  const dirty = useMemo(() => keys.some(k => k in edits
    && ((WRITE_ONLY_KEYS as readonly string[]).includes(k) ? !!edits[k] : !sameValue(edits[k], saved[k]))), [keys, edits, saved])

  useEffect(() => { report(dirty) }, [dirty, report])
  useEffect(() => () => report(false), [report])

  const problems = useMemo(() => validate(value, t), [validate, value, t])
  const error = (k: string): string | undefined => (attempted || touched.has(k) ? problems[k] || undefined : undefined)
  const blur = (k: string) => setTouched(s => (s.has(k) ? s : new Set(s).add(k)))
  const patch = useCallback((p: Partial<Pick<UISettings, K>>) => setEdits(e => ({ ...e, ...p })), [])
  const discard = useCallback(() => { setEdits({}); setTouched(new Set()); setAttempted(false) }, [])

  // Read at the moment of saving, so a confirmation that awaits the admin does not save a stale draft.
  const latest = useRef({ saved, value, problems })
  latest.current = { saved, value, problems }

  /** Returns true when the record was written. `quiet` is for a save that is a step of another action (no toast). */
  async function save(opts: { quiet?: boolean } = {}): Promise<boolean> {
    const { saved: base, value: draft, problems: bad } = latest.current
    const first = Object.keys(bad).find(k => bad[k])
    if (first) {
      setAttempted(true)
      setTimeout(() => document.getElementById(settingId(first))?.focus(), 0)
      return false
    }
    const body: UISettings = { ...base, ...draft }
    for (const k of WRITE_ONLY_KEYS) if (!body[k]) delete body[k]
    setPending(true)
    try {
      await write(body)
      setEdits({}); setTouched(new Set()); setAttempted(false)
      if (!opts.quiet) pushSnack(t('settings.saved'), 'success')
      return true
    } catch {
      // The request layer already told the admin what failed; the draft stays as it is.
      return false
    } finally { setPending(false) }
  }

  return { saved, value, patch, dirty, pending, error, blur, save, discard, problems, attempted }
}

export type UiSection<K extends keyof UISettings> = ReturnType<typeof useUiSection<K>>
