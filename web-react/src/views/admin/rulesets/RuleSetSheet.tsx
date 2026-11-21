import { lazy, Suspense, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { inspectProxyGroups, saveRuleSet, type RuleSet } from '@/api/rules'
import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { DetailSheet } from '@/components/data/DetailSheet'
import MihomoAdvancedRulesEditor from '@/components/MihomoAdvancedRulesEditor'
import ProxyGroupMembersEditor from '@/components/ProxyGroupMembersEditor'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { IconSpinner } from '@/lib/icons'
import { pruneRuleSetProxyGroupMetadata } from '@/utils/proxyGroupMembers'
import { useSheetGuard } from '../subscription/useSheetGuard'
import { cloneProxyGroupMembers, cloneProxyGroupOptions, cloneRuleSet, EMPTY, sameRuleSet } from './model'

// Lazy-load the CodeMirror editor so its (heavy) deps stay out of the initial SPA bundle: it is fetched only
// when a rule-set sheet opens.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const TABS = ['rules', 'sub_rules', 'rematches', 'members'] as const
type Tab = (typeof TABS)[number]

/** What the sheet is for: a new rule set, a copy of one (saved as new), or an existing one. */
export type SheetTarget = { kind: 'new' } | { kind: 'copy'; draft: RuleSet; source: RuleSet } | { kind: 'edit'; ruleSet: RuleSet }

/**
 * The rule-set editor (design: a long document is edited in a wide sheet with a SaveBar, not a dialog). Four tabs:
 * the main rules, the Mihomo sub-rules, the Rematch outbounds and the proxy-group members. All four edit one
 * draft; one Save sends it, exactly as the old dialog's OK did, in the same order: required fields, the slug
 * check for a new set, the server's inspection (with the orphan-group cleanup the admin must confirm), then the
 * save itself.
 */
export function RuleSetSheet({ target, existingSlugs, groups, onClose, onSaved }: {
  target: SheetTarget | null
  existingSlugs: readonly string[]
  groups: readonly Group[]
  onClose: () => void
  onSaved: (saved: RuleSet, wasEdit: boolean) => void
}) {
  if (!target) return null
  // Keyed so opening another rule set starts from its own draft.
  const key = target.kind === 'edit' ? `edit:${target.ruleSet.slug}` : target.kind === 'copy' ? `copy:${target.source.slug}` : 'new'
  return <SheetBody key={key} target={target} existingSlugs={existingSlugs} groups={groups} onClose={onClose} onSaved={onSaved} />
}

function SheetBody({ target, existingSlugs, groups, onClose, onSaved }: {
  target: SheetTarget
  existingSlugs: readonly string[]
  groups: readonly Group[]
  onClose: () => void
  onSaved: (saved: RuleSet, wasEdit: boolean) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const editing = target.kind === 'edit'
  // What the draft is compared with: the saved rule set, or a blank one for new and copy (a copy is unsaved from the start).
  const baseline = useMemo<RuleSet>(() => (editing ? cloneRuleSet(target.ruleSet) : cloneRuleSet(EMPTY)),
    // The draft is seeded once per opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [])
  const seed = target.kind === 'edit' ? target.ruleSet : target.kind === 'copy' ? target.draft : EMPTY
  const [form, setForm] = useState<RuleSet>(() => cloneRuleSet(seed))
  // The members editor marks groups whose members differ from where the sheet opened. For a copy that is the source's.
  const metaSource = target.kind === 'edit' ? target.ruleSet : target.kind === 'copy' ? target.source : EMPTY
  const [initialOrder] = useState<string[]>(() => [...(metaSource.proxy_group_order || [])])
  const [initialMembers] = useState(() => cloneProxyGroupMembers(metaSource.proxy_group_members))
  const [initialOptions] = useState(() => cloneProxyGroupOptions(metaSource.proxy_group_options))
  const [tab, setTab] = useState<Tab>('rules')
  const [attempted, setAttempted] = useState(false)
  const [rematchSeen, setRematchSeen] = useState(false)
  const [pending, setPending] = useState(false)

  const dirty = !sameRuleSet(form, baseline)
  const slugTaken = !editing && existingSlugs.includes(form.slug)
  const slugError = attempted && !form.slug ? t('admin:rules.validate.slug_name_required')
    : attempted && slugTaken ? t('admin:rules.validate.slug_exists', { slug: form.slug }) : undefined
  const nameError = attempted && !form.name ? t('admin:rules.validate.slug_name_required') : undefined

  const { close, finish } = useSheetGuard({ dirty, pending, onClose })
  const changeTab = (next: Tab) => {
    if (tab === 'rematches' && next !== 'rematches') setRematchSeen(true)
    setTab(next)
  }
  const warn = (key: string) => pushToast({ message: t(key), severity: 'warning' })

  async function save() {
    if (!form.slug || !form.name) {
      setAttempted(true); setTab('rules'); warn('admin:rules.validate.slug_name_required'); return
    }
    // Creating or duplicating with an existing slug would silently overwrite that rule set server-side (Save is
    // a slug-keyed upsert). Block it with a clear error so a copy can't clobber the original; the slug is locked
    // when editing, so this only fires on the create and duplicate flows.
    if (slugTaken) {
      setAttempted(true); setTab('rules'); pushToast({ message: t('admin:rules.validate.slug_exists', { slug: form.slug }), severity: 'warning' }); return
    }
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
      if (errors.length > 0) {
        // Take the person to the part that holds the problem: the sub-rules, the Rematch outbounds or the members.
        const sections = errors.map(issue => issue.section).filter(Boolean)
        const rematch = sections.includes('rematch_outbound')
        const sub = sections.includes('sub_rule') || sections.includes('rules')
        if (rematch) { setRematchSeen(true); setTab('rematches') } else if (sub) setTab('sub_rules'); else setTab('members')
        warn(sections.length > 0 ? 'admin:rules.validate.rule_extensions' : 'admin:rules.validate.proxy_group_members'); return
      }
      const saved = await saveRuleSet(draft)
      pushToast({ message: t('admin:rules.toast.saved'), severity: 'success' })
      finish(() => onSaved(saved, editing))
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  const subCount = form.mihomo_sub_rules?.length ?? 0
  const rematchCount = form.mihomo_rematch_outbounds?.length ?? 0
  const withCount = (label: string, n: number) => (n ? `${label} (${n})` : label)

  return (
    <DetailSheet
      open
      onClose={close}
      title={editing ? t('admin:rules.edit_title') : t('admin:rules.create')}
      description={editing ? form.slug : undefined}
      className="data-[side=right]:sm:w-[min(72rem,100vw)] data-[side=right]:sm:max-w-none"
    >
      <FormValidityProvider>
        <form onSubmit={e => { e.preventDefault(); void save() }} noValidate className="flex flex-col gap-4">
          <Tabs value={tab} onValueChange={v => changeTab(v as Tab)}>
            <TabsList className="max-w-full overflow-x-auto">
              <TabsTrigger value="rules">{t('admin:rules.tabs.rules')}</TabsTrigger>
              <TabsTrigger value="sub_rules">{withCount(t('admin:rules.mihomo.sub_rules'), subCount)}</TabsTrigger>
              <TabsTrigger value="rematches">{withCount(t('admin:rules.mihomo.rematches'), rematchCount)}</TabsTrigger>
              <TabsTrigger value="members">{t('admin:rules.tabs.members')}</TabsTrigger>
            </TabsList>
            {/* Only the shown tab is mounted: the editors fetch from the server as they open, and the fields that can block Save sit on the first tab, which save() takes the person back to. */}
            <TabsContent value="rules" keepMounted={false}>
              <div className="flex flex-col gap-4">
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <Field label={t('admin:rules.field.slug')} required error={slugError}>
                    <Input value={form.slug} disabled={editing} placeholder={t('admin:rules.placeholder.slug')}
                      onChange={e => setForm({ ...form, slug: e.target.value })} />
                  </Field>
                  <Field label={t('admin:rules.field.name')} required error={nameError}>
                    <Input value={form.name} placeholder={t('admin:rules.placeholder.name')}
                      onChange={e => setForm({ ...form, name: e.target.value })} />
                  </Field>
                  <Field label={t('admin:rules.field.sort')}>
                    <Input type="number" value={form.sort} onChange={e => setForm({ ...form, sort: Number(e.target.value) })} />
                  </Field>
                  <Field label={t('admin:rules.field.enabled')} orientation="horizontal">
                    <Switch checked={form.enabled} onCheckedChange={c => setForm({ ...form, enabled: c })} />
                  </Field>
                </div>
                <Field label={t('admin:rules.field.direct_subscription_domain')} hint={t('admin:rules.hint.direct_subscription_domain')} orientation="horizontal">
                  <Switch checked={form.direct_subscription_domain} onCheckedChange={c => setForm({ ...form, direct_subscription_domain: c })} />
                </Field>
                <div className="flex flex-col gap-1.5">
                  <span className="text-sm font-medium">{t('admin:rules.field.content')}</span>
                  <div className="overflow-hidden rounded-lg border">
                    <Suspense fallback={<div className="grid h-96 place-items-center"><IconSpinner aria-hidden className="animate-spin text-muted-foreground" /></div>}>
                      <CodeEditor value={form.content} onChange={v => setForm(f => ({ ...f, content: v }))} ariaLabel={t('admin:rules.field.content')} />
                    </Suspense>
                  </div>
                  <p className="text-xs text-muted-foreground">{t('admin:rules.editor_keyboard_hint')}</p>
                </div>
              </div>
            </TabsContent>
            <TabsContent value="sub_rules" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="sub_rules" value={form} onChange={setForm} />
            </TabsContent>
            <TabsContent value="rematches" keepMounted={false}>
              <MihomoAdvancedRulesEditor part="rematches" value={form} onChange={setForm} revealRematchIssues={rematchSeen} />
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
          </Tabs>
          <SaveBar dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { setForm(cloneRuleSet(seed)); setAttempted(false); setRematchSeen(false) }} />
        </form>
      </FormValidityProvider>
    </DetailSheet>
  )
}
