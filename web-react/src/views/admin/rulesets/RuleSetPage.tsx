import { useDeferredValue, useEffect, useMemo, useRef, useState, type MutableRefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import { deleteRuleSet, inspectProxyGroups, resetRuleSet, saveRuleSet, type ProxyGroupIssue, type RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import type { Group, ListResponse } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusDot } from '@/components/common/StatusDot'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import MihomoAdvancedRulesEditor from '@/components/MihomoAdvancedRulesEditor'
import ProxyGroupMembersEditor from '@/components/ProxyGroupMembersEditor'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTabParam } from '@/hooks/useTabParam'
import { IconCopy, IconDelete } from '@/lib/icons'
import { ruleKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { diffStats } from '@/utils/ruleset/diff'
import { pruneRuleSetProxyGroupMetadata } from '@/utils/proxyGroupMembers'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { askDeleteRuleSet, askResetRuleSet, isSeeded } from './actions'
import { BasicsTab } from './BasicsTab'
import { ChangesDialog } from './ChangesDialog'
import { cloneProxyGroupMembers, cloneProxyGroupOptions, cloneRuleSet, EMPTY, sameRuleSet } from './model'
import { RulesTab } from './RulesTab'
import { isReservedSlug, newRuleSetPath, ruleSetPath } from './ruleSetRoutes'

/** The request body limit of the rule-set endpoints: 1 MiB for everything the middleware does not name (router.go BodyLimitByPath). */
export const MAX_BODY_BYTES = 1 << 20

const TABS = ['rules', 'members', 'sub_rules', 'rematches', 'basics'] as const
type Tab = (typeof TABS)[number]

const bytesOf = (value: unknown) => new Blob([JSON.stringify(value)]).size
const mb = (n: number) => `${(n / (1 << 20)).toFixed(2)} MB`
const split = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))

