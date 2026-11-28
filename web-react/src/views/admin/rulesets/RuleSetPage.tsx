import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MutableRefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import { deleteRuleSet, getRuleSet, inspectProxyGroups, resetRuleSet, RULESET_BODY_LIMIT, ruleSetFailureOf, ruleSetRequestBody, saveRuleSet, type ProxyGroupIssue, type RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { Notice } from '@/components/common/Notice'
import { StatusDot } from '@/components/common/StatusDot'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import MihomoAdvancedRulesEditor from '@/components/MihomoAdvancedRulesEditor'
import ProxyGroupMembersEditor from '@/components/ProxyGroupMembersEditor'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useIsMobile } from '@/hooks/use-mobile'
import { useTabParam } from '@/hooks/useTabParam'
import { IconCopy, IconDelete } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { ruleKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { diffStats } from '@/utils/ruleset/diff'
import { buildBlocks } from '@/utils/ruleset/blocks'
import { continuation } from '@/utils/ruleset/continuation'
import { groupUsage, type RenamePlan } from '@/utils/ruleset/rename'
import { pruneRuleSetProxyGroupMetadata } from '@/utils/proxyGroupMembers'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { askDeleteRuleSet, askResetRuleSet, isSeeded } from './actions'
import { BasicsTab } from './BasicsTab'
import { ChangesDialog } from './ChangesDialog'
import { readDoc, SETTLE_MS, useSettled } from './docCache'
import { cloneProxyGroupMembers, cloneProxyGroupOptions, cloneRuleSet, EMPTY, sameRuleSet } from './model'
import { ConflictDialog } from './ConflictDialog'
import { RenameGroupDialog } from './RenameGroupDialog'
import { RulesTab } from './RulesTab'
import { mb, tooLargeText } from './saveFailures'
import { isReservedSlug, newRuleSetPath, ruleSetPath } from './ruleSetRoutes'
import { isBuiltinTarget } from './targets'
import { useDraftInspection } from './useDraftInspection'

const TABS = ['rules', 'members', 'sub_rules', 'rematches', 'basics'] as const
type Tab = (typeof TABS)[number]

const bytesOf = (value: unknown) => new Blob([JSON.stringify(value)]).size
const split = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))

/**
 * One rule set as a page, for an existing one and for the create form (`seed` is then a blank rule set, or a copy).
 * The save is the old sheet's, in the same order: required fields, the slug check for a new set, the server's inspection
 * (with the orphan-group cleanup the admin must confirm), then the PUT itself, with the same body. What is new is a size
 * check before any of that, because the server refuses a body over 8 MiB (RULESET_BODY_LIMIT) with an error that says nothing about why;
 * a 413 that still comes back (a proxy in front may allow less) is worded the same way. Every save of an existing rule set carries the revision it
 * started from, and a 409 means someone saved since: the draft is left as it is and the page offers to review, overwrite or discard.
 *
 * Leaving with an edited draft asks once; a finished create and a delete made here leave without asking (`leaving`).
 */
