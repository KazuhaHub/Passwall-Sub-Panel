import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { testServer, type Server } from '@/api/servers'
import { pushSnack } from '@/components/feedback/toasts'
import { allSettledLimited } from '@/utils/promises'

export type ProbeStatus = 'unknown' | 'checking' | 'ok' | 'fail' | 'unconfigured'

export interface ProbeState {
  status: ProbeStatus
  error?: string
  inbound_count?: number
}

export function credentialsConfigured(s: Server): boolean {
  return s.panel_type === 'psp' || s.has_api_token || s.has_password
}

/**
 * The connection state of each server on the page, from the "test connection" probe.
 *
 * THE PROBE IS A REAL REQUEST TO THE UPSTREAM SERVER, so it is driven only by the SET OF ROW IDS (`pageIdsKey`): a
 * probe response merges fresh version data back into the row (`mutateItems`), which produces a new `items` reference,
 * and an effect that depended on `items` would loop (probe, mutate, effect, probe). Keyed on ids, it fires once per
 * page, search or sort change, and a plain list refresh never probes.
 */
export function useServerProbes(items: Server[], mutateItems: (updater: (prev: Server[]) => Server[]) => void) {
  const { t } = useTranslation('admin')
  const [states, setStates] = useState<Record<number, ProbeState>>({})
  const [testing, setTesting] = useState<number | null>(null)
  const [testingAll, setTestingAll] = useState(false)
  const pageIdsKey = useMemo(() => items.map(s => s.id).join('|'), [items])

  const stateFor = useCallback((s: Server): ProbeState =>
    states[s.id] ?? { status: credentialsConfigured(s) ? 'unknown' : 'unconfigured' }, [states])

  const probeServer = useCallback(async (s: Server, notify = false) => {
    if (!credentialsConfigured(s)) {
      setStates(p => ({ ...p, [s.id]: { status: 'unconfigured' } }))
      if (notify) pushSnack(t('admin:servers.validate.credentials_required'), 'warning')
      return
    }
    setStates(p => ({ ...p, [s.id]: { status: 'checking' } }))
    try {
      const r = await testServer(s.id)
      if (r.ok) {
        setStates(p => ({ ...p, [s.id]: { status: 'ok', inbound_count: r.inbound_count } }))
        // The test piggybacks a version probe: merge any returned version fields back into the row so the Version
        // column and the compatibility banners reflect it without a list reload. The IP-cap probe rides the same
        // click. An absent field means the probe produced nothing usable and the stored state was deliberately left
        // alone, so the old value is kept rather than blanking the badge on a blip.
        if (r.panel_version !== undefined || r.compat_status || r.ip_limit_enforcement ||
          r.latest_xui_version !== undefined || r.latest_sui_version !== undefined || r.update_available !== undefined) {
          mutateItems(prev => prev.map(it => it.id === s.id ? {
            ...it,
            panel_version: r.panel_version ?? it.panel_version,
            xray_version: r.xray_version ?? it.xray_version,
            core_engine: r.core_engine ?? it.core_engine,
            core_version: r.core_version ?? it.core_version,
            version_checked_at: r.version_checked_at ?? it.version_checked_at,
            compat_status: r.compat_status ?? it.compat_status,
            compat_message: r.compat_message ?? it.compat_message,
            latest_xui_version: r.latest_xui_version ?? it.latest_xui_version,
            latest_sui_version: r.latest_sui_version ?? it.latest_sui_version,
            update_available: r.update_available ?? it.update_available,
            ip_limit_enforcement: r.ip_limit_enforcement ?? it.ip_limit_enforcement,
          } : it))
        }
        if (notify) pushSnack(t('admin:servers.toast.test_ok', { count: r.inbound_count ?? 0 }), 'success')
      } else {
        setStates(p => ({ ...p, [s.id]: { status: 'fail', error: r.error ?? 'unknown' } }))
        if (notify) pushSnack(r.error ?? 'unknown', 'error')
      }
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
        ?? (err as { message?: string }).message
        ?? 'unknown'
      setStates(p => ({ ...p, [s.id]: { status: 'fail', error: msg } }))
      // The request layer already toasts a non-401 failure.
    }
  }, [mutateItems, t])

  useEffect(() => {
    void allSettledLimited(items, s => probeServer(s))
    // Once per page of rows: see the note above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageIdsKey])

  async function runTest(s: Server) {
    setTesting(s.id)
    try { await probeServer(s, true) } finally { setTesting(null) }
  }

  // "Test all" ignores the selection and probes every server on the page: a quick check after a network change.
  async function runTestMany(rows: Server[]) {
    if (!rows.length) return
    setTestingAll(true)
    try {
      await allSettledLimited(rows, s => probeServer(s))
      pushSnack(t('admin:servers.toast.batch_tested', { count: rows.length }), 'success')
    } finally { setTestingAll(false) }
  }

  return { pageIdsKey, stateFor, probeServer, runTest, runTestMany, testing, testingAll }
}
