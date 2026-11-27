import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { inspectProxyGroups, type MihomoRematchOutbound, type MihomoSubRule, type ProxyGroupInspection, type ProxyGroupMember, type ProxyGroupOptions, type RuleSet } from '@/api/rules'

interface Asked { content: string; small: string }
const sameAs = (a: Asked | null | undefined, b: Asked) => !!a && a.content === b.content && a.small === b.small

/**
 * The backend's own reading of the DRAFT: its proxy groups with their effective members (defaults included). The routing test,
 * the covered-rule findings, the proxy-group list and the rename all need to know what a group really is, and none of them
 * works that out itself (the members of a group with no stored list come from its name, in Go).
 *
 * It asks only while `needed` (something on screen uses the answer), and only for a draft that has not been answered:
 * drafts are compared by CONTENT (the rules text and the other inputs serialised), never by object identity, so a tab
 * switch, or the reset that follows a save with nothing changed, sends nothing. A request waits one second after the draft
 * stops changing, there is at most one in flight, and a newer draft supersedes (aborts) an older request.
 *
 * `fresh` means `inspection` answers for exactly the draft as it is now. A failure is remembered for that draft and not retried
 * on its own; `retry` asks again.
 */
export function useDraftInspection(draft: Pick<RuleSet, 'content' | 'proxy_group_members' | 'proxy_group_options' | 'mihomo_sub_rules' | 'mihomo_rematch_outbounds'>, needed: boolean) {
  const { content } = draft
  const small = useMemo(
    () => JSON.stringify([draft.proxy_group_members ?? null, draft.proxy_group_options ?? null, draft.mihomo_sub_rules ?? null, draft.mihomo_rematch_outbounds ?? null]),
    [draft.proxy_group_members, draft.proxy_group_options, draft.mihomo_sub_rules, draft.mihomo_rematch_outbounds],
  )
  const [done, setDone] = useState<(Asked & { data: ProxyGroupInspection }) | null>(null)
  const [failed, setFailed] = useState<Asked | null>(null)
  const [loading, setLoading] = useState(false)
  const inflight = useRef<(Asked & { controller: AbortController }) | null>(null)
  const current: Asked = { content, small }

  useEffect(() => {
    if (!needed) return
    if (sameAs(done, current) || sameAs(failed, current) || sameAs(inflight.current, current)) return
    const asked: Asked = { content, small }
    const timer = window.setTimeout(() => {
      inflight.current?.controller.abort()
      const controller = new AbortController()
      inflight.current = { ...asked, controller }
      setLoading(true)
      const members = JSON.parse(asked.small) as [Record<string, ProxyGroupMember[]> | null, Record<string, ProxyGroupOptions> | null, MihomoSubRule[] | null, MihomoRematchOutbound[] | null]
      void inspectProxyGroups({
        content: asked.content,
        proxy_group_members: members[0] || {},
        proxy_group_options: members[1] || {},
        mihomo_sub_rules: members[2] || [],
        mihomo_rematch_outbounds: members[3] || [],
      }, controller.signal).then(data => {
        if (controller.signal.aborted) return
        inflight.current = null
        setDone({ ...asked, data })
        setLoading(false)
      }).catch(() => {
        if (controller.signal.aborted) return
        inflight.current = null
        setFailed(asked)
        setLoading(false)
      })
    }, 1000)
    return () => window.clearTimeout(timer)
    // `current` is rebuilt from `content` and `small` each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [needed, content, small, done, failed])

  useEffect(() => () => inflight.current?.controller.abort(), [])
  const retry = useCallback(() => setFailed(null), [])

  const fresh = sameAs(done, current)
  return {
    inspection: done?.data ?? null,
    fresh,
    /** The request for this draft is pending or running. */
    loading: needed && !fresh && !sameAs(failed, current),
    /** The last request for this draft failed. */
    failed: sameAs(failed, current),
    retry,
    // A request is running even if the draft has moved on since.
    busy: loading,
  }
}
