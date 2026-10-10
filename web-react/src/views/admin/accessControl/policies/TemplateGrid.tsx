import { Alert, Box, Button, Paper, Skeleton, Stack, Typography, useTheme } from '@mui/material'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import type { DestinationBudget, DestinationCategoriesView } from '@/api/accessControl'
import FieldHint from '@/components/FieldHint'
import GeositeDownloadNotice from '../lists/GeositeDownloadNotice'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { policyTemplates, templateCategory, type PolicyTemplate } from './templates'
const P = 'admin:access_control.templates.'
export interface TemplateCatalogProps { catalog?: DestinationCategoriesView; loading: boolean; downloading: boolean; failed: boolean; disabled: boolean; onDownload: () => Promise<void> }
interface Props extends TemplateCatalogProps { budget: DestinationBudget; added: Set<string>; onCreate: (template: PolicyTemplate) => void; onBlank: () => void; onCreateList: () => void }
export default function TemplateGrid(props: Props) {
  const { t } = useAccessTranslation(['admin', 'common']), theme = useTheme()
  const betting = props.catalog?.categories?.find(category => category.name === 'category-betting-ru')
  return <Stack spacing={2} sx={{ '& button': { minWidth: 44, minHeight: 44 } }}>
    <Box><Typography variant="h6">{t(`${P}empty_title`)}</Typography><Typography variant="body2" color="text.secondary">{t(`${P}empty_hint`)}</Typography></Box>
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)', md: 'repeat(3, 1fr)' }, gap: 2 }}>
      {policyTemplates.map(template => {
        const title = t(`admin:access_control.policies.template_${template.key}`), category = templateCategory(template, props.catalog), needsCategory = 'category' in template
        const added = props.added.has(template.key), over = category && category.regexp_count + props.budget.regexps.used > props.budget.regexps.limit
        return <Paper key={template.key} variant="outlined" sx={{ p: 2, minWidth: 0 }}><Stack spacing={1.5}>
          <Typography component="h3" variant="subtitle1" sx={{ overflowWrap: 'anywhere' }}>{title}</Typography>
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: .5 }}><ToneBadge tone={stateTone(theme, 'quiet')} label={t(`admin:access_control.editor.action_${template.action}`)} />{template.risk && <Typography variant="caption">{t('admin:access_control.editor.counts_as_risk')}</Typography>}{added && <ToneBadge tone={stateTone(theme, 'quiet')} label={t(`${P}added`)} />}</Stack>
          <Typography variant="body2" color="text.secondary">{t(`admin:access_control.policies.template_${template.key}_hint`)}</Typography>
          {category && <Typography variant="caption">{t(`${P}count`, { count: category.count, regexps: category.regexp_count })}</Typography>}
          {over && <FieldHint tone="amber" summary={t(`${P}over_summary`)} detail={t(`${P}over_detail`, { count: category.regexp_count, limit: props.budget.regexps.limit })} />}
          {needsCategory && !category ? props.loading ? <Skeleton height={40} /> : <GeositeDownloadNotice message={props.catalog ? t(`${P}category_missing`) : undefined} pending={props.downloading} disabled={props.disabled} onDownload={props.onDownload} /> : <Button aria-label={t(`${P}use`, { name: title })} disabled={props.disabled} onClick={() => props.onCreate(template)}>{t(added ? `${P}again` : 'admin:access_control.policies.use_template')}</Button>}
        </Stack></Paper>
      })}
      <Paper variant="outlined" sx={{ p: 2 }}><Stack spacing={1.5}><Typography component="h3" variant="subtitle1">{t(`${P}finance_title`)}</Typography><Typography variant="body2" color="text.secondary">{t(`${P}finance_hint`)}</Typography>{betting && <Typography variant="caption">{t(`${P}betting_hint`, { count: betting.count })}</Typography>}<Button disabled={props.disabled} onClick={props.onCreateList}>{t(`${P}create_list`)}</Button></Stack></Paper>
    </Box>
    {props.failed && <Alert severity="error">{t('admin:access_control.categories.failed')}</Alert>}
    <Button sx={{ alignSelf: 'flex-start' }} disabled={props.disabled} onClick={props.onBlank}>{t(`${P}blank`)}</Button>
  </Stack>
}
