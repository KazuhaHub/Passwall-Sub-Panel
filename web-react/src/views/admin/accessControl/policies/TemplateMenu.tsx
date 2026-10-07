import { useState } from 'react'
import { Alert, Button, Divider, Menu, MenuItem, Stack, Typography, useTheme } from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { AsyncButton } from '@/components/AsyncButton'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { policyTemplates, templateCategory, type PolicyTemplate } from './templates'
import type { TemplateCatalogProps } from './TemplateGrid'
const P = 'admin:access_control.templates.'
export default function TemplateMenu(props: TemplateCatalogProps & { added: Set<string>; onOpen: (open: boolean) => void; onCreate: (template: PolicyTemplate) => void; onBlank: () => void; onFinance: () => void }) {
  const { t } = useAccessTranslation('admin'), theme = useTheme(), [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const missingCategory = policyTemplates.some(template => 'category' in template && !templateCategory(template, props.catalog))
  const close = () => { setAnchor(null); props.onOpen(false) }
  return <>
    <Button aria-label={t('admin:access_control.policies.create')} disabled={props.disabled} startIcon={<AddIcon />} endIcon={<ArrowDropDownIcon />} onClick={e => { setAnchor(e.currentTarget); props.onOpen(true) }}>{t('admin:access_control.policies.create')}</Button>
    <Menu anchorEl={anchor} open={!!anchor} onClose={close}>
      <MenuItem onClick={() => { close(); props.onBlank() }}>{t(`${P}blank`)}</MenuItem><Divider />
      {policyTemplates.map(template => <MenuItem key={template.key} aria-label={t(`admin:access_control.policies.template_${template.key}`)} disabled={'category' in template && !templateCategory(template, props.catalog)} onClick={() => { close(); props.onCreate(template) }}><Stack><Typography variant="body2">{t(`admin:access_control.policies.template_${template.key}`)}</Typography><Typography variant="caption" color="text.secondary">{t(`admin:access_control.editor.action_${template.action}`)}</Typography>{props.added.has(template.key) && <ToneBadge tone={stateTone(theme, 'quiet')} label={t(`${P}added`)} />}</Stack></MenuItem>)}
      {missingCategory && !props.loading && <MenuItem disableRipple onClick={() => void props.onDownload()}><Stack spacing={1}><Typography variant="caption">{t(props.catalog ? `${P}category_missing` : 'admin:access_control.categories.missing')}</Typography>{props.failed && <Alert severity="error">{t('admin:access_control.categories.failed')}</Alert>}<AsyncButton pending={props.downloading} disabled={props.disabled} onClick={props.onDownload}>{t('admin:access_control.categories.download')}</AsyncButton></Stack></MenuItem>}
      <Divider /><MenuItem onClick={() => { close(); props.onFinance() }}>{t(`${P}finance_question`)}</MenuItem>
    </Menu>
  </>
}