export function RuleSetPage({ mode, seed, metaSource, existingSlugs, templates, groups, back, readOnly, leaving, onReseed }: {
  mode: 'edit' | 'create'
  seed: RuleSet
  /** Whose proxy-group settings the members tab marks changes against (a copy starts from its source's). */
  metaSource: RuleSet
  existingSlugs: readonly string[]
  templates: readonly Template[]
  groups: readonly Group[]
  back: string
  readOnly: boolean
  leaving: MutableRefObject<boolean>
  onReseed: () => Promise<void>
}) {
  const { t } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const scope = useQueryScope()
  const editing = mode === 'edit'
  const [form, setForm] = useState<RuleSet>(() => cloneRuleSet(seed))
  const [baseline, setBaseline] = useState<RuleSet>(() => cloneRuleSet(editing ? seed : EMPTY))
  const [initialOrder, setInitialOrder] = useState<string[]>(() => [...(metaSource.proxy_group_order || [])])
  const [initialMembers, setInitialMembers] = useState(() => cloneProxyGroupMembers(metaSource.proxy_group_members))
  const [initialOptions, setInitialOptions] = useState(() => cloneProxyGroupOptions(metaSource.proxy_group_options))
  const visible: readonly Tab[] = readOnly ? ['rules', 'basics'] : TABS
  const [tab, setTab] = useTabParam<Tab>('tab', editing ? 'rules' : 'basics', visible)
  const [attempted, setAttempted] = useState(false)
  const [rematchSeen, setRematchSeen] = useState(false)
  const [pending, setPending] = useState(false)
  const [acting, setActing] = useState(false)
  const [ruleIssues, setRuleIssues] = useState<ProxyGroupIssue[]>([])
  const [changes, setChanges] = useState(false)
  // Set when a save found the stored rule set newer than the one this draft started from; `current` is their version (null when it could not be read).
  const [conflict, setConflict] = useState<{ current: RuleSet | null } | null>(null)
  const [reviewing, setReviewing] = useState(false)
  const phone = useIsMobile()
  // The strip (tabs and toolbar) that sticks under the top bar, and the box the Rules tab's toolbar is drawn into. The strip's height
  // is written as `--rs-stick` on the tabs, the distance from the top of the window to the rules: the columns and the routing test
  // size themselves to what is left under it (a measured height, because the toolbar can be one row or two).
  const strip = useRef<HTMLDivElement>(null)
  const [slot, setSlot] = useState<HTMLElement | null>(null)
  useLayoutEffect(() => {
    const el = strip.current
    const host = el?.parentElement
    if (!el || !host) return
    if (phone) { host.style.removeProperty('--rs-stick'); return }
    const set = () => host.style.setProperty('--rs-stick', `calc(var(--shell-bar-h) + ${Math.round(el.getBoundingClientRect().height)}px)`)
    set()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(set)
    ro.observe(el)
    return () => ro.disconnect()
  }, [phone])
  // The group being renamed (its name and the names a new one may not take), and the group the Proxy groups tab asked the Rules tab to show.
  const [renaming, setRenaming] = useState<{ name: string } | null>(null)
  const [showTarget, setShowTarget] = useState<{ name: string; token: number } | null>(null)
  // Bumped on Discard so the rules tab starts again from the restored text with a clean history.
  const [rev, setRev] = useState(0)

  const dirty = !readOnly && !sameRuleSet(form, baseline)
  // The floating Save bar takes this much of the window's bottom while it is shown (it is a bar of 3.5rem and a margin of 1rem on each side).
  const barShown = !readOnly && (dirty || pending)
  useLayoutEffect(() => { strip.current?.parentElement?.style.setProperty('--rs-bottom', barShown ? '5.5rem' : '0px') }, [barShown])
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t), bypass: leaving })

  // Slugs are file names, and the file system may fold case: `Custom` would overwrite `custom`.
  const slugTaken = !editing && existingSlugs.some(s => s.toLowerCase() === form.slug.toLowerCase())
  const slugReserved = !editing && isReservedSlug(form.slug)
  const slugError = attempted && !form.slug ? t('admin:rules.validate.slug_name_required')
    : attempted && slugTaken ? t('admin:rules.validate.slug_exists', { slug: form.slug })
    : attempted && slugReserved ? t('admin:rules.validate.slug_reserved', { slug: form.slug }) : undefined
  const nameError = attempted && !form.name ? t('admin:rules.validate.slug_name_required') : undefined
  const usedBy = useMemo(() => templates.filter(tpl => (tpl.rule_sets || []).includes(seed.slug)).map(x => x.name || x.slug), [templates, seed.slug])

  // A rename rewrites rule lines, so it needs the rules readable as a list: with a foreign line the editor writes nothing (see parse.ts foreignOf).
  // Worked out from the text as it stood when typing paused (and from the text itself whenever another tab is open): the whole text is
  // read to know it, which on a long rule set is too much to do on every keystroke of the Rules tab.
  const typing = tab === 'rules' || tab === 'sub_rules'
  const settledContent = useSettled(form.content, typing ? SETTLE_MS : 0)
  const settledSubRules = useSettled(form.mihomo_sub_rules, typing ? SETTLE_MS : 0)
  const textOnly = useMemo(() => readDoc(settledContent).foreign.length > 0, [settledContent])
  // What the backend says every proxy group of THIS draft is (members included); null until that answer is in for the draft as it
  // is now. The routing test, the covered-rule findings and the rename rest on it, and none of them works the members out itself.
  // Asked only while something on screen uses it: the Proxy groups tab, the rename dialog, or the routing test / problems panel of
  // the Rules tab or of a sub-rule (their editors say so through `groups.needed`).
  const [needsMain, setNeedsMain] = useState(false)
  const [needsSub, setNeedsSub] = useState(false)
  const needed = !readOnly && (tab === 'members' || (tab === 'rules' && needsMain) || (tab === 'sub_rules' && needsSub) || renaming !== null)
  const { inspection, fresh, loading: groupsLoading, failed: groupsFailed, failedMessage: groupsFailedMessage, retry: retryGroups } = useDraftInspection(form, needed)
  const effective = useMemo(() => (fresh && inspection ? new Map(inspection.groups.map(g => [g.name, g.members] as const)) : null), [fresh, inspection])
  // Names the renderer does not turn into a group (a node or line of the subscription is one already): proxyTargetExclusions.
  const reserved = useMemo(() => new Set((inspection?.nodes ?? []).map(n => n.display_name).concat((inspection?.lines ?? []).flatMap(l => [l.name, l.display_name]))), [inspection])
  const rematchNames = useMemo(() => (form.mihomo_rematch_outbounds || []).map(o => o.name), [form.mihomo_rematch_outbounds])
  const cont = useMemo(() => continuation(effective, rematchNames), [effective, rematchNames])
  const renameBlocked = textOnly ? t('admin:rules.rename.unavailable.text_only') : effective === null ? t('admin:rules.rename.unavailable.not_ready') : undefined
  const usage = useCallback((name: string) => groupUsage({ content: form.content, mihomo_sub_rules: form.mihomo_sub_rules }, name), [form.content, form.mihomo_sub_rules])
  const applyRename = (plan: Extract<RenamePlan, { ok: true }>, to: string) => {
    setForm(f => ({ ...f, ...plan.patch }))
    // The Rules tab starts again from the renamed text with a clean history: undoing a rename there would bring back the old
    // name in the rules but not in the member lists.
    setRev(r => r + 1)
    setRenaming(null)
    pushToast({ message: t('admin:rules.rename.done', { name: to }), severity: 'success' })
  }

  // The proxy groups the rules name, main rules and sub-rules alike: what a sub-rule's targets may be, and the names a new group may not take.
  const ruleGroups = useMemo(() => {
    const names = new Set<string>()
    for (const text of [settledContent, ...(settledSubRules ?? []).map(r => r.content)]) {
      for (const b of buildBlocks(readDoc(text))) if (b.kind === 'rules' && b.target && !isBuiltinTarget(b.target)) names.add(b.target)
    }
    return [...names]
  }, [settledContent, settledSubRules])

  const warn = (message: string) => pushToast({ message, severity: 'warning' })
  const changeTab = (next: Tab) => {
    if (tab === 'rematches' && next !== 'rematches') setRematchSeen(true)
    setTab(next)
  }

  /**
   * A write here changes what the list and the template pages show (line count, targets, revision). Those hold summaries the
   * server derives from the text, which the page cannot rebuild itself, so they are marked stale and read again when next shown
   * (nothing is requested while no list is on screen).
   */
  function listsChanged() {
    void queryClient.invalidateQueries({ queryKey: ruleKeys.lists(scope) })
  }

  /** Takes a rule set the server returned as the new saved state: the draft, the baseline (revision included) and the members tab's reference. */
  function adopt(saved: RuleSet) {
    listsChanged()
    setForm(cloneRuleSet(saved))
    setBaseline(cloneRuleSet(saved))
    setInitialOrder([...(saved.proxy_group_order || [])])
    setInitialMembers(cloneProxyGroupMembers(saved.proxy_group_members))
    setInitialOptions(cloneProxyGroupOptions(saved.proxy_group_options))
  }

  /** `base`: the revision to save against when it is not the baseline's (Overwrite saves against the one the server just reported). */
  async function save(base?: string) {
    // Save and Enter must not start a second save while one is in flight.
    if (pending || readOnly) return
    if (!form.slug || !form.name) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_name_required')); return }
    // Creating or duplicating with an existing slug would silently overwrite that rule set server-side (Save is a
    // slug-keyed upsert); the slug is locked when editing, so this only fires on the create and duplicate flows.
    if (slugTaken) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_exists', { slug: form.slug })); return }
    if (slugReserved) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_reserved', { slug: form.slug })); return }
    // The server refuses a larger body in a middleware, with nothing the person could act on; say it here, before any request.
    // A new rule set (or a copy of one) has no revision to be stale against; an existing one saves against the revision it was read at.
    const baseRevision = editing ? (base ?? baseline.revision) : undefined
    // Never fall back to an unconditional overwrite: a rule set read without a revision is not saved until it is read again.
    if (editing && !baseRevision) { warn(t('admin:rules.revision_missing')); return }
    const size = bytesOf(ruleSetRequestBody(form, baseRevision))
    if (size > RULESET_BODY_LIMIT) { warn(t('admin:rules.too_large', { size: mb(size), limit: mb(RULESET_BODY_LIMIT) })); return }
    // The server's reading of this exact draft, when the page already holds it (the members tab, the rename or the routing test asked
    // for it): `fresh` is by content, so it answers for the draft as it is now. Reusing it saves a second upload of the whole text.
    const known = fresh ? inspection : null
    // From here the editor is locked (`pending`) until the answer is in, so nothing typed meanwhile is overwritten by the response or reported as saved.
    setPending(true)
    try {
      let draft = form
      const inspect = (d: RuleSet) => inspectProxyGroups({
        content: d.content,
        proxy_group_members: d.proxy_group_members || {},
        proxy_group_options: d.proxy_group_options || {},
        mihomo_sub_rules: d.mihomo_sub_rules || [],
        mihomo_rematch_outbounds: d.mihomo_rematch_outbounds || [],
      }, undefined, true)
      // Only a fresh answer stands in for the request. The draft the orphan-group cleanup rewrites below is a different draft and is always asked.
      let inspection = known ?? await inspect(draft)
      const cleanup = pruneRuleSetProxyGroupMetadata(draft, inspection.groups)
      if (cleanup.removedGroups.length > 0) {
        const ok = await confirm({
          title: t('admin:rules.confirm.cleanup_groups_title'),
          message: t('admin:rules.confirm.cleanup_groups_message', { names: cleanup.removedGroups.join('、') }),
          confirmText: t('admin:rules.confirm.cleanup_groups_action'),
          destructive: true,
          initialFocus: 'cancel',
        })
        if (!ok) return
        draft = cleanup.ruleSet
        setForm(draft)
        inspection = await inspect(draft)
      }
      const errors = inspection.issues.filter(issue => issue.level === 'error')
      setRuleIssues(errors.filter(issue => issue.section === 'rules'))
      if (errors.length > 0) {
        // Take the person to the part that holds the problem. A main-rule problem stays on the Rules tab.
        const sections = errors.map(issue => issue.section).filter(Boolean)
        if (sections.includes('rematch_outbound')) { setRematchSeen(true); changeTab('rematches') }
        else if (sections.includes('sub_rule')) changeTab('sub_rules')
        else if (sections.includes('rules')) changeTab('rules')
        else changeTab('members')
        warn(sections.length > 0 ? t('admin:rules.validate.rule_extensions') : t('admin:rules.validate.proxy_group_members'))
        return
      }
      const saved = await saveRuleSet(draft, baseRevision)
      pushToast({ message: t('admin:rules.toast.saved'), severity: 'success' })
      if (editing) {
        adopt(saved)
        setConflict(null)
      } else {
        leaving.current = true
        listsChanged()
        // The new rule set's own page, with the list's place still behind its back link.
        void navigate(ruleSetPath(saved.slug), { replace: true, state: location.state })
      }
    } catch (err) {
      // Only the two refusals worded here are not toasted by the request layer; every other failure already was.
      const failure = ruleSetFailureOf(err)
      if (failure.kind === 'too_large') warn(tooLargeText(t, failure.limitBytes, size))
      else if (failure.kind === 'stale') await onStale(failure.current)
    } finally { setPending(false) }
  }

  /** Someone saved since this draft started. The draft is not touched; their version is kept to compare with and to overwrite against. */
  async function onStale(current: RuleSet | undefined) {
    let theirs = current
    // The refusal leaves their version out when the file vanished between its two reads; ask once more before saying it is gone.
    if (!theirs) { try { theirs = await getRuleSet(form.slug) } catch { theirs = undefined } }
    setConflict({ current: theirs ?? null })
  }

  async function overwrite() {
    const theirs = conflict?.current
    if (!theirs?.revision) return
    const ok = await confirm({
      title: t('admin:rules.stale_overwrite_title'),
      message: t('admin:rules.stale_overwrite_message'),
      confirmText: t('admin:rules.stale_overwrite_action'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    await save(theirs.revision)
  }

  async function takeTheirs() {
    const ok = await confirm({
      title: t('admin:rules.stale_discard_title'),
      message: t('admin:rules.stale_discard_message'),
      confirmText: t('admin:rules.stale_discard_action'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    const theirs = conflict?.current
    setConflict(null)
    // Without their version in hand the page starts again from a fresh read.
    if (!theirs) { await onReseed(); return }
    adopt(theirs)
    setAttempted(false)
    setRematchSeen(false)
    setRuleIssues([])
    setRev(r => r + 1)
  }

  async function remove() {
    if (!await askDeleteRuleSet(t, seed.slug, usedBy)) return
    setActing(true)
    try {
      await deleteRuleSet(seed.slug)
      pushToast({ message: t('admin:rules.toast.deleted'), severity: 'success' })
      leaving.current = true
      listsChanged()
      void navigate(back, { replace: true })
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  async function restore() {
    if (!await askResetRuleSet(t, seed.slug)) return
    setActing(true)
    try {
      await resetRuleSet(seed.slug)
      pushToast({ message: t('admin:rules.toast.reset'), severity: 'success' })
      listsChanged()
      await onReseed()
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  // Ctrl/Cmd+S saves and "/" goes to the search box, as the design lists.
  const saveRef = useRef(save)
  saveRef.current = save
  const pendingRef = useRef(pending)
  pendingRef.current = pending
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        // Not while a dialog is open (it is the thing being worked on) or a save is already running.
        if (dirtyRef.current && !pendingRef.current && !document.querySelector('[role="dialog"]:not([data-nonmodal]), [role="alertdialog"]')) void saveRef.current()
        return
      }
      const el = e.target as HTMLElement | null
      if (e.key === '/' && !e.metaKey && !e.ctrlKey && !el?.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) {
        // The Rules tab and a sub-rule's editor each have one; the visible one is the one meant.
        const box = [...document.querySelectorAll<HTMLInputElement>('[data-rules-search]')].find(b => b.offsetParent !== null)
        if (box) { e.preventDefault(); box.focus() }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const more: MoreMenuItem[] = []
  if (editing && !readOnly) {
    if (isSeeded(seed)) more.push({ id: 'reset', label: t('admin:rules.reset_to_default'), disabled: acting, onSelect: () => void restore() })
    // A seeded rule set keeps its Delete item, off, saying why, instead of silently having none.
    more.push(isSeeded(seed)
      ? { id: 'delete', label: t('admin:rules.row_delete'), disabledReason: t('admin:rules.cannot_delete_seeded'), icon: IconDelete, destructive: true, disabled: true }
      : { id: 'delete', label: t('admin:rules.row_delete'), icon: IconDelete, destructive: true, disabled: acting, onSelect: () => void remove() })
  }

  // The +N −M on the review button is worked out from the text as it stood when typing paused, so a keystroke is not followed by
  // a split and a diff of the whole document; the saved text is split once, not again for every change of the draft.
  const draftContent = useSettled(form.content, SETTLE_MS)
  const baseLines = useMemo(() => split(baseline.content), [baseline.content])
  const stats = useMemo(() => (dirty ? diffStats(baseLines, split(draftContent)) : { added: 0, removed: 0 }), [dirty, baseLines, draftContent])
  const subCount = form.mihomo_sub_rules?.length ?? 0
  const rematchCount = form.mihomo_rematch_outbounds?.length ?? 0
  const withCount = (label: string, n: number) => (n ? `${label} (${n})` : label)
  const status = t(baseline.enabled ? 'admin:rules.status.enabled' : 'admin:rules.status.disabled')
  const meta = usedBy.length > 0
    ? t('admin:rules.page.meta', { slug: baseline.slug, status, count: usedBy.length })
    : t('admin:rules.page.meta_unused', { slug: baseline.slug, status })

  const title = editing ? (baseline.name || baseline.slug) : t('admin:rules.page.new_title')
  return (
    <ObjectPage
      back={{ to: back, label: t('admin:rules.page.breadcrumb') }}
      title={title}
      badges={editing ? <StatusDot tone={baseline.enabled ? 'success' : 'neutral'} label={status} /> : undefined}
      subtitle={editing ? <span>{meta}</span> : undefined}
      secondary={dirty || (editing && !readOnly) ? (
        <>
          {dirty && (
            <Button variant="outline" onClick={() => setChanges(true)}>{t('admin:rules.changes.button', { added: stats.added, removed: stats.removed })}</Button>
          )}
          {editing && !readOnly && (
            <Button variant="outline" disabled={acting} onClick={() => void navigate(newRuleSetPath(seed.slug), { state: location.state })}>
              <IconCopy />{t('admin:rules.duplicate')}
            </Button>
          )}
        </>
      ) : undefined}
      more={more}
    >
      {conflict && (
        <Notice tone="warning" className="mb-4" action={
          <div className="flex flex-wrap gap-2">
            {conflict.current && <Button variant="outline" size="sm" disabled={pending} onClick={() => setReviewing(true)}>{t('admin:rules.stale_review')}</Button>}
            {conflict.current?.revision && <Button variant="outline" size="sm" disabled={pending} onClick={() => void overwrite()}>{t('admin:rules.stale_overwrite')}</Button>}
            <Button variant="outline" size="sm" disabled={pending} onClick={() => void takeTheirs()}>{t('admin:rules.stale_discard')}</Button>
          </div>
        }>
          <p className="font-medium">{t('admin:rules.stale_title')}</p>
          <p>{conflict.current ? t('admin:rules.stale_body') : t('admin:rules.stale_gone')}</p>
        </Notice>
      )}
      {readOnly && <Alert className="mb-4"><AlertDescription>{t('admin:rules.read_only')}</AlertDescription></Alert>}
      <FormValidityProvider>
        {/* A dialog opened from inside the page is portaled out of the DOM but not out of React's tree, so ITS submit
            reaches this handler too: only this form's own submit saves the rule set. */}
        <form onSubmit={e => { e.preventDefault(); if (e.target === e.currentTarget) void save() }} noValidate>
          {/* A disabled fieldset switches off every native control inside it: the members, sub-rule and Rematch editors while a save runs. */}
          <fieldset disabled={pending} className="min-w-0 border-0 p-0">
          <Tabs value={tab} onValueChange={v => changeTab(v as Tab)}>
            {/* The tabs and, on the Rules tab, its toolbar are one strip that stays under the top bar while the rules scroll (a phone scrolls it away:
                two rows of controls would take a fifth of the screen). One border, on the strip, so the toolbar is not boxed again inside it. */}
            <div ref={strip} data-slot="rules-strip" className={cn(!phone && 'sticky top-(--shell-bar-h) z-20 -mx-4 border-b bg-background px-4 pt-1 pb-2 sm:-mx-6 sm:px-6')}>
              {/* On a phone the tabs are a menu: five of them do not fit a row, and a row that scrolls hides what is off screen. */}
              {phone ? (
                <div className="mb-1">
                  <NativeSelect aria-label={t('admin:rules.tabs.label')} value={tab} onChange={e => changeTab(e.target.value as Tab)}>
                    <option value="rules">{t('admin:rules.tabs.rules')}</option>
                    {!readOnly && <option value="members">{t('admin:rules.tabs.members')}</option>}
                    {!readOnly && <option value="sub_rules">{withCount(t('admin:rules.mihomo.sub_rules'), subCount)}</option>}
                    {!readOnly && <option value="rematches">{withCount(t('admin:rules.mihomo.rematches'), rematchCount)}</option>}
                    <option value="basics">{t('admin:rules.tabs.basics')}</option>
                  </NativeSelect>
                </div>
              ) : (
                <div className="max-w-full overflow-x-auto">
                  <TabsList>
                    <TabsTrigger value="rules">{t('admin:rules.tabs.rules')}</TabsTrigger>
                    {!readOnly && <TabsTrigger value="members">{t('admin:rules.tabs.members')}</TabsTrigger>}
                    {!readOnly && <TabsTrigger value="sub_rules">{withCount(t('admin:rules.mihomo.sub_rules'), subCount)}</TabsTrigger>}
                    {!readOnly && <TabsTrigger value="rematches">{withCount(t('admin:rules.mihomo.rematches'), rematchCount)}</TabsTrigger>}
                    <TabsTrigger value="basics">{t('admin:rules.tabs.basics')}</TabsTrigger>
                  </TabsList>
                </div>
              )}
              {/* Where the Rules tab puts its toolbar. */}
              {!phone && <div ref={setSlot} className="mt-2 empty:hidden" />}
            </div>
            {/* The rules and the basics stay built while another tab is shown (the text and the history are in them); the three that ask the server as they open are built only when shown, as the sheet's were. */}
            <TabsContent value="rules" keepMounted>
              <RulesTab key={rev} content={form.content} setContent={content => setForm(f => ({ ...f, content }))} readOnly={readOnly || pending}
                order={form.proxy_group_order || []} rematchNames={(form.mihomo_rematch_outbounds || []).map(o => o.name)} serverIssues={ruleIssues}
                showTarget={showTarget} cont={cont} explain={{ draft: form, templates }} toolbarSlot={phone ? undefined : tab === 'rules' ? slot : null} groups={{ known: effective !== null, loading: groupsLoading, failed: groupsFailed, failedMessage: groupsFailedMessage, unavailable: readOnly, retry: retryGroups, needed: setNeedsMain }} />
            </TabsContent>
            <TabsContent value="members" keepMounted={false}>
              <ProxyGroupMembersEditor
                content={form.content}
                groupOrder={form.proxy_group_order || []}
                initialGroupOrder={initialOrder}
                onGroupOrderChange={proxy_group_order => setForm(current => ({ ...current, proxy_group_order }))}
                members={form.proxy_group_members || {}}
                initialMembers={initialMembers}
                onChange={proxy_group_members => setForm(current => ({ ...current, proxy_group_members }))}
                options={form.proxy_group_options || {}}
                initialOptions={initialOptions}
                onOptionsChange={proxy_group_options => setForm(current => ({ ...current, proxy_group_options }))}
                previewGroups={groups as Group[]}
                draft={form}
                mihomoSubRules={form.mihomo_sub_rules}
                mihomoRematchOutbounds={form.mihomo_rematch_outbounds}
                usage={usage}
                onRename={name => setRenaming({ name })}
                sharedInspection={{ data: inspection, loading: !fresh }}
                renameBlocked={renameBlocked}
                onShowRules={name => { setShowTarget({ name, token: Date.now() }); changeTab('rules') }}
              />
            </TabsContent>
            <TabsContent value="sub_rules" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="sub_rules" value={form} onChange={setForm}
                renderContent={({ index, generation, value, onChange, ariaLabel }) => (
                  <RulesTab key={`${rev}:${index}:${generation}`} content={value} setContent={onChange} readOnly={readOnly || pending} ariaLabel={ariaLabel}
                    order={form.proxy_group_order || []} rematchNames={(form.mihomo_rematch_outbounds || []).map(o => o.name)} serverIssues={[]} subRule={{ groups: ruleGroups }} cont={cont} groups={{ known: effective !== null, loading: groupsLoading, failed: groupsFailed, failedMessage: groupsFailedMessage, unavailable: readOnly, retry: retryGroups, needed: setNeedsSub }} />
                )} />
            </TabsContent>
            <TabsContent value="rematches" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="rematches" value={form} onChange={setForm} revealRematchIssues={rematchSeen} />
            </TabsContent>
            <TabsContent value="basics" keepMounted>
              <BasicsTab active={tab === 'basics'} form={form} setForm={setForm} editing={editing} readOnly={readOnly || pending} slugError={slugError} nameError={nameError} usedBy={usedBy} />
            </TabsContent>
          </Tabs>
          </fieldset>
          {!readOnly && (
            <SaveBar dirty={dirty} pending={pending} onSave={() => void save()}
              onDiscard={() => { setForm(cloneRuleSet(editing ? baseline : seed)); setAttempted(false); setRematchSeen(false); setRuleIssues([]); setRev(r => r + 1) }} />
          )}
        </form>
      </FormValidityProvider>
      {renaming && effective && (
        <RenameGroupDialog draft={form} from={renaming.name} effective={effective} reserved={reserved} templates={usedBy} rematchNames={rematchNames}
          onApply={applyRename} onClose={() => setRenaming(null)} />
      )}
      {reviewing && conflict?.current && <ConflictDialog base={baseline} theirs={conflict.current} mine={form} onClose={() => setReviewing(false)} />}
      {changes && <ChangesDialog saved={baseline.content} draft={form.content} onClose={() => setChanges(false)} />}
    </ObjectPage>
  )
}

