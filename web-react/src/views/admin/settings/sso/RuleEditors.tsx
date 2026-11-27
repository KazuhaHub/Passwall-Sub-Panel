import type { Group as GroupRow } from '@/api/types'
import type { SSOGroupRule, SSORoleRule } from '@/api/settings'
import { InfoTip } from '@/components/common/HelpTip'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { IconAdd } from '@/lib/icons'
import { useAdminT } from '../../useAdminT'
import { RowTools, SuggestInput, moved } from '../parts'

// Panel roles are free text: an admin may plan for a custom role before the backend knows it, so the built-in ones
// are only suggestions.
const BUILTIN_ROLES = ['admin', 'operator', 'user']

/** The label of the "keep on miss" switch with its explanation one tap away. */
function KeepLabel({ hint }: { hint: string }) {
  const t = useAdminT()
  return <span className="inline-flex items-center gap-1">{t('settings.sso.role_rules_keep')}<InfoTip text={hint} /></span>
}

function RuleRow({ index, count, name, onMove, onRemove, children }: {
  index: number
  count: number
  name: string
  onMove: (to: number) => void
  onRemove: () => void
  children: React.ReactNode
}) {
  return (
    <li className="grid gap-3 rounded-lg border bg-muted/20 p-3" aria-label={name}>
      <div className="flex items-start gap-2">
        <div className="grid min-w-0 flex-1 grid-cols-1 gap-3 sm:grid-cols-2">{children}</div>
        <div className="pt-6"><RowTools index={index} count={count} name={name} onMove={onMove} onRemove={onRemove} /></div>
      </div>
    </li>
  )
}

/**
 * Attribute-driven role mapping. Rules are matched in order and the first match wins, so the order is the priority,
 * and the move buttons are how it is changed. Each rule has a Keep switch (what a miss does to the role the user has)
 * and a free note that never affects matching.
 */
export function RoleRulesEditor({ value, onChange }: { value: SSORoleRule[]; onChange: (rules: SSORoleRule[]) => void }) {
  const t = useAdminT()
  const rules = value ?? []
  const patchRule = (i: number, p: Partial<SSORoleRule>) => onChange(rules.map((r, idx) => (idx === i ? { ...r, ...p } : r)))
  return (
    <div className="grid gap-3">
      <p className="text-sm text-muted-foreground">{t('settings.sso.role_rules_hint')}</p>
      {rules.length === 0 && <p className="text-xs text-muted-foreground italic">{t('settings.sso.role_rules_empty')}</p>}
      <ol className="grid gap-3">
        {rules.map((r, i) => (
          <RuleRow key={i} index={i} count={rules.length} name={t('settings.sso.role_rule_name', { n: i + 1 })}
            onMove={to => onChange(moved(rules, i, to))} onRemove={() => onChange(rules.filter((_, idx) => idx !== i))}>
            <Field label={t('settings.sso.role_rules_attribute')}>
              <Input value={r.attribute} placeholder={t('settings.sso.role_rules_attribute_placeholder')} onChange={e => patchRule(i, { attribute: e.target.value })} />
            </Field>
            <Field label={t('settings.sso.role_rules_value')}>
              <Input value={r.value} onChange={e => patchRule(i, { value: e.target.value })} />
            </Field>
            <Field label={t('settings.sso.role_rules_role')}>
              <SuggestInput value={r.role} options={BUILTIN_ROLES} onChange={role => patchRule(i, { role })} />
            </Field>
            <Field orientation="horizontal" label={<KeepLabel hint={t('settings.sso.role_rules_keep_hint')} />}>
              <Switch checked={!!r.keep} onCheckedChange={c => patchRule(i, { keep: c })} />
            </Field>
            <div className="sm:col-span-2">
              <Field label={t('settings.sso.role_rules_note')}>
                <Input value={r.note ?? ''} placeholder={t('settings.sso.role_rules_note_placeholder')} onChange={e => patchRule(i, { note: e.target.value })} />
              </Field>
            </div>
          </RuleRow>
        ))}
      </ol>
      <div><Button type="button" variant="outline" onClick={() => onChange([...rules, { attribute: '', value: '', role: 'admin', keep: false, note: '' }])}><IconAdd />{t('settings.sso.role_rules_add')}</Button></div>
    </div>
  )
}

