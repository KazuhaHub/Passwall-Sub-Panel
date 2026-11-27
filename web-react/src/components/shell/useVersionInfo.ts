import { useEffect, useState } from 'react'

import { getVersion, type VersionInfo } from '@/api/version'

// The version badge is on by default; whoever ships the binary can suppress it at
// SPA build time with `VITE_SHOW_VERSION=0 npm run build`. Deliberately not a
// runtime or admin-settings toggle: the choice belongs to the person who ships the
// binary, not to admins clicking around in the UI.
const SHOW_VERSION = import.meta.env.VITE_SHOW_VERSION !== '0'

/**
 * Build identity for the sidebar footer, fetched once. The endpoint is public and
 * the footer silently shows nothing if the call fails (no point toasting for a
 * missing version string). Skipped entirely when the badge is disabled at build time.
 */
export function useVersionInfo(): VersionInfo | null {
  const [info, setInfo] = useState<VersionInfo | null>(null)
  useEffect(() => {
    if (!SHOW_VERSION) return
    let cancelled = false
    getVersion()
      .then(v => { if (!cancelled) setInfo(v) })
      .catch(() => { /* leave the footer empty */ })
    return () => { cancelled = true }
  }, [])
  return info
}
