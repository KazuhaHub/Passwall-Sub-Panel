import { Box, TextField } from '@mui/material'
import { useTranslation } from 'react-i18next'
import TagsAutocomplete from './TagsAutocomplete'
import PreviewCaption from '@/components/PreviewCaption'
import type { PreviewHint } from '@/hooks/useDraftPreview'

/**
 * A relay or VLESS route line's own Region and Tags (ADR 0040). They only
 * label the line in subscriptions (flag, region:/tag: sets, placeholders) and
 * never decide which users get the node.
 */
export default function LineLabelsFields({ region, tags, tagOptions, disabled, visibility, onChange }: {
  region: string
  tags: string[]
  tagOptions: string[]
  disabled?: boolean
  /** Which groups would see this line with the current draft (ADR 0041). */
  visibility?: PreviewHint
  onChange: (patch: { region?: string; tags?: string[] }) => void
}) {
  const { t } = useTranslation('admin')
  // A single row of tags matches the Region field's height; wrapped rows grow.
  return <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: '120px minmax(0, 1fr)' }, gap: 1, alignItems: 'start' }}>
      <TextField size="small" label={t('nodes.line_labels.region')} value={region} disabled={disabled}
        slotProps={{ htmlInput: { maxLength: 64 } }}
        onChange={event => onChange({ region: event.target.value })} />
      {disabled
        ? <TextField size="small" label={t('nodes.line_labels.tags')} value={tags.join(', ')} disabled />
        : <TagsAutocomplete dense label={t('nodes.line_labels.tags')} value={tags.join(', ')} options={tagOptions}
          onChange={next => onChange({ tags: next ? next.split(',').map(tag => tag.trim()).filter(Boolean) : [] })} />}
    </Box>
    <PreviewCaption hint={visibility} testId="line-visibility"
      readyText={groups => t('nodes.line_labels.visible_groups', { groups: groups.join(t('nodes.line_labels.group_separator')) })}
      noneText={t('nodes.line_labels.visible_none')} unknownText={t('nodes.line_labels.visibility_unknown')} />
  </Box>
}
