import { useEffect, useState } from 'react'
import { listNodeReleases, type NodeRelease } from '@/api/nodeReleases'
import { getCompatStatus, getSUIRelease, type CompatStatusResponse, type Server } from '@/api/servers'

/**
 * What the page needs to know about releases, read separately from the connection probes (a cold GitHub cache finishing
 * later must still update the page): the Passwall Node catalog (one metadata read per page, not one GitHub request per
 * server; the backend caches and validates the reviewed, published catalog), the newest S-UI release, and the panel's
 * own account of what its compatibility decisions rest on. All of it is best effort: it is diagnosis, and a panel that
 * cannot report its state must not claim one.
 */
export function useReleaseInfo(items: Server[], pageIdsKey: string) {
  const [nodeReleases, setNodeReleases] = useState<NodeRelease[] | null>(null)
  const [nodeReleaseCheckFailed, setNodeReleaseCheckFailed] = useState(false)
  const [check, setCheck] = useState(0)
  const [suiReleaseVersion, setSUIReleaseVersion] = useState<string | null>(null)
  const [compatStatus, setCompatStatus] = useState<CompatStatusResponse | null>(null)
  const hasNative = items.some(s => s.panel_type === 'psp')
  const hasSUI = items.some(s => s.panel_type === 'sui')

  useEffect(() => {
    const controller = new AbortController()
    setNodeReleases(null)
    setNodeReleaseCheckFailed(false)
    if (hasNative) {
      void listNodeReleases(controller.signal).then(catalog => {
        if (controller.signal.aborted) return
        if (!Array.isArray(catalog.releases)) throw new Error('Invalid Node release catalog')
        setNodeReleases(catalog.releases)
      }).catch(() => {
        if (!controller.signal.aborted) setNodeReleaseCheckFailed(true)
      })
    }
    return () => controller.abort()
  }, [hasNative, pageIdsKey, check])

  useEffect(() => {
    const controller = new AbortController()
    setSUIReleaseVersion(null)
    if (hasSUI) {
      void getSUIRelease(controller.signal).then(metadata => {
        if (!controller.signal.aborted && typeof metadata.version === 'string') setSUIReleaseVersion(metadata.version)
      }).catch(() => { /* Keep any useful last-known-good fields in the server DTO. */ })
    }
    return () => controller.abort()
  }, [hasSUI, pageIdsKey, check])

  useEffect(() => {
    let live = true
    void getCompatStatus().then(next => { if (live) setCompatStatus(next) }).catch(() => { if (live) setCompatStatus(null) })
    return () => { live = false }
  }, [])

  return { nodeReleases, nodeReleaseCheckFailed, suiReleaseVersion, compatStatus, recheck: () => setCheck(n => n + 1) }
}
