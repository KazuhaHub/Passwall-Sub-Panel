import { Box, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import TagsAutocomplete from './TagsAutocomplete'
import type { LineVisibilityHint } from './useLineVisibility'

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
  visibility?: LineVisibilityHint
  onChange: (patch: { region?: string; tags?: string[] }) => void
}) {
  const { t } = useTranslation('admin')
  const md = useTheme().palette.md
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
    {visibility && visibility.state !== 'loading' && <Typography data-testid="line-visibility" sx={{
      fontSize: 12,
      color: visibility.state === 'ready' && visibility.groups.length === 0 ? md.error : md.onSurfaceVariant,
    }}>
      {visibility.state === 'error'
        ? t('nodes.line_labels.visibility_unknown')
        : visibility.groups.length === 0
          ? t('nodes.line_labels.visible_none')
          : t('nodes.line_labels.visible_groups', { groups: visibility.groups.join(t('nodes.line_labels.group_separator')) })}
    </Typography>}
  </Box>
}
