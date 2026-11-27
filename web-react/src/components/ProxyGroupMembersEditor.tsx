import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  inspectProxyGroups,
  previewProxyGroups,
  type GroupSubscriptionPreview,
  type RuleSet,
  type ProxyGroupInspection,
  type ProxyGroupMember,
  type ProxyGroupOptions,
  type ProxyGroupType,
  type MihomoRematchOutbound,
  type MihomoSubRule,
} from '@/api/rules'
import type { Group } from '@/api/types'
import { LevelAlert } from '@/components/LevelAlert'
import { OptionCombobox, type ComboOption } from '@/components/OptionCombobox'
import { IconButton } from '@/components/common/IconButton'
import { Field } from '@/components/form/Field'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { IconAdd, IconArrowDown, IconArrowUp, IconDelete, IconDrag, IconEdit, IconReset, IconSpinner } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { appendUniqueProxyGroupMember, applyProxyGroupOrder, defaultProxyGroupOptions, proxyGroupMemberIdentity, proxyGroupMemberListsEqual, proxyGroupOptionsEqual, proxyGroupOrderEqual, reorderProxyGroupMembers, reorderProxyGroupNames } from '@/utils/proxyGroupMembers'

type MemberMap = Record<string, ProxyGroupMember[]>
type OptionMap = Record<string, ProxyGroupOptions>

interface Props {
  content: string
  groupOrder: string[]
  initialGroupOrder: string[]
  onGroupOrderChange: (order: string[]) => void
  members: MemberMap
  initialMembers: MemberMap
  onChange: (members: MemberMap) => void
  options: OptionMap
  initialOptions: OptionMap
  onOptionsChange: (options: OptionMap) => void
  previewGroups: Group[]
  draft?: RuleSet
  mihomoSubRules?: MihomoSubRule[]
  mihomoRematchOutbounds?: MihomoRematchOutbound[]
  onValidationChange?: (hasErrors: boolean) => void
  /**
   * What the rules say about a group, for the heading (design 3.11): how many groups of rules and how many rules name it. All
   * four props below are optional and come together; without them the editor is the plain members editor it always was.
   */
  usage?: (name: string) => { blocks: number; rules: number }
  /** Asks the page to rename the group. `groupNames` is every group the server lists, for the name check. */
  onRename?: (name: string, groupNames: string[]) => void
  /** Why renaming is not possible now (shown on the disabled button), or undefined when it is. */
  renameBlocked?: string
  /** Shows the rules that name the group on the Rules tab. */
  onShowRules?: (name: string) => void
  /**
   * The inspection of this draft, when the page already asks for it (the routing test and the rename use it too): the editor then
   * shows that one and sends no request of its own. `loading` is true while the answer is for an older draft.
   */
  sharedInspection?: { data: ProxyGroupInspection | null; loading: boolean }
}

type AddKind = 'node' | 'line' | 'builtin' | 'proxy_group' | 'rematch' | 'region' | 'tag' | 'remaining'
type AddOption = ComboOption<string | number>

