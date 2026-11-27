import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { previewGroupNodes, type GroupNodePreview } from '@/api/groups'
import { listNodes } from '@/api/nodes'
import { Field } from '@/components/form/Field'
import { TagsField } from '@/components/form/TagsField'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { useDraftPreview } from '@/hooks/useDraftPreview'
import { groupKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { NodePreview } from './NodePreview'
import { tagFilterOf, type GroupForm } from './model'
import { useGroupsT } from './useGroupsT'

const sorted = (set: Set<string>) => Array.from(set).sort((a, b) => a.localeCompare(b))

/**
 * Which nodes and lines a group's users get: the tag filter (match everything, or conditions combined with AND /
 * OR) and, under it, the live answer to "which nodes does this draft reach", asked as typing pauses.
 *
 * The conditions are split by prefix into three pickers (region / tag / node) so nobody has to remember the
 * `region:XX` syntax; the pickers still take values that do not exist yet (a region about to be used on a new
 * node), and a fourth box holds conditions the three cannot express.
 */
export function NodesTab({ form, setForm, groupId }: {
  form: GroupForm
  setForm: (next: GroupForm) => void
  /** The saved group the preview diffs against; null while creating. */
  groupId: number | null
}) {
  const t = useGroupsT()
  const scope = useQueryScope()
  const draft = useMemo(() => tagFilterOf(form), [form])
  const preview = useDraftPreview<GroupNodePreview>(JSON.stringify([groupId, draft]),
    signal => previewGroupNodes(groupId, draft, signal))

  // Suggestions from every managed node. A failed read leaves the pickers empty (free typing still works).
  const nodes = useQuery({
    queryKey: [...groupKeys.all(scope), 'node-options'] as const,
    queryFn: () => listNodes({ page: 1, page_size: 500 }),
    staleTime: 60_000,
    retry: false,
  })
  const options = useMemo(() => {
    const regions = new Set<string>(); const tags = new Set<string>(); const servers = new Set<string>()
    for (const n of nodes.data?.items ?? []) {
      if (n.region) regions.add(n.region)
      for (const tg of n.tags ?? []) if (tg) tags.add(tg)
      if (n.server_address) servers.add(n.server_address)
    }
    return { regions: sorted(regions), tags: sorted(tags), servers: sorted(servers) }
  }, [nodes.data])

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">{t('admin:groups.nodes.intro')}</p>
      <Field label={t('admin:groups.field.match_all')} orientation="horizontal">
        <Switch checked={form.all} onCheckedChange={c => setForm({ ...form, all: c })} />
      </Field>
      {!form.all && (
        <>
          <Field label={t('admin:groups.field.mode')} hint={t('admin:groups.hint.mode')}>
            <NativeSelect className="sm:max-w-sm" value={form.mode} onChange={e => setForm({ ...form, mode: e.target.value as 'all' | 'any' })}>
              <option value="all">{t('admin:groups.mode.all')}</option>
              <option value="any">{t('admin:groups.mode.any')}</option>
            </NativeSelect>
          </Field>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <Field label={t('admin:groups.field.region')}>
              {p => <TagsField id={p.id} aria-describedby={p['aria-describedby']} value={form.regions} suggestions={options.regions}
                placeholder={t('admin:groups.placeholder.pick')} onChange={v => setForm({ ...form, regions: v })} />}
            </Field>
            <Field label={t('admin:groups.field.tag')}>
              {p => <TagsField id={p.id} aria-describedby={p['aria-describedby']} value={form.tags} suggestions={options.tags}
                placeholder={t('admin:groups.placeholder.pick')} onChange={v => setForm({ ...form, tags: v })} />}
            </Field>
            <Field label={t('admin:groups.field.node')}>
              {p => <TagsField id={p.id} aria-describedby={p['aria-describedby']} value={form.servers} suggestions={options.servers}
                placeholder={t('admin:groups.placeholder.pick')} onChange={v => setForm({ ...form, servers: v })} />}
            </Field>
          </div>
          <Field label={t('admin:groups.field.custom_conditions')} hint={t('admin:groups.hint.custom_conditions')}>
            <Input placeholder="vendor:gcp, ..." value={form.custom_text} onChange={e => setForm({ ...form, custom_text: e.target.value })} />
          </Field>
        </>
      )}
      <NodePreview state={preview} editing={groupId !== null} />
    </div>
  )
}
