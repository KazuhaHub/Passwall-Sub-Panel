import { useEffect, useRef, useState } from 'react'
import { Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Menu, MenuItem, Paper, Stack, Switch, Typography } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import AddIcon from '@mui/icons-material/Add'
import { useTranslation } from 'react-i18next'
import type { DestinationPoliciesView, DestinationPolicyAction, DestinationPolicyInput, DestinationPolicyOverviewItem, DestinationStatus } from '@/api/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useDeleteDestinationPolicy, useDestinationCategories, useOrderDestinationPolicies, useRefreshDestinationCategories, useSaveDestinationPolicy } from '@/query/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { pipelineSteps } from '@/utils/accessControl'
import { firstPublishCopy, needsFirstPublishConfirm } from '../confirmCopy'
import PendingActionGuard from '../PendingActionGuard'
import { destinationError } from '../errors'
import { categoryRefreshState } from '@/utils/destinationCategories'
import { policyListAvailable } from '@/utils/destinationListAvailability'
import { emptyPolicy, policyInput } from './policyDraft'
import { summaryText } from './summaryText'
import QuotaMeters from './QuotaMeters'
import PolicyEditorDialog from './PolicyEditorDialog'
import ConvertToBlockDialog from './ConvertToBlockDialog'
import TemplateGrid from './TemplateGrid'
import TemplateMenu from './TemplateMenu'
import { templatePolicy, type PolicyTemplate } from './templates'
import { PipelineRail, PipelineStep } from '../PipelineRail'
const P = 'admin:access_control.policies.'
interface Props { data: DestinationPoliciesView; status?: DestinationStatus; seconds?: number; onCreateList: () => void; onExemptions: () => void; openRequest?: { id: number; token: number } | null; onOpenRequestHandled?: (token: number) => void }
export default function PoliciesTab({ data, status, seconds, onCreateList, onExemptions, openRequest, onOpenRequestHandled }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const save = useSaveDestinationPolicy(scope)
  const remove = useDeleteDestinationPolicy(scope)
  const order = useOrderDestinationPolicies(scope)
  const [editor, setEditor] = useState<{ initial: DestinationPolicyInput; existing?: DestinationPolicyOverviewItem; templateName?: string } | null>(null)
  const [menu, setMenu] = useState<{ anchor: HTMLElement; row: DestinationPolicyOverviewItem } | null>(null)
  const [promotion, setPromotion] = useState<DestinationPolicyOverviewItem | null>(null)
  const [templateMenuOpen, setTemplateMenuOpen] = useState(false), [finance, setFinance] = useState(false)
  const [busy, setBusy] = useState<number | null>(null)
  const admission = useRef(false)
  const openedRequest = useRef<number | null>(null), [highlight, setHighlight] = useState<number | null>(null)
  const rowsRef = useRef(new Map<number, HTMLDivElement>())
  useEffect(() => {
    if (!openRequest || openedRequest.current === openRequest.token) return
    const row = [...data.allow, ...data.block, ...data.observe].find(row => row.id === openRequest.id)
    if (!row) { openedRequest.current = openRequest.token; onOpenRequestHandled?.(openRequest.token); pushSnack(t('admin:access_control.test.policy_missing'), 'warning'); return }
    openedRequest.current = openRequest.token
    onOpenRequestHandled?.(openRequest.token)
    rowsRef.current.get(row.id)?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
    setHighlight(row.id); setEditor({ initial: policyInput(row), existing: row })
  }, [openRequest, data, t, onOpenRequestHandled])
  useEffect(() => { if (highlight === null) return; const timer = window.setTimeout(() => setHighlight(null), 2000); return () => window.clearTimeout(timer) }, [highlight])
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
  const categories = useDestinationCategories(scope, !any || templateMenuOpen), categoryRefresh = useRefreshDestinationCategories(scope), categoryAdmission = useRef(false)
  const download = async () => { if (categoryAdmission.current) return; categoryAdmission.current = true; try { await categoryRefresh.mutateAsync() } catch { /* Render the failed download below. */ } finally { categoryAdmission.current = false } }
  const catalogState = categoryRefreshState(categories.data, categories.error)
  const catalog = { catalog: categories.data, loading: categories.isPending, downloading: categoryRefresh.isPending || catalogState.refreshing, failed: !catalogState.refreshing && (!!categoryRefresh.error || catalogState.failed), disabled: busy !== null, onDownload: download }
  const fromTemplate = (template: PolicyTemplate) => { const name = t(`${P}template_${template.key}`); setEditor({ initial: templatePolicy(template, name, t('admin:access_control.templates.list_name', { name })), templateName: name }) }
  const added = new Set([...data.allow, ...data.block, ...data.observe].map(row => row.template_key))
  return <Stack spacing={2.5}>
    {busy !== null && <PendingActionGuard />}
    <QuotaMeters compact budget={data.budget} />
    <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', gap: 1 }}><Typography variant="body2" color="text.secondary">{any ? t(`${P}order_hint`) : ''}</Typography><TemplateMenu {...catalog} added={added} onOpen={setTemplateMenuOpen} onCreate={fromTemplate} onBlank={() => create('block')} onFinance={() => setFinance(true)} /></Stack>
    {!any && <TemplateGrid {...catalog} budget={data.budget} added={added} onCreate={fromTemplate} onBlank={() => create('block')} onCreateList={onCreateList} />}
    {!any && <Button sx={{ alignSelf: 'flex-start' }} onClick={onExemptions}>{t('admin:access_control.exemptions.manage')}</Button>}
    {any && <PipelineRail>
      {pipelineSteps(false).map((step, index) => <PipelineStep key={step} index={index}>
        <Stack direction="row" spacing={1} sx={{ mb: 1, alignItems: 'center', justifyContent: 'space-between' }}><Typography component="h2" variant="subtitle1">{t(`${P}${step}`, { count: data.exemptions.count })}</Typography>
          {step === 'exemption' && <Button disabled={busy !== null} onClick={onExemptions}>{t('admin:access_control.exemptions.manage')}</Button>}{['allow', 'block', 'observe'].includes(step) && <IconButton disabled={busy !== null} aria-label={t(`${P}create_${step}`)} onClick={() => create(step as DestinationPolicyAction)}><AddIcon /></IconButton>}</Stack>
        {['allow', 'block', 'observe'].includes(step) && <Stack spacing={1}>{data[step as DestinationPolicyAction].length ? data[step as DestinationPolicyAction].map(row => <Paper key={row.id} ref={node => { if (node) rowsRef.current.set(row.id, node); else rowsRef.current.delete(row.id) }} data-highlighted={highlight === row.id ? true : undefined} variant="outlined" sx={{ p: 1.5, opacity: row.enabled ? 1 : .6, bgcolor: highlight === row.id ? theme => theme.palette.md.secondaryContainer : undefined }}>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><Button sx={{ justifyContent: 'flex-start', minWidth: 0, textAlign: 'left', flex: 1, overflowWrap: 'anywhere' }} aria-label={t(`${P}edit`, { name: row.name })} disabled={busy !== null} onClick={() => setEditor({ initial: policyInput(row), existing: row })}>{row.name}</Button>
            {busy === row.id && <CircularProgress size={18} />}
            <Switch checked={row.enabled} disabled={busy !== null} slotProps={{ input: { 'aria-label': t(`${P}toggle`, { name: row.name }) } }} onChange={() => toggle(row)} />
            <IconButton disabled={busy !== null} aria-label={t(`${P}menu`, { name: row.name })} onClick={e => setMenu({ anchor: e.currentTarget, row })}><MoreVertIcon /></IconButton></Stack>
          <Typography variant="body2" color="text.secondary">{summaryText(t, row, new Map(row.list_states.map(list => [list.id, list.name])))} · {row.scope === 'all' ? t('admin:access_control.editor.all') : t(`${P}group_count`, { count: row.group_ids.length })}</Typography>
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>{!row.enabled && <Typography variant="caption">{t(`${P}disabled`)}</Typography>}{row.counts_as_risk && <Typography variant="caption">{t('admin:access_control.editor.counts_as_risk')}</Typography>}{row.scope_missing && <Typography variant="caption" color="error">{t(`${P}scope_missing`)}</Typography>}{row.list_states.some(list => !policyListAvailable(list)) && <Typography variant="caption" color="warning.main">{t(`${P}list_pending`)}</Typography>}</Stack>
        </Paper>) : <Box sx={{ border: theme => `1px dashed ${theme.palette.md.outlineVariant}`, p: 2, borderRadius: 2 }}><Typography variant="body2" color="text.secondary">{t(`${P}empty_step`)}</Typography><Button disabled={busy !== null} onClick={() => create(step as DestinationPolicyAction)}>{t(`${P}create_${step}`)}</Button></Box>}</Stack>}
      </PipelineStep>)}
    </PipelineRail>}
    <Menu anchorEl={menu?.anchor} open={!!menu} onClose={() => setMenu(null)}>{menu && (() => { const row = menu.row; const rows = data[row.action]; const index = rows.findIndex(p => p.id === row.id); return [
      <MenuItem key="edit" onClick={() => { setMenu(null); setEditor({ initial: policyInput(row), existing: row }) }}>{t(`${P}edit`, { name: row.name })}</MenuItem>,
      <MenuItem key="copy" onClick={() => { setMenu(null); setEditor({ initial: { ...policyInput(row), name: t(`${P}copy_name`, { name: row.name }), enabled: false, template_key: row.template_key === 'global-exceptions' ? '' : row.template_key } }) }}>{t(`${P}copy`)}</MenuItem>,
      ...(row.action === 'observe' ? [<MenuItem key="promote" onClick={() => { setMenu(null); setPromotion(row) }}>{t('admin:access_control.promotion.action')}</MenuItem>] : []),
      ...[{ key: 'up', to: index - 1 }, { key: 'down', to: index + 1 }, { key: 'top', to: 0 }, { key: 'bottom', to: rows.length - 1 }].map(item => <MenuItem key={item.key} disabled={item.to < 0 || item.to >= rows.length || item.to === index} onClick={() => { setMenu(null); move(row, item.to) }}>{t(`${P}${item.key}`)}</MenuItem>),
      <MenuItem key="delete" sx={{ color: 'error.main' }} onClick={() => { setMenu(null); deleting(row) }}>{t('common:actions.delete')}</MenuItem>,
    ] })()}</Menu>
    {editor && <PolicyEditorDialog {...editor} policies={data} status={status} seconds={seconds} onClose={() => setEditor(null)} />}
    {promotion && <ConvertToBlockDialog policy={promotion} onClose={() => setPromotion(null)} />}
    <Dialog open={finance} maxWidth="sm" fullWidth onClose={() => setFinance(false)}><DialogTitle>{t('admin:access_control.templates.finance_question')}</DialogTitle><DialogContent><Typography>{t('admin:access_control.templates.finance_hint')}</Typography>{categories.data?.categories?.some(category => category.name === 'category-betting-ru') && <Typography variant="body2" sx={{ mt: 2 }}>{t('admin:access_control.templates.betting_hint', { count: categories.data.categories.find(category => category.name === 'category-betting-ru')!.count })}</Typography>}</DialogContent><DialogActions><Button onClick={() => setFinance(false)}>{t('common:actions.close')}</Button><Button onClick={() => { setFinance(false); onCreateList() }}>{t('admin:access_control.templates.create_list')}</Button></DialogActions></Dialog>
  </Stack>
}