export default function ProxyGroupMembersEditor({ content, groupOrder, initialGroupOrder, onGroupOrderChange, members, initialMembers, onChange, options, initialOptions, onOptionsChange, previewGroups, draft, mihomoSubRules = [], mihomoRematchOutbounds = [], onValidationChange, usage, onRename, renameBlocked, onShowRules, sharedInspection }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const [inspection, setInspection] = useState<ProxyGroupInspection | null>(null)
  const [selected, setSelected] = useState('')
  const [previewGroupID, setPreviewGroupID] = useState<number | undefined>()
  const [resolvedPreviewGroupID, setResolvedPreviewGroupID] = useState<number | undefined>()
  const [preview, setPreview] = useState<GroupSubscriptionPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [loading, setLoading] = useState(false)
  const [addKind, setAddKind] = useState<AddKind>('node')
  const [addValue, setAddValue] = useState<string | number | null>(null)
  const [lineNodeID, setLineNodeID] = useState<number | null>(null)
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [groupDragIndex, setGroupDragIndex] = useState<number | null>(null)

  const serializedMembers = useMemo(() => JSON.stringify(members), [members])
  const serializedOptions = useMemo(() => JSON.stringify(options), [options])
  const serializedMihomo = useMemo(() => JSON.stringify([mihomoSubRules, mihomoRematchOutbounds]), [mihomoSubRules, mihomoRematchOutbounds])
  useEffect(() => {
    if (!sharedInspection) return
    setInspection(sharedInspection.data)
    setLoading(sharedInspection.loading)
    const first = sharedInspection.data?.groups
    if (first && (!selected || !first.some(group => group.name === selected))) setSelected(first[0]?.name || '')
  }, [sharedInspection?.data, sharedInspection?.loading]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (sharedInspection) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setLoading(true)
      void inspectProxyGroups({
        content,
        proxy_group_members: members,
        proxy_group_options: options,
        mihomo_sub_rules: mihomoSubRules,
        mihomo_rematch_outbounds: mihomoRematchOutbounds,
      }, controller.signal).then(result => {
        if (controller.signal.aborted) return
        setInspection(result)
        const hasErrors = result.issues.some(issue => issue.level === 'error')
        onValidationChange?.(hasErrors)
        if (!selected || !result.groups.some(group => group.name === selected)) {
          setSelected(result.groups[0]?.name || '')
        }
      }).catch(error => {
        if (!controller.signal.aborted && error?.code !== 'ERR_CANCELED') onValidationChange?.(true)
      }).finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    }, 300)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [content, serializedMembers, serializedOptions, serializedMihomo]) // eslint-disable-line react-hooks/exhaustive-deps

  const serializedDraft = JSON.stringify(draft)
  useEffect(() => {
    const controller = new AbortController()
    setPreview(null)
    setPreviewError('')
    if (!draft) return () => controller.abort()
    setPreviewLoading(true)
    const timer = window.setTimeout(() => {
      void previewProxyGroups({ draft, preview_group_id: previewGroupID }, controller.signal).then(result => {
        if (!controller.signal.aborted) { setPreview(result); setResolvedPreviewGroupID(result.group_id) }
      }).catch(error => {
        if (!controller.signal.aborted) {
          setPreviewError(error?.response?.data?.error || t('admin:rules.members.preview_failed'))
          if (error?.response?.data?.group_id > 0) setResolvedPreviewGroupID(error.response.data.group_id)
        }
      }).finally(() => { if (!controller.signal.aborted) setPreviewLoading(false) })
    }, 300)
    return () => { controller.abort(); window.clearTimeout(timer) }
  }, [serializedDraft, previewGroupID]) // eslint-disable-line react-hooks/exhaustive-deps

  const current = inspection?.groups.find(group => group.name === selected)
  const currentPreview = preview?.groups.find(group => group.name === selected)
  const usedBy = useMemo(() => (usage && current ? usage(current.name) : undefined), [usage, current])
  const currentConfigured = Object.prototype.hasOwnProperty.call(members, selected)
  const currentOptionsConfigured = Object.prototype.hasOwnProperty.call(options, selected)
  const currentMembers = members[selected] || current?.default_members || []
  const configuredOptions = options[selected]
  const currentOptions = configuredOptions
    ? { ...defaultProxyGroupOptions(configuredOptions.type), ...configuredOptions }
    : current?.options || defaultProxyGroupOptions('select')
  const currentType = currentOptions.type || 'select'
  const emphasizeFirst = currentType === 'select' || currentType === 'fallback'
  const currentIssues = (inspection?.issues || []).filter(issue => !issue.group || issue.group === selected)
  const orderedGroups = useMemo(() => applyProxyGroupOrder(inspection?.groups || [], groupOrder), [inspection, groupOrder])
  const initialOrderedGroups = useMemo(() => applyProxyGroupOrder(inspection?.groups || [], initialGroupOrder), [inspection, initialGroupOrder])
  const groupOrderDirty = !proxyGroupOrderEqual(orderedGroups.map(group => group.name), initialOrderedGroups.map(group => group.name))

  function commit(next: ProxyGroupMember[]) {
    if (!selected) return
    onChange({ ...members, [selected]: next })
  }

  function resetCurrent() {
    if (!selected) return
    const nextMembers = { ...members }
    delete nextMembers[selected]
    onChange(nextMembers)
    const nextOptions = { ...options }
    delete nextOptions[selected]
    onOptionsChange(nextOptions)
  }

  function changeType(type: ProxyGroupType) {
    if (!selected) return
    const next = { ...options }
    const defaults = defaultProxyGroupOptions(type)
    next[selected] = { ...defaults, ...currentOptions, type }
    onOptionsChange(next)
  }

  function updateOptions(patch: Partial<ProxyGroupOptions>) {
    if (!selected) return
    onOptionsChange({ ...options, [selected]: { ...currentOptions, ...patch } })
  }

  function move(from: number, to: number) {
    if (to < 0 || to >= currentMembers.length || from === to) return
    commit(reorderProxyGroupMembers(currentMembers, from, to))
  }

  function moveGroup(from: number, to: number) {
    if (to < 0 || to >= orderedGroups.length || from === to) return
    const next = reorderProxyGroupNames(orderedGroups.map(group => group.name), from, to)
    const initialEffective = initialOrderedGroups.map(group => group.name)
    onGroupOrderChange(proxyGroupOrderEqual(next, initialEffective) ? [...initialGroupOrder] : next)
  }

  function remove(index: number) {
    if (currentMembers.length <= 1) return
    commit(currentMembers.filter((_, i) => i !== index))
  }

  function memberKey(member: ProxyGroupMember) {
    return proxyGroupMemberIdentity(member)
  }

  function memberLabel(member: ProxyGroupMember) {
    if (member.kind === 'line') {
      const line = inspection?.lines?.find(line => line.node_id === member.node_id && line.line_id === member.line_id)
      return line ? line.display_name : t('admin:rules.members.missing_line', { id: member.line_id })
    }
    if (member.kind === 'node') {
      const node = inspection?.nodes.find(n => n.id === member.node_id)
      return node ? node.display_name : t('admin:rules.members.missing_node', { id: member.node_id })
    }
    if (member.kind === 'node_set') {
      if (member.value === 'remaining') return t('admin:rules.members.remaining')
      if (member.value?.startsWith('region:')) return t('admin:rules.members.region_set', { value: member.value.slice(7) })
      if (member.value?.startsWith('tag:')) return t('admin:rules.members.tag_set', { value: member.value.slice(4) })
    }
    return member.value || member.kind
  }

  // Type prefixes belong only to the sortable editor cards, never to saved
  // identities, candidate labels or server-compiled subscription previews.
  function memberOrderLabel(member: ProxyGroupMember) {
    const value = memberLabel(member)
    if (member.kind === 'line') {
      const line = inspection?.lines?.find(line => line.node_id === member.node_id && line.line_id === member.line_id)
      const kind = line?.kind || (member.line_id === 'direct' ? 'direct' : 'line')
      return t(`admin:rules.members.member_label_${kind}`, { value })
    }
    if (['node', 'builtin', 'proxy_group', 'rematch'].includes(member.kind)) {
      return t(`admin:rules.members.member_label_${member.kind}`, { value })
    }
    return value
  }

  function issueLabel(issue: NonNullable<ProxyGroupInspection['issues']>[number]) {
    if (!issue.code) return issue.message
    return t(`admin:rules.members.issues.${issue.code}`, { ...issue.params, defaultValue: issue.message })
  }

  const lineNode = inspection?.nodes.find(node => node.id === lineNodeID)
  const lineNodeOptions = useMemo<AddOption[]>(() => (inspection?.nodes || []).map(node => ({ value: node.id, label: node.display_name })), [inspection])
  const addOptions = useMemo<AddOption[]>(() => {
    switch (addKind) {
      case 'line': return (inspection?.lines || []).filter(line => line.node_id === lineNode?.id).map(line => ({ value: JSON.stringify([line.node_id, line.line_id]), label: `${line.kind === 'direct' ? '' : `${line.name} `}[${t(`admin:rules.members.line_type_${line.kind}`)}]`, note: line.available ? undefined : t('admin:rules.members.line_unavailable') }))
      case 'node': return (inspection?.nodes || []).map(node => ({ value: node.id, label: node.display_name }))
      case 'builtin': return (inspection?.builtins || []).map(value => ({ value, label: value }))
      case 'proxy_group': return (inspection?.groups || []).filter(group => group.name !== selected).map(group => ({ value: group.name, label: group.name }))
      case 'rematch': return mihomoRematchOutbounds.filter(outbound => outbound.name.trim()).map(outbound => ({ value: outbound.name, label: outbound.name }))
      case 'region': return (inspection?.regions || []).map(value => ({ value, label: value }))
      case 'tag': return (inspection?.tags || []).map(value => ({ value, label: value }))
      case 'remaining': return [{ value: 'remaining', label: t('admin:rules.members.remaining') }]
    }
  }, [addKind, inspection, lineNode?.id, mihomoRematchOutbounds, selected, t])

  const selectedAddOption = addOptions.find(option => option.value === addValue) || null
  const noMatch = t('admin:rules.members.no_match')

  function addMember() {
    if (addKind === 'line' && (!lineNode || !selectedAddOption)) return
    let member: ProxyGroupMember | null = null
    if (addKind === 'remaining') member = { kind: 'node_set', value: 'remaining' }
    else if (addValue !== null) {
      if (addKind === 'node') member = { kind: 'node', node_id: Number(addValue) }
      else if (addKind === 'line') {
        const [node_id, line_id] = JSON.parse(String(addValue)) as [number, string]
        member = { kind: 'line', node_id, line_id }
      }
      else if (addKind === 'region') member = { kind: 'node_set', value: `region:${addValue}` }
      else if (addKind === 'tag') member = { kind: 'node_set', value: `tag:${addValue}` }
      else member = { kind: addKind, value: String(addValue) }
    }
    if (!member) return
    const next = appendUniqueProxyGroupMember(currentMembers, member)
    if (next === currentMembers) return
    commit(next)
    setAddValue(null)
  }

  const dot = (title: string) => (
    <span role="img" aria-label={title} title={title} className="size-2 shrink-0 rounded-full bg-primary" />
  )

  return (
    <div className="grid min-h-[35rem] grid-cols-1 overflow-hidden rounded-lg border md:h-[35rem] md:grid-cols-[17.5rem_minmax(0,1fr)]">
      <div className="flex min-h-0 flex-col border-b bg-muted/30 md:border-r md:border-b-0">
        <div className="flex items-center justify-between gap-2 border-b p-3">
          <div className="flex min-w-0 items-center gap-2">
            <h3 className="text-sm font-semibold">{t('admin:rules.members.groups_title')}</h3>
            {groupOrderDirty && dot(t('admin:rules.members.order_unsaved'))}
          </div>
          <IconButton label={t('admin:rules.members.restore_group_order')} size="icon-sm" onClick={() => onGroupOrderChange([])}>
            <IconReset />
          </IconButton>
        </div>
        {loading && !inspection ? <div className="flex justify-center p-6"><IconSpinner className="animate-spin text-muted-foreground" aria-hidden /></div> : (
          <ul className="max-h-60 min-h-0 overflow-y-auto md:max-h-none">
            {orderedGroups.map((group, index) => {
              const edited = !proxyGroupMemberListsEqual(members[group.name], initialMembers[group.name]) || !proxyGroupOptionsEqual(options[group.name], initialOptions[group.name])
              return (
                <li key={group.name}
                  draggable
                  onDragStart={() => setGroupDragIndex(index)} onDragEnd={() => setGroupDragIndex(null)}
                  onDragOver={event => event.preventDefault()}
                  onDrop={() => { if (groupDragIndex !== null) moveGroup(groupDragIndex, index); setGroupDragIndex(null) }}
                  className={cn('flex items-center gap-0.5 border-b pr-1 pl-1', group.name === selected && 'bg-accent', groupDragIndex === index && 'opacity-50')}>
                  <IconDrag aria-hidden className="size-4 shrink-0 cursor-grab text-muted-foreground" />
                  <button type="button" aria-current={group.name === selected ? 'true' : undefined} onClick={() => setSelected(group.name)}
                    className="min-w-0 flex-1 rounded-md px-1.5 py-2 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
                    <span className="block truncate text-sm font-medium" title={group.name}>{group.name}</span>
                    <span className="block truncate text-xs text-muted-foreground">{group.members[0] ? memberLabel(group.members[0]) : '—'}</span>
                  </button>
                  {edited && dot(t('admin:rules.members.unsaved'))}
                  <IconButton label={t('admin:rules.members.move_group_up')} size="icon-xs" disabled={index === 0} onClick={() => moveGroup(index, index - 1)}><IconArrowUp /></IconButton>
                  <IconButton label={t('admin:rules.members.move_group_down')} size="icon-xs" disabled={index === orderedGroups.length - 1} onClick={() => moveGroup(index, index + 1)}><IconArrowDown /></IconButton>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      <div className="min-w-0 overflow-y-auto p-4 sm:p-5">
        {!current ? (
          <p className="py-16 text-center text-sm text-muted-foreground">{t('admin:rules.members.no_groups')}</p>
        ) : <>
          <div className="flex flex-col justify-between gap-2 sm:flex-row sm:items-center">
            <div className="min-w-0">
              <h3 className="text-lg font-semibold break-words">{current.name}</h3>
              <p className="text-xs text-muted-foreground">{t(`admin:rules.members.type_hint.${currentType}`)}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {onRename && (
                <span title={renameBlocked}>
                  <Button variant="outline" size="sm" disabled={renameBlocked !== undefined} onClick={() => onRename(current.name, (inspection?.groups ?? []).map(g => g.name))}>
                    <IconEdit />{t('admin:rules.rename.action')}
                  </Button>
                </span>
              )}
              <Button variant="outline" size="sm" onClick={resetCurrent} disabled={!currentConfigured && !currentOptionsConfigured}>
                <IconReset />{t('admin:rules.members.restore_default')}
              </Button>
            </div>
          </div>
          {renameBlocked !== undefined && <p className="mt-1 text-xs text-muted-foreground">{renameBlocked}</p>}
          {usedBy && (
            <p className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
              <span>{usedBy.rules === 0 ? t('admin:rules.groups.not_used') : t('admin:rules.groups.used_by', { blocks: usedBy.blocks.toLocaleString(), rules: usedBy.rules.toLocaleString() })}</span>
              {onShowRules && usedBy.rules > 0 && (
                <button type="button" className="underline underline-offset-4" onClick={() => onShowRules(current.name)}>{t('admin:rules.groups.show_rules')}</button>
              )}
            </p>
          )}

          <div className="mt-4 flex flex-col gap-3 rounded-lg bg-muted/40 p-3">
            <Field label={t('admin:rules.members.group_type')}>
              <NativeSelect value={currentType} onChange={e => changeType(e.target.value as ProxyGroupType)}>
                <option value="select">{t('admin:rules.members.type.select')}</option>
                <option value="url-test">{t('admin:rules.members.type.url-test')}</option>
                <option value="fallback">{t('admin:rules.members.type.fallback')}</option>
                <option value="load-balance">{t('admin:rules.members.type.load-balance')}</option>
              </NativeSelect>
            </Field>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label={t('admin:rules.members.test_url')} className="sm:col-span-2">
                <Input value={currentOptions.url || ''} onChange={e => updateOptions({ url: e.target.value })} />
              </Field>
              {currentType !== 'select' && <>
                <Field label={t('admin:rules.members.interval')}>
                  <Input type="number" min={0} step={1} value={currentOptions.interval ?? ''} onChange={e => updateOptions({ interval: Number(e.target.value) })} />
                </Field>
                <Field label={t('admin:rules.members.timeout')}>
                  <Input type="number" min={1} step={1} value={currentOptions.timeout ?? ''} onChange={e => updateOptions({ timeout: Number(e.target.value) })} />
                </Field>
                {currentType === 'url-test' && (
                  <Field label={t('admin:rules.members.tolerance')}>
                    <Input type="number" min={0} step={1} value={currentOptions.tolerance ?? ''} onChange={e => updateOptions({ tolerance: Number(e.target.value) })} />
                  </Field>
                )}
                {currentType === 'load-balance' && (
                  <Field label={t('admin:rules.members.strategy')} hint={t(`admin:rules.members.strategy_hint.${currentOptions.strategy || 'consistent-hashing'}`)} className="sm:col-span-2">
                    <NativeSelect value={currentOptions.strategy || 'consistent-hashing'} onChange={e => updateOptions({ strategy: e.target.value as ProxyGroupOptions['strategy'] })}>
                      <option value="round-robin">{t('admin:rules.members.strategy_value.round-robin')}</option>
                      <option value="consistent-hashing">{t('admin:rules.members.strategy_value.consistent-hashing')}</option>
                      <option value="sticky-sessions">{t('admin:rules.members.strategy_value.sticky-sessions')}</option>
                    </NativeSelect>
                  </Field>
                )}
                <Field label={t('admin:rules.members.lazy')} orientation="horizontal" className="sm:col-span-2">
                  <Switch checked={currentOptions.lazy ?? true} onCheckedChange={checked => updateOptions({ lazy: checked })} />
                </Field>
              </>}
            </div>
            <LevelAlert level="info">{t('admin:rules.members.client_compatibility')}</LevelAlert>
          </div>

          <ol className="mt-4 flex flex-col gap-2">
            {currentMembers.map((member, index) => {
              const first = index === 0 && emphasizeFirst
              return (
                <li key={`${memberKey(member)}:${index}`} draggable
                  onDragStart={() => setDragIndex(index)} onDragEnd={() => setDragIndex(null)}
                  onDragOver={e => e.preventDefault()} onDrop={() => { if (dragIndex !== null) move(dragIndex, index); setDragIndex(null) }}
                  className={cn('grid grid-cols-[1.25rem_auto_minmax(0,1fr)] items-center gap-2 rounded-lg border p-2 sm:grid-cols-[1.25rem_auto_minmax(0,1fr)_auto]', first ? 'bg-accent' : 'bg-card', dragIndex === index && 'opacity-50')}>
                  <IconDrag aria-hidden className="size-4 cursor-grab text-muted-foreground" />
                  <Badge variant={first ? 'default' : 'secondary'} className="tabular-nums">{index + 1}</Badge>
                  <span tabIndex={0} title={memberKey(member)} className="min-w-0 truncate rounded-sm text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50">{memberOrderLabel(member)}</span>
                  <div className="col-span-3 flex justify-self-end sm:col-span-1">
                    <IconButton label={t('admin:rules.members.move_up')} size="icon-sm" disabled={index === 0} onClick={() => move(index, index - 1)}><IconArrowUp /></IconButton>
                    <IconButton label={t('admin:rules.members.move_down')} size="icon-sm" disabled={index === currentMembers.length - 1} onClick={() => move(index, index + 1)}><IconArrowDown /></IconButton>
                    <IconButton label={`${t('common:actions.delete')} ${memberOrderLabel(member)}`} size="icon-sm" disabled={currentMembers.length <= 1} onClick={() => remove(index)}><IconDelete /></IconButton>
                  </div>
                </li>
              )
            })}
          </ol>

          <div className="mt-4 rounded-lg bg-muted/40 p-3">
            <h4 className="text-[13px] font-semibold">{t('admin:rules.members.add_title')}</h4>
            <p className="mt-0.5 mb-2 text-xs text-muted-foreground">{t(`admin:rules.members.kind_${addKind}_hint`)}</p>
            <div className="flex flex-col gap-2 sm:flex-row">
              <div className="sm:w-44">
                <NativeSelect aria-label={t('admin:rules.members.kind_label')} value={addKind} onChange={e => { setAddKind(e.target.value as AddKind); setAddValue(null); setLineNodeID(null) }}>
                  <option value="node">{t('admin:rules.members.kind_node')}</option>
                  <option value="line">{t('admin:rules.members.kind_line')}</option>
                  <option value="remaining">{t('admin:rules.members.kind_remaining')}</option>
                  <option value="region">{t('admin:rules.members.kind_region')}</option>
                  <option value="tag">{t('admin:rules.members.kind_tag')}</option>
                  <option value="builtin">{t('admin:rules.members.kind_builtin')}</option>
                  <option value="proxy_group">{t('admin:rules.members.kind_group')}</option>
                  <option value="rematch">{t('admin:rules.members.kind_rematch')}</option>
                </NativeSelect>
              </div>
              {addKind === 'line' ? (
                <div className="grid min-w-0 flex-1 grid-cols-1 gap-2 sm:grid-cols-2">
                  <OptionCombobox aria-label={t('admin:rules.members.select_line_node')} placeholder={t('admin:rules.members.select_line_node')} emptyText={noMatch}
                    options={lineNodeOptions} value={lineNodeID}
                    onChange={next => { setLineNodeID(next === null ? null : Number(next)); setAddValue(null) }} />
                  <OptionCombobox key={lineNode?.id ?? 'no-node'} aria-label={t('admin:rules.members.select_line')} placeholder={t('admin:rules.members.select_line')} emptyText={noMatch}
                    options={addOptions} value={selectedAddOption?.value ?? null} disabled={!lineNode}
                    onChange={next => setAddValue(next)} />
                </div>
              ) : (
                <div className="min-w-0 flex-1">
                  <OptionCombobox key={addKind} aria-label={t(`admin:rules.members.kind_${addKind}`)} placeholder={t('admin:rules.members.search_placeholder')} emptyText={noMatch}
                    options={addOptions} value={selectedAddOption?.value ?? null} onChange={next => setAddValue(next)} />
                </div>
              )}
              <Button variant="outline" onClick={addMember}
                disabled={addKind === 'line' ? !lineNode || !selectedAddOption : addKind !== 'remaining' && addValue === null}>
                <IconAdd />{t('admin:rules.members.add')}
              </Button>
            </div>
          </div>

          <div className="mt-3 flex flex-col gap-2">
            {currentIssues.map((issue, index) => <LevelAlert key={`${issue.message}:${index}`} level={issue.level}>{issueLabel(issue)}</LevelAlert>)}
          </div>

          <hr className="my-4" />
          <div className="flex flex-col justify-between gap-2 sm:flex-row sm:items-center">
            <h3 className="text-sm font-semibold">{t('admin:rules.members.preview_title')}</h3>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <span className="text-sm text-muted-foreground">{t('admin:rules.members.preview_group_label')}</span>
              <div className="sm:w-56">
                <NativeSelect aria-label={t('admin:rules.members.preview_group_label')} value={previewGroupID ?? resolvedPreviewGroupID ?? ''} onChange={e => setPreviewGroupID(Number(e.target.value))}>
                  {previewGroupID === undefined && resolvedPreviewGroupID === undefined && <option value="" disabled />}
                  {previewGroups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}
                </NativeSelect>
              </div>
            </div>
          </div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {previewLoading ? <IconSpinner className="animate-spin text-muted-foreground" aria-hidden />
              : previewError ? <LevelAlert level="error" className="w-full">{previewError}</LevelAlert>
              : preview?.status !== 'ready' ? preview && <LevelAlert level="info" className="w-full">{t(`admin:rules.members.preview_${preview.status}`)}</LevelAlert>
              : currentPreview ? <>
                {['url-test', 'load-balance'].includes(currentPreview.type || '') && currentPreview.members.length < 2 && <LevelAlert level="warning" className="w-full">{t('admin:rules.members.issues.insufficient_auto_members')}</LevelAlert>}
                {currentPreview.members.map((name, index) => <Badge key={`${name}:${index}`} variant={index === 0 ? 'default' : 'secondary'}>{`${index + 1}. ${name}`}</Badge>)}
                {!currentPreview.members.length && <span className="text-xs text-muted-foreground">{t('admin:rules.members.preview_empty')}</span>}
              </> : preview && <span className="text-xs text-muted-foreground">{t('admin:rules.members.preview_absent')}</span>}
          </div>
        </>}
      </div>
    </div>
  )
}
