import { useRef, useState } from 'react'
import { Box, Button, CircularProgress, IconButton, Menu, MenuItem, Paper, Stack, Switch, Typography } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import AddIcon from '@mui/icons-material/Add'
import { useTranslation } from 'react-i18next'
import type { DestinationPoliciesView, DestinationPolicyAction, DestinationPolicyInput, DestinationPolicyOverviewItem, DestinationStatus } from '@/api/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useDeleteDestinationPolicy, useOrderDestinationPolicies, useSaveDestinationPolicy } from '@/query/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { pipelineSteps } from '@/utils/accessControl'
import { firstPublishCopy, needsFirstPublishConfirm } from '../confirmCopy'
import PendingActionGuard from '../PendingActionGuard'
import { destinationError } from '../errors'
import { emptyPolicy, policyInput } from './policyDraft'
import { summaryText } from './summaryText'
import QuotaMeters from './QuotaMeters'
import PolicyEditorDialog from './PolicyEditorDialog'
const P = 'admin:access_control.policies.'
interface Props { data: DestinationPoliciesView; status?: DestinationStatus; seconds?: number }
export default function PoliciesTab({ data, status, seconds }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const save = useSaveDestinationPolicy(scope)
  const remove = useDeleteDestinationPolicy(scope)
  const order = useOrderDestinationPolicies(scope)
  const [editor, setEditor] = useState<{ initial: DestinationPolicyInput; existing?: DestinationPolicyOverviewItem } | null>(null)
  const [menu, setMenu] = useState<{ anchor: HTMLElement; row: DestinationPolicyOverviewItem } | null>(null)
  const [busy, setBusy] = useState<number | null>(null)
  const admission = useRef(false)
  const reportError = (error: unknown) => {
    const details = destinationError(error)
    pushSnack(t(`${P}${details.error === 'dest_policy_stale' ? 'stale' : details.error === 'dest_policy_order_stale' ? 'order_stale' : 'failed'}`, { error: details.error }), 'error')
  }
  const run = async (id: number, action: () => Promise<void>) => {
    if (admission.current) return
    admission.current = true; setBusy(id)
    try { await action() } catch (error) { reportError(error) } finally { admission.current = false; setBusy(null) }
  }
  const toggle = (row: DestinationPolicyOverviewItem) => void run(row.id, async () => {
    const input = { ...policyInput(row), enabled: !row.enabled }
    if (needsFirstPublishConfirm(data, [], input) && !(await confirm(firstPublishCopy(t, status, seconds)))) return
    await save.mutateAsync({ input, existing: row })
    pushSnack(t(`${P}saved`, { name: row.name }), 'success')
  })
  const deleting = (row: DestinationPolicyOverviewItem) => void run(row.id, async () => {
    if (!(await confirm({ title: t(`${P}delete_title`, { name: row.name }), message: t(`${P}delete_message`), confirmText: t('common:actions.delete'), destructive: true }))) return
    await remove.mutateAsync(row.id); pushSnack(t(`${P}deleted`, { name: row.name }), 'success')
  })
  const move = (row: DestinationPolicyOverviewItem, target: number) => void run(row.id, async () => {
    const ids = data[row.action].map(p => p.id)
    const index = ids.indexOf(row.id); ids.splice(index, 1); ids.splice(target, 0, row.id)
    await order.mutateAsync({ action: row.action, ids }); pushSnack(t(`${P}reordered`), 'success')
  })
  const create = (action: DestinationPolicyAction) => setEditor({ initial: { ...emptyPolicy(), action } })
  const any = data.allow.length + data.block.length + data.observe.length > 0
  const templates = [
    { key: 'mail', inline: { ports: '25,465,587', network: 'tcp' as const }, risk: true },
    { key: 'bt', inline: { protocols: ['bittorrent'] }, risk: true },
    { key: 'private', inline: { private: true }, risk: false },
  ]
  return <Stack spacing={2.5}>
    {busy !== null && <PendingActionGuard />}
    <QuotaMeters compact budget={data.budget} />
    <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', gap: 1 }}><Typography variant="body2" color="text.secondary">{t(`${P}order_hint`)}</Typography><Button aria-label={t(`${P}create`)} sx={{ flexShrink: 0 }} disabled={busy !== null} onClick={() => create('block')}><AddIcon /><Box component="span" sx={{ display: { xs: 'none', sm: 'inline' }, ml: .5 }}>{t(`${P}create`)}</Box></Button></Stack>
    {!any && <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(3, 1fr)' }, gap: 2 }}>{templates.map(template => <Paper variant="outlined" key={template.key} sx={{ p: 2 }}><Typography variant="subtitle1">{t(`${P}template_${template.key}`)}</Typography><Typography variant="body2" sx={{ my: 1 }}>{t(`${P}template_${template.key}_hint`)}</Typography><Button disabled={busy !== null} onClick={() => setEditor({ initial: { ...emptyPolicy(), name: t(`${P}template_${template.key}`), inline: template.inline, enabled: true, counts_as_risk: template.risk, template_key: template.key } })}>{t(`${P}use_template`)}</Button></Paper>)}</Box>}
    <Box sx={{ borderLeft: theme => `2px solid ${theme.palette.md.outlineVariant}`, ml: { xs: 1, sm: 1.5 }, pl: { xs: 2, sm: 3 } }}>
      {pipelineSteps(false).map((step, index) => <Box key={step} sx={{ position: 'relative', mb: 3 }}>
        <Box aria-hidden sx={{ position: 'absolute', left: { xs: -27, sm: -37 }, top: 5, width: 24, height: 24, borderRadius: '50%', bgcolor: theme => theme.palette.md.surfaceContainerHighest, display: 'grid', placeItems: 'center', fontSize: 13, fontWeight: 600 }}>{index + 1}</Box>
        <Stack direction="row" spacing={1} sx={{ mb: 1, alignItems: 'center', justifyContent: 'space-between' }}><Typography component="h2" variant="subtitle1">{t(`${P}${step}`, { count: data.exemptions.count })}</Typography>
          {['allow', 'block', 'observe'].includes(step) && <IconButton disabled={busy !== null} aria-label={t(`${P}create_${step}`)} onClick={() => create(step as DestinationPolicyAction)}><AddIcon /></IconButton>}</Stack>
        {['allow', 'block', 'observe'].includes(step) && <Stack spacing={1}>{data[step as DestinationPolicyAction].length ? data[step as DestinationPolicyAction].map(row => <Paper key={row.id} variant="outlined" sx={{ p: 1.5, opacity: row.enabled ? 1 : .6 }}>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><Button sx={{ justifyContent: 'flex-start', minWidth: 0, textAlign: 'left', flex: 1, overflowWrap: 'anywhere' }} aria-label={t(`${P}edit`, { name: row.name })} disabled={busy !== null} onClick={() => setEditor({ initial: policyInput(row), existing: row })}>{row.name}</Button>
            {busy === row.id && <CircularProgress size={18} />}
            <Switch checked={row.enabled} disabled={busy !== null} slotProps={{ input: { 'aria-label': t(`${P}toggle`, { name: row.name }) } }} onChange={() => toggle(row)} />
            <IconButton disabled={busy !== null} aria-label={t(`${P}menu`, { name: row.name })} onClick={e => setMenu({ anchor: e.currentTarget, row })}><MoreVertIcon /></IconButton></Stack>
          <Typography variant="body2" color="text.secondary">{summaryText(t, row, new Map(row.list_states.map(list => [list.id, list.name])))} · {row.scope === 'all' ? t('admin:access_control.editor.all') : t(`${P}group_count`, { count: row.group_ids.length })}</Typography>
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>{!row.enabled && <Typography variant="caption">{t(`${P}disabled`)}</Typography>}{row.counts_as_risk && <Typography variant="caption">{t('admin:access_control.editor.counts_as_risk')}</Typography>}{row.scope_missing && <Typography variant="caption" color="error">{t(`${P}scope_missing`)}</Typography>}{row.list_states.some(list => list.state !== 'ready') && <Typography variant="caption" color="warning.main">{t(`${P}list_pending`)}</Typography>}</Stack>
        </Paper>) : <Box sx={{ border: theme => `1px dashed ${theme.palette.md.outlineVariant}`, p: 2, borderRadius: 2 }}><Typography variant="body2" color="text.secondary">{t(`${P}empty_step`)}</Typography><Button disabled={busy !== null} onClick={() => create(step as DestinationPolicyAction)}>{t(`${P}create_${step}`)}</Button></Box>}</Stack>}
      </Box>)}
    </Box>
    <Menu anchorEl={menu?.anchor} open={!!menu} onClose={() => setMenu(null)}>{menu && (() => { const row = menu.row; const rows = data[row.action]; const index = rows.findIndex(p => p.id === row.id); return [
      <MenuItem key="edit" onClick={() => { setMenu(null); setEditor({ initial: policyInput(row), existing: row }) }}>{t(`${P}edit`, { name: row.name })}</MenuItem>,
      <MenuItem key="copy" onClick={() => { setMenu(null); setEditor({ initial: { ...policyInput(row), name: t(`${P}copy_name`, { name: row.name }), enabled: false, template_key: row.template_key === 'global-exceptions' ? '' : row.template_key } }) }}>{t(`${P}copy`)}</MenuItem>,
      ...[{ key: 'up', to: index - 1 }, { key: 'down', to: index + 1 }, { key: 'top', to: 0 }, { key: 'bottom', to: rows.length - 1 }].map(item => <MenuItem key={item.key} disabled={item.to < 0 || item.to >= rows.length || item.to === index} onClick={() => { setMenu(null); move(row, item.to) }}>{t(`${P}${item.key}`)}</MenuItem>),
      <MenuItem key="delete" sx={{ color: 'error.main' }} onClick={() => { setMenu(null); deleting(row) }}>{t('common:actions.delete')}</MenuItem>,
    ] })()}</Menu>
    {editor && <PolicyEditorDialog {...editor} policies={data} status={status} seconds={seconds} onClose={() => setEditor(null)} />}
  </Stack>
}
