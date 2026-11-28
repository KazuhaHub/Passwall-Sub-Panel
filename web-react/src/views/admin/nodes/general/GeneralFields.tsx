import { Field } from '@/components/form/Field'
import { TagsField } from '@/components/form/TagsField'
import { Input } from '@/components/ui/input'
import { SuggestInput } from '../../settings/parts'
import type { PreviewHint } from '@/hooks/useDraftPreview'
import { validateHost, validateName } from '@/utils/validators'
import { useNodesT } from '../shared/useNodesT'
import { PreviewNote } from '../lines/fieldKit'

/** What the General tab and the create page's basics edit of a node. Tags are a list here, a comma text in the inbound form's state. */
export interface GeneralDraft {
  display_name: string
  server_address: string
  region: string
  tags: string[]
}

export type GeneralField = 'display_name' | 'server_address' | 'region'

export const GENERAL_ID: Record<GeneralField, string> = { display_name: 'node-name', server_address: 'node-address', region: 'node-region' }

/** The same rules the legacy editor and the wizard applied: a name up to 64, a host, a region up to 32. A key of the admin namespace, or '' when fine. */
export function generalProblems(d: GeneralDraft): Partial<Record<GeneralField, string>> {
  const out: Partial<Record<GeneralField, string>> = {}
  const name = validateName(d.display_name, { required: true, max: 64 }); if (name) out.display_name = name
  const host = validateHost(d.server_address, { required: true }); if (host) out.server_address = host
  const region = validateName(d.region, { required: true, max: 32 }); if (region) out.region = region
  return out
}

/** The regions nodes already use, most used first (ties alphabetical); blank values are skipped. */
export function regionOptionsOf(nodes: readonly { region?: string }[]): string[] {
  const count = new Map<string, number>()
  for (const n of nodes) { const r = n.region?.trim(); if (r) count.set(r, (count.get(r) ?? 0) + 1) }
  return [...count].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([r]) => r)
}

/**
 * Name, address, region and tags, with the groups that would receive the node (ADR 0041) under the last two.
 * Controlled by the caller; it knows nothing about saving, so the General tab and the create page share it.
 */
export function GeneralFields({ draft, patch, error, blur, tagOptions, regionOptions = [], visible, removed }: {
  draft: GeneralDraft
  patch: (p: Partial<GeneralDraft>) => void
  error: (f: GeneralField) => string | undefined
  blur: (f: GeneralField) => void
  tagOptions: readonly string[]
  /** Regions in use, offered as suggestions; any other text is still accepted. */
  regionOptions?: readonly string[]
  /** The groups that would see the node with these values; absent on the create page (nothing is saved yet to compare with). */
  visible?: PreviewHint
  removed?: string[]
}) {
  const t = useNodesT()
  const set = patch
  return (
    <div className="flex flex-col gap-4">
      <Field label={t('nodes.field.display_name')} required hint={t('nodes.field.display_name_factor_hint', { placeholder: '{{ factor }}' })} error={error('display_name') && t(error('display_name')!)}>
        <Input id={GENERAL_ID.display_name} value={draft.display_name} maxLength={128} onChange={e => set({ display_name: e.target.value })} onBlur={() => blur('display_name')} />
      </Field>
      <Field label={t('nodes.field.server_address')} required error={error('server_address') && t(error('server_address')!)}>
        <Input id={GENERAL_ID.server_address} value={draft.server_address} onChange={e => set({ server_address: e.target.value })} onBlur={() => blur('server_address')} />
      </Field>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-[10rem_minmax(0,1fr)]">
        <Field label={t('nodes.field.region')} required error={error('region') && t(error('region')!)}>
          <SuggestInput id={GENERAL_ID.region} value={draft.region} options={regionOptions} placeholder={t('nodes.field.region_search_placeholder')}
            onChange={region => set({ region })} onBlur={() => blur('region')} />
        </Field>
        <Field label={t('nodes.field.tags')}>
          {p => <TagsField id={p.id} aria-labelledby={p.labelId} value={draft.tags} suggestions={tagOptions} onChange={tags => set({ tags })} />}
        </Field>
      </div>
      {(visible || (removed?.length ?? 0) > 0) && (
        <div className="flex flex-col gap-1">
          <PreviewNote hint={visible} testId="node-groups" none={t('nodes.node_groups.none')} unknown={t('nodes.line_labels.visibility_unknown')}
            ready={groups => t('nodes.node_groups.visible', { groups: groups.join(t('nodes.line_labels.group_separator')) })} />
          {(removed?.length ?? 0) > 0 && (
            <p data-testid="node-groups-removed" className="text-xs text-destructive">{t('nodes.node_groups.removed', { groups: removed!.join(t('nodes.line_labels.group_separator')) })}</p>
          )}
        </div>
      )}
    </div>
  )
}
