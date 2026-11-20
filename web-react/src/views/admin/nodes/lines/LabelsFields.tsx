import { Field } from '@/components/form/Field'
import { TagsField } from '@/components/form/TagsField'
import { Input } from '@/components/ui/input'
import type { PreviewHint } from '@/hooks/useDraftPreview'
import { useNodesT } from '../shared/useNodesT'
import { ID, PreviewNote, type FieldsProps } from './fieldKit'

/**
 * A line's own Region and Tags (ADR 0040). They label the line in subscriptions (flag, region and tag sets,
 * placeholders) and decide which groups receive it (ADR 0041); the groups that would, with the unsaved values, are
 * the caption under the fields.
 */
export function LabelsFields({ draft, patch, error, blur, tagOptions, visibility }: FieldsProps & { tagOptions: readonly string[]; visibility?: PreviewHint }) {
  const t = useNodesT()
  return (
    <div className="flex flex-col gap-2">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-[8rem_minmax(0,1fr)]">
        <Field label={t('nodes.line_labels.region')} error={error('region') && t(`nodes.${error('region')}`)}>
          <Input id={ID.region} value={draft.region ?? ''} maxLength={64} onChange={e => patch({ region: e.target.value })} onBlur={() => blur('region')} />
        </Field>
        <Field label={t('nodes.line_labels.tags')}>
          {p => <TagsField id={p.id} aria-labelledby={p.labelId} value={draft.tags ?? []} suggestions={tagOptions} onChange={tags => patch({ tags })} />}
        </Field>
      </div>
      <PreviewNote hint={visibility} testId="line-visibility" none={t('nodes.line_labels.visible_none')} unknown={t('nodes.line_labels.visibility_unknown')}
        ready={groups => t('nodes.line_labels.visible_groups', { groups: groups.join(t('nodes.line_labels.group_separator')) })} />
    </div>
  )
}
