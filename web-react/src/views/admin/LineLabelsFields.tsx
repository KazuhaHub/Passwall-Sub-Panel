import { Box, TextField } from '@mui/material'
import { useTranslation } from 'react-i18next'
import TagsAutocomplete from './TagsAutocomplete'

/**
 * A relay or VLESS route line's own Region and Tags (ADR 0040). They only
 * label the line in subscriptions (flag, region:/tag: sets, placeholders) and
 * never decide which users get the node.
 */
export default function LineLabelsFields({ region, tags, tagOptions, disabled, onChange }: {
  region: string
  tags: string[]
  tagOptions: string[]
  disabled?: boolean
  onChange: (patch: { region?: string; tags?: string[] }) => void
}) {
  const { t } = useTranslation('admin')
  // A single row of tags matches the Region field's height; wrapped rows grow.
  return <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: '120px minmax(0, 1fr)' }, gap: 1, alignItems: 'start' }}>
    <TextField size="small" label={t('nodes.line_labels.region')} value={region} disabled={disabled}
      slotProps={{ htmlInput: { maxLength: 64 } }}
      onChange={event => onChange({ region: event.target.value })} />
    {disabled
      ? <TextField size="small" label={t('nodes.line_labels.tags')} value={tags.join(', ')} disabled />
      : <TagsAutocomplete dense label={t('nodes.line_labels.tags')} value={tags.join(', ')} options={tagOptions}
        onChange={next => onChange({ tags: next ? next.split(',').map(tag => tag.trim()).filter(Boolean) : [] })} />}
  </Box>
}