/**
 * Attribute-driven group (OU) placement, the same shape as the role rules because one server-side predicate matches
 * both. The target is a pick from the real groups, not free text: a slug that does not resolve refuses to provision
 * new principals and is skipped for existing ones, and both show up at somebody's login rather than here.
 * `loaded` is false while the catalogue loads or after it failed: an empty list means nothing then, and a saved rule
 * must not be called "this group no longer exists" because a request dropped.
 */
export function GroupRulesEditor({ value, onChange, groups, loaded }: {
  value: SSOGroupRule[]
  onChange: (rules: SSOGroupRule[]) => void
  groups: GroupRow[]
  loaded: boolean
}) {
  const t = useAdminT()
  const rules = value ?? []
  const patchRule = (i: number, p: Partial<SSOGroupRule>) => onChange(rules.map((r, idx) => (idx === i ? { ...r, ...p } : r)))
  return (
    <div className="grid gap-3">
      <p className="text-sm text-muted-foreground">{t('settings.sso.group_rules_hint')}</p>
      {rules.length === 0 && <p className="text-xs text-muted-foreground italic">{t('settings.sso.group_rules_empty')}</p>}
      <ol className="grid gap-3">
        {rules.map((r, i) => {
          // A rule may point at a group deleted since: keep the value selectable so opening the form does not rewrite it, and mark it.
          const gone = loaded && !!r.group && !groups.some(g => g.slug === r.group)
          return (
            <RuleRow key={i} index={i} count={rules.length} name={t('settings.sso.group_rule_name', { n: i + 1 })}
              onMove={to => onChange(moved(rules, i, to))} onRemove={() => onChange(rules.filter((_, idx) => idx !== i))}>
              <Field label={t('settings.sso.role_rules_attribute')}>
                <Input value={r.attribute} placeholder={t('settings.sso.role_rules_attribute_placeholder')} onChange={e => patchRule(i, { attribute: e.target.value })} />
              </Field>
              <Field label={t('settings.sso.role_rules_value')}>
                <Input value={r.value} onChange={e => patchRule(i, { value: e.target.value })} />
              </Field>
              <Field label={t('settings.sso.group_rules_group')} error={gone ? t('settings.sso.group_rules_missing') : undefined} errorBlocksSave={false}>
                <NativeSelect value={r.group} onChange={e => patchRule(i, { group: e.target.value })}>
                  {(gone || (!loaded && !!r.group && !groups.some(g => g.slug === r.group))) && <option value={r.group}>{r.group}</option>}
                  {groups.map(g => <option key={g.slug} value={g.slug}>{g.name || g.slug}</option>)}
                </NativeSelect>
              </Field>
              <Field orientation="horizontal" label={<KeepLabel hint={t('settings.sso.group_rules_keep_hint')} />}>
                <Switch checked={!!r.keep} onCheckedChange={c => patchRule(i, { keep: c })} />
              </Field>
              <div className="sm:col-span-2">
                <Field label={t('settings.sso.role_rules_note')}>
                  <Input value={r.note ?? ''} placeholder={t('settings.sso.role_rules_note_placeholder')} onChange={e => patchRule(i, { note: e.target.value })} />
                </Field>
              </div>
            </RuleRow>
          )
        })}
      </ol>
      <div>
        {/* A rule with no group is skipped by the matcher and refused by the server, so a new row starts from a real one. */}
        <Button type="button" variant="outline" disabled={!loaded || groups.length === 0}
          onClick={() => onChange([...rules, { attribute: '', value: '', group: groups[0]?.slug ?? '', keep: false, note: '' }])}>
          <IconAdd />{t('settings.sso.group_rules_add')}
        </Button>
      </div>
    </div>
  )
}
