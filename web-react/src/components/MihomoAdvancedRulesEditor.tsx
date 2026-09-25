import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  List,
  ListItemButton,
  ListItemText,
  MenuItem,
  Stack,
  TextField,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import { useTranslation } from 'react-i18next'

import { inspectProxyGroups, type MihomoRematchOutbound, type MihomoSubRule, type ProxyGroupIssue, type RuleSet } from '@/api/rules'
import RuleEditorDialog from '@/components/RuleEditorDialog'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

interface Props {
  value: RuleSet
  onChange: (next: RuleSet) => void
  onValidationChange?: (hasErrors: boolean) => void
}

export default function MihomoAdvancedRulesEditor({ value, onChange, onValidationChange }: Props) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const subRules = value.mihomo_sub_rules || []
  const rematches = value.mihomo_rematch_outbounds || []
  const [selectedSubRuleIndex, setSelectedSubRuleIndex] = useState(0)
  const [subRulesOpen, setSubRulesOpen] = useState(false)
  const [rematchesOpen, setRematchesOpen] = useState(false)
  const [rematchCloseAttempted, setRematchCloseAttempted] = useState(false)
  const [validatingRematches, setValidatingRematches] = useState(false)
  const [issues, setIssues] = useState<ProxyGroupIssue[]>([])
  const [checking, setChecking] = useState(false)
  const serialized = useMemo(() => JSON.stringify([
    value.content,
    value.proxy_group_members || {},
    value.proxy_group_options || {},
    subRules,
    rematches,
  ]), [value.content, value.proxy_group_members, value.proxy_group_options, subRules, rematches])

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setChecking(true)
      void inspectProxyGroups({
        content: value.content,
        proxy_group_members: value.proxy_group_members || {},
        proxy_group_options: value.proxy_group_options || {},
        mihomo_sub_rules: subRules,
        mihomo_rematch_outbounds: rematches,
      }, controller.signal).then(result => {
        setIssues(result.issues.filter(issue => issue.section && issue.section !== 'proxy_group'))
        onValidationChange?.(result.issues.some(issue => issue.level === 'error'))
      }).catch(error => {
        if (error?.code !== 'ERR_CANCELED') onValidationChange?.(true)
      }).finally(() => {
        if (!controller.signal.aborted) setChecking(false)
      })
    }, 300)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [serialized]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (selectedSubRuleIndex >= subRules.length) setSelectedSubRuleIndex(Math.max(0, subRules.length - 1))
  }, [subRules.length, selectedSubRuleIndex])

  const selectedIndex = selectedSubRuleIndex < subRules.length ? selectedSubRuleIndex : -1
  const selected = selectedIndex >= 0 ? subRules[selectedIndex] : undefined

  function patch(patch: Partial<RuleSet>) {
    onChange({ ...value, ...patch })
  }

  function nextSubRuleName() {
    const used = new Set(subRules.map(rule => rule.name))
    let index = subRules.length + 1
    while (used.has(`sub-rule-${index}`)) index++
    return `sub-rule-${index}`
  }

  function addSubRule() {
    const name = nextSubRuleName()
    patch({ mihomo_sub_rules: [...subRules, { name, content: '- MATCH,DIRECT' }] })
    setSelectedSubRuleIndex(subRules.length)
  }

  function updateSubRule(index: number, next: MihomoSubRule) {
    const copy = subRules.map((rule, i) => i === index ? next : rule)
    patch({ mihomo_sub_rules: copy })
  }

  function removeSubRule(index: number) {
    patch({ mihomo_sub_rules: subRules.filter((_, i) => i !== index) })
    if (index < selectedSubRuleIndex) setSelectedSubRuleIndex(selectedSubRuleIndex - 1)
    else if (index === selectedSubRuleIndex && selectedSubRuleIndex >= subRules.length - 1) setSelectedSubRuleIndex(Math.max(0, selectedSubRuleIndex - 1))
  }

  function addRematch() {
    patch({ mihomo_rematch_outbounds: [...rematches, { name: '', target_rematch_name: '', target_sub_rule: '' }] })
  }

  function updateRematch(index: number, next: MihomoRematchOutbound) {
    patch({ mihomo_rematch_outbounds: rematches.map((outbound, i) => i === index ? next : outbound) })
  }

  function issuesForRematch(index: number, outbound: MihomoRematchOutbound) {
    const name = outbound.name.trim()
    return issues.filter(issue => issue.section === 'rematch_outbound' && (
      issue.params?.index === index || (issue.params?.index == null && name !== '' && issue.name === name)
    ))
  }

  function openRematches() {
    setRematchCloseAttempted(false)
    setRematchesOpen(true)
  }

  async function attemptCloseRematches() {
    if (validatingRematches) return
    setValidatingRematches(true)
    try {
      const result = await inspectProxyGroups({
        content: value.content,
        proxy_group_members: value.proxy_group_members || {},
        proxy_group_options: value.proxy_group_options || {},
        mihomo_sub_rules: subRules,
        mihomo_rematch_outbounds: rematches,
      })
      const nextIssues = result.issues.filter(issue => issue.section && issue.section !== 'proxy_group')
      setIssues(nextIssues)
      const hasErrors = nextIssues.some(issue => issue.section === 'rematch_outbound' && issue.level === 'error')
      onValidationChange?.(result.issues.some(issue => issue.level === 'error'))
      if (hasErrors) {
        setRematchCloseAttempted(true)
        return
      }
      setRematchCloseAttempted(false)
      setRematchesOpen(false)
    } catch {
      onValidationChange?.(true)
    } finally {
      setValidatingRematches(false)
    }
  }

  return (
    <>
      <Button type="button" variant="outlined" onClick={() => setSubRulesOpen(true)}>
        {t('admin:rules.mihomo.sub_rules')}{subRules.length ? ` (${subRules.length})` : ''}
      </Button>
      <Button type="button" variant="outlined" onClick={openRematches}>
        {t('admin:rules.mihomo.rematches')}{rematches.length ? ` (${rematches.length})` : ''}
      </Button>

      <RuleEditorDialog open={subRulesOpen} onClose={() => setSubRulesOpen(false)} paperWidth={900} paperHeight={680}>
        <DialogTitle>{t('admin:rules.mihomo.sub_rules')}</DialogTitle>
        <DialogContent dividers sx={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          <Alert severity="info" sx={{ mb: 1.5 }}>{t('admin:rules.mihomo.sub_rules_compatibility')}</Alert>
          <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <Button size="small" startIcon={<AddIcon />} onClick={addSubRule}>{t('admin:rules.members.add')}</Button>
              <Box sx={{ width: 18, height: 18, display: 'grid', placeItems: 'center' }}>
                {checking && <CircularProgress size={18} />}
              </Box>
            </Stack>
            <Typography variant="body2" color="text.secondary">{t('admin:rules.mihomo.sub_rules_count', { count: subRules.length })}</Typography>
          </Stack>
          <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '240px minmax(0, 1fr)' }, border: `1px solid ${md.outlineVariant}`, borderRadius: 2, overflow: 'hidden', flex: 1, minHeight: 0 }}>
            <List dense disablePadding sx={{ bgcolor: md.surfaceContainerLow, borderRight: { md: `1px solid ${md.outlineVariant}` }, overflowY: 'auto', minHeight: 0 }}>
              {subRules.map((rule, index) => (
                <ListItemButton key={`${rule.name}:${index}`} selected={index === selectedIndex} onClick={() => setSelectedSubRuleIndex(index)}>
                  <ListItemText primary={rule.name || t('admin:rules.mihomo.unnamed_sub_rule')} />
                  <IconButton size="small" color="error" onClick={event => { event.stopPropagation(); removeSubRule(index) }}><DeleteIcon fontSize="small" /></IconButton>
                </ListItemButton>
              ))}
            </List>
            <Box sx={{ p: 1.5, minWidth: 0, overflowY: 'auto' }}>
              {selected ? <>
                <TextField fullWidth size="small" label={t('admin:rules.mihomo.sub_rule_name')} value={selected.name}
                  onChange={event => updateSubRule(selectedIndex, { ...selected, name: event.target.value })} sx={{ mb: 1 }} />
                <Box sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, overflow: 'hidden' }}>
                  <Suspense fallback={<Box sx={{ height: 220, display: 'grid', placeItems: 'center' }}><CircularProgress size={22} /></Box>}>
                    <CodeEditor value={selected.content} onChange={content => updateSubRule(selectedIndex, { ...selected, content })} height="220px" dark={theme.palette.mode === 'dark'} />
                  </Suspense>
                </Box>
              </> : <Box sx={{ py: 10, textAlign: 'center', color: md.onSurfaceVariant }}>{t('admin:rules.mihomo.no_sub_rules')}</Box>}
            </Box>
          </Box>
          {issues.filter(issue => issue.section === 'sub_rule' || issue.section === 'rules').map((issue, index) => (
            <Alert key={`${issue.code}:${issue.name}:${index}`} severity={issue.level} sx={{ mt: 1.5 }}>
              {t(`admin:rules.mihomo.issues.${issue.code}`, { ...issue.params, name: issue.name, defaultValue: issue.message })}
            </Alert>
          ))}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setSubRulesOpen(false)}>{t('common:actions.close')}</Button>
        </DialogActions>
      </RuleEditorDialog>

      <RuleEditorDialog open={rematchesOpen} onClose={() => void attemptCloseRematches()} paperWidth={900} paperHeight={680}>
        <DialogTitle>{t('admin:rules.mihomo.rematches')}</DialogTitle>
        <DialogContent dividers sx={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          <Alert severity="warning" sx={{ mb: 1.5 }}>{t('admin:rules.mihomo.rematch_compatibility')}</Alert>
          <Stack direction="row" sx={{ justifyContent: 'flex-end', mb: 1 }}>
            <Button size="small" startIcon={<AddIcon />} onClick={addRematch}>{t('admin:rules.members.add')}</Button>
          </Stack>
          <Stack spacing={1} sx={{ flex: 1, minHeight: 0, overflowY: 'auto', pr: 0.5 }}>
            {rematches.map((outbound, index) => {
              const rowIssues = issuesForRematch(index, outbound)
              return (
                <Stack key={index} spacing={1}>
                  <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr 1fr auto' }, gap: 1, p: 1.5, border: `1px solid ${md.outlineVariant}`, borderRadius: 2 }}>
                    <TextField size="small" label={t('admin:rules.mihomo.outbound_name')} value={outbound.name}
                      onChange={event => updateRematch(index, { ...outbound, name: event.target.value })} />
                    <TextField size="small" label={t('admin:rules.mihomo.target_rematch_name')} value={outbound.target_rematch_name || ''}
                      onChange={event => updateRematch(index, { ...outbound, target_rematch_name: event.target.value })} />
                    <TextField select size="small" label={t('admin:rules.mihomo.target_sub_rule')} value={outbound.target_sub_rule || ''}
                      onChange={event => updateRematch(index, { ...outbound, target_sub_rule: event.target.value })}
                      slotProps={{ inputLabel: { shrink: true } }}>
                      <MenuItem value="">—</MenuItem>
                      {subRules.filter(rule => rule.name.trim()).map(rule => <MenuItem key={rule.name} value={rule.name}>{rule.name}</MenuItem>)}
                    </TextField>
                    <IconButton color="error" onClick={() => patch({ mihomo_rematch_outbounds: rematches.filter((_, i) => i !== index) })}><DeleteIcon /></IconButton>
                  </Box>
                  {rematchCloseAttempted && rowIssues.map((issue, issueIndex) => (
                    <Alert key={`${issue.code}:${issue.name}:${issueIndex}`} severity={issue.level}>
                      {t(`admin:rules.mihomo.issues.${issue.code}`, { ...issue.params, name: issue.name, defaultValue: issue.message })}
                    </Alert>
                  ))}
                </Stack>
              )
            })}
            {!rematches.length && <Typography variant="body2" color="text.secondary">{t('admin:rules.mihomo.no_rematches')}</Typography>}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => void attemptCloseRematches()} disabled={validatingRematches}
            startIcon={validatingRematches ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.close')}
          </Button>
        </DialogActions>
      </RuleEditorDialog>
    </>
  )
}
