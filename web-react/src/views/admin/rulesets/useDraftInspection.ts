import { useEffect, useState } from 'react'
import { inspectProxyGroups, type MihomoRematchOutbound, type MihomoSubRule, type ProxyGroupInspection, type ProxyGroupMember, type ProxyGroupOptions, type RuleSet } from '@/api/rules'

interface Refs {
  content: string
  members: RuleSet['proxy_group_members']
  options: RuleSet['proxy_group_options']
  subs: MihomoSubRule[] | undefined
  rematches: MihomoRematchOutbound[] | undefined
}

const same = (a: Refs, b: Refs) => a.content === b.content && a.members === b.members && a.options === b.options && a.subs === b.subs && a.rematches === b.rematches

/**
 * The backend's own reading of the DRAFT: its proxy groups with their effective members (defaults included), asked after a
 * pause once the draft stops changing. The routing test, the covered-rule findings and the proxy-group rename all need to
 * know what a group really is, and they must not work that out themselves (the members of a group with no stored list come
 * from its name, in Go).
 *
 * `fresh` is true only when `groups` answers for exactly the draft as it is now. An answer for an older draft, a request in
 * flight or one that failed are all "not fresh", and whoever relies on the members says it cannot while that is so.
 * It is the same request the save makes (inspect-proxy-groups), and asks only while `enabled`.
 */
export function useDraftInspection(draft: Pick<RuleSet, 'content' | 'proxy_group_members' | 'proxy_group_options' | 'mihomo_sub_rules' | 'mihomo_rematch_outbounds'>, enabled: boolean) {
  const [done, setDone] = useState<{ refs: Refs; data: ProxyGroupInspection } | null>(null)
  const refs: Refs = { content: draft.content, members: draft.proxy_group_members, options: draft.proxy_group_options, subs: draft.mihomo_sub_rules, rematches: draft.mihomo_rematch_outbounds }

  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    const asked: Refs = { content: draft.content, members: draft.proxy_group_members, options: draft.proxy_group_options, subs: draft.mihomo_sub_rules, rematches: draft.mihomo_rematch_outbounds }
    const timer = window.setTimeout(() => {
      void inspectProxyGroups({
        content: asked.content,
        proxy_group_members: (asked.members || {}) as Record<string, ProxyGroupMember[]>,
        proxy_group_options: (asked.options || {}) as Record<string, ProxyGroupOptions>,
        mihomo_sub_rules: asked.subs || [],
        mihomo_rematch_outbounds: asked.rematches || [],
      }, controller.signal).then(data => {
        if (!controller.signal.aborted) setDone({ refs: asked, data })
      }).catch(() => { /* Not fresh: whoever needs the members says so. The request layer reports a failure that matters. */ })
    }, 600)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [enabled, draft.content, draft.proxy_group_members, draft.proxy_group_options, draft.mihomo_sub_rules, draft.mihomo_rematch_outbounds])

  const fresh = enabled && done !== null && same(done.refs, refs)
  return { inspection: done?.data ?? null, fresh }
}