/**
 * One rule set as a page, for an existing one and for the create form (`seed` is then a blank rule set, or a copy).
 * The save is the old sheet's, in the same order: required fields, the slug check for a new set, the server's inspection
 * (with the orphan-group cleanup the admin must confirm), then the PUT itself, with the same body. What is new is a size
 * check before any of that, because the server refuses a body over 1 MiB with an error that says nothing about why.
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
  // Bumped on Discard so the rules tab starts again from the restored text with a clean history.
  const [rev, setRev] = useState(0)

  const dirty = !readOnly && !sameRuleSet(form, baseline)
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t), bypass: leaving })

  const slugTaken = !editing && existingSlugs.includes(form.slug)
  const slugReserved = !editing && isReservedSlug(form.slug)
  const slugError = attempted && !form.slug ? t('admin:rules.validate.slug_name_required')
    : attempted && slugTaken ? t('admin:rules.validate.slug_exists', { slug: form.slug })
    : attempted && slugReserved ? t('admin:rules.validate.slug_reserved', { slug: form.slug }) : undefined
  const nameError = attempted && !form.name ? t('admin:rules.validate.slug_name_required') : undefined
  const usedBy = useMemo(() => templates.filter(tpl => (tpl.rule_sets || []).includes(seed.slug)).map(x => x.name || x.slug), [templates, seed.slug])

  const warn = (message: string) => pushToast({ message, severity: 'warning' })
  const changeTab = (next: Tab) => {
    if (tab === 'rematches' && next !== 'rematches') setRematchSeen(true)
    setTab(next)
  }

  function patchList(update: (prev: RuleSet[]) => RuleSet[]) {
    queryClient.setQueryData<ListResponse<RuleSet>>(ruleKeys.list(scope, {}), prev => (prev ? { ...prev, items: update(prev.items) } : prev))
  }

  async function save() {
    // Save and Enter must not start a second save while one is in flight.
    if (pending || readOnly) return
    if (!form.slug || !form.name) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_name_required')); return }
    // Creating or duplicating with an existing slug would silently overwrite that rule set server-side (Save is a
    // slug-keyed upsert); the slug is locked when editing, so this only fires on the create and duplicate flows.
    if (slugTaken) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_exists', { slug: form.slug })); return }
    if (slugReserved) { setAttempted(true); changeTab('basics'); warn(t('admin:rules.validate.slug_reserved', { slug: form.slug })); return }
    // The server refuses a larger body in a middleware, with nothing the person could act on; say it here, before any request.
    const size = bytesOf(form)
    if (size > MAX_BODY_BYTES) { warn(t('admin:rules.too_large', { size: mb(size), limit: mb(MAX_BODY_BYTES) })); return }
    setPending(true)
    try {
      let draft = form
      const inspect = (d: RuleSet) => inspectProxyGroups({
        content: d.content,
        proxy_group_members: d.proxy_group_members || {},
        proxy_group_options: d.proxy_group_options || {},
        mihomo_sub_rules: d.mihomo_sub_rules || [],
        mihomo_rematch_outbounds: d.mihomo_rematch_outbounds || [],
      })
      let inspection = await inspect(draft)
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
      const saved = await saveRuleSet(draft)
      pushToast({ message: t('admin:rules.toast.saved'), severity: 'success' })
      if (editing) {
        patchList(prev => prev.map(item => (item.slug === saved.slug ? saved : item)))
        setForm(cloneRuleSet(saved))
        setBaseline(cloneRuleSet(saved))
        setInitialOrder([...(saved.proxy_group_order || [])])
        setInitialMembers(cloneProxyGroupMembers(saved.proxy_group_members))
        setInitialOptions(cloneProxyGroupOptions(saved.proxy_group_options))
      } else {
        leaving.current = true
        patchList(prev => [...prev, saved])
        void queryClient.invalidateQueries({ queryKey: ruleKeys.lists(scope) })
        // The new rule set's own page, with the list's place still behind its back link.
        void navigate(ruleSetPath(saved.slug), { replace: true, state: location.state })
      }
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  async function remove() {
    if (!await askDeleteRuleSet(t, seed.slug, usedBy)) return
    setActing(true)
    try {
      await deleteRuleSet(seed.slug)
      pushToast({ message: t('admin:rules.toast.deleted'), severity: 'success' })
      leaving.current = true
      patchList(prev => prev.filter(item => item.slug !== seed.slug))
      void navigate(back, { replace: true })
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  async function restore() {
    if (!await askResetRuleSet(t, seed.slug)) return
    setActing(true)
    try {
      await resetRuleSet(seed.slug)
      pushToast({ message: t('admin:rules.toast.reset'), severity: 'success' })
      await onReseed()
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  // Ctrl/Cmd+S saves and "/" goes to the search box, as the design lists.
  const saveRef = useRef(save)
  saveRef.current = save
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        if (dirtyRef.current) void saveRef.current()
        return
      }
      const el = e.target as HTMLElement | null
      if (e.key === '/' && !e.metaKey && !e.ctrlKey && !el?.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) {
        const box = document.querySelector<HTMLInputElement>('[data-rules-search]')
        if (box && box.offsetParent !== null) { e.preventDefault(); box.focus() }
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

  // The +N −M on the review button is worked out from the deferred text, so a keystroke or an edit is not held up by a diff of the whole document.
  const draftContent = useDeferredValue(form.content)
  const stats = useMemo(() => (dirty ? diffStats(split(baseline.content), split(draftContent)) : { added: 0, removed: 0 }), [dirty, baseline.content, draftContent])
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
      {readOnly && <Alert className="mb-4"><AlertDescription>{t('admin:rules.read_only')}</AlertDescription></Alert>}
      <FormValidityProvider>
        {/* A dialog opened from inside the page is portaled out of the DOM but not out of React's tree, so ITS submit
            reaches this handler too: only this form's own submit saves the rule set. */}
        <form onSubmit={e => { e.preventDefault(); if (e.target === e.currentTarget) void save() }} noValidate>
          <Tabs value={tab} onValueChange={v => changeTab(v as Tab)}>
            <div className="max-w-full overflow-x-auto">
              <TabsList>
                <TabsTrigger value="rules">{t('admin:rules.tabs.rules')}</TabsTrigger>
                {!readOnly && <TabsTrigger value="members">{t('admin:rules.tabs.members')}</TabsTrigger>}
                {!readOnly && <TabsTrigger value="sub_rules">{withCount(t('admin:rules.mihomo.sub_rules'), subCount)}</TabsTrigger>}
                {!readOnly && <TabsTrigger value="rematches">{withCount(t('admin:rules.mihomo.rematches'), rematchCount)}</TabsTrigger>}
                <TabsTrigger value="basics">{t('admin:rules.tabs.basics')}</TabsTrigger>
              </TabsList>
            </div>
            {/* The rules and the basics stay built while another tab is shown (the text and the history are in them); the three that ask the server as they open are built only when shown, as the sheet's were. */}
            <TabsContent value="rules" keepMounted>
              <RulesTab key={rev} content={form.content} setContent={content => setForm(f => ({ ...f, content }))} readOnly={readOnly}
                order={form.proxy_group_order || []} rematchNames={(form.mihomo_rematch_outbounds || []).map(o => o.name)} serverIssues={ruleIssues} />
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
              />
            </TabsContent>
            <TabsContent value="sub_rules" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="sub_rules" value={form} onChange={setForm} />
            </TabsContent>
            <TabsContent value="rematches" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="rematches" value={form} onChange={setForm} revealRematchIssues={rematchSeen} />
            </TabsContent>
            <TabsContent value="basics" keepMounted>
              <BasicsTab form={form} setForm={setForm} editing={editing} readOnly={readOnly} slugError={slugError} nameError={nameError} usedBy={usedBy} />
            </TabsContent>
          </Tabs>
          {!readOnly && (
            <SaveBar dirty={dirty} pending={pending} onSave={() => void save()}
              onDiscard={() => { setForm(cloneRuleSet(editing ? baseline : seed)); setAttempted(false); setRematchSeen(false); setRuleIssues([]); setRev(r => r + 1) }} />
          )}
        </form>
      </FormValidityProvider>
      {changes && <ChangesDialog saved={baseline.content} draft={form.content} onClose={() => setChanges(false)} />}
    </ObjectPage>
  )
}

