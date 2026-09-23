import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Divider,
  IconButton,
  List,
  ListItemButton,
  ListItemText,
  Stack,
  TextField,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import { useTranslation } from 'react-i18next'

import { inspectProxyGroups, type MihomoRematchOutbound, type MihomoSubRule, type ProxyGroupIssue, type RuleSet } from '@/api/rules'

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
  const [issues, setIssues] = useState<ProxyGroupIssue[]>([])
  const [checking, setChecking] = useState(false)
  const serialized = useMemo(() => JSON.stringify([
    value.content,
    value.proxy_group_members || {},
    value.proxy_group_options || {},
    value.mihomo_rules || '',
    subRules,
    rematches,
  ]), [value.content, value.proxy_group_members, value.proxy_group_options, value.mihomo_rules, subRules, rematches])

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setChecking(true)
      void inspectProxyGroups({
        content: value.content,
        proxy_group_members: value.proxy_group_members || {},
        proxy_group_options: value.proxy_group_options || {},
        mihomo_rules: value.mihomo_rules || '',
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

  return (
    <Stack spacing={2}>
      <Alert severity="warning">{t('admin:rules.mihomo.compatibility')}</Alert>

      <Box>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 0.75 }}>
          <Box>
            <Typography sx={{ fontWeight: 600 }}>{t('admin:rules.mihomo.main_rules')}</Typography>
            <Typography variant="caption" color="text.secondary">{t('admin:rules.mihomo.main_rules_hint')}</Typography>
          </Box>
          {checking && <CircularProgress size={18} />}
        </Stack>
        <Box sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, overflow: 'hidden' }}>
          <Suspense fallback={<Box sx={{ height: 180, display: 'grid', placeItems: 'center' }}><CircularProgress size={22} /></Box>}>
            <CodeEditor value={value.mihomo_rules || ''} onChange={mihomo_rules => patch({ mihomo_rules })} height="180px" dark={theme.palette.mode === 'dark'} />
          </Suspense>
        </Box>
      </Box>

      <Divider />

      <Box>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
          <Box>
            <Typography sx={{ fontWeight: 600 }}>{t('admin:rules.mihomo.rematches')}</Typography>
            <Typography variant="caption" color="text.secondary">{t('admin:rules.mihomo.rematches_hint')}</Typography>
          </Box>
          <Button size="small" startIcon={<AddIcon />} onClick={addRematch}>{t('admin:rules.members.add')}</Button>
        </Stack>
        <Stack spacing={1}>
          {rematches.map((outbound, index) => (
            <Box key={index} sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr 1fr auto' }, gap: 1, p: 1.5, border: `1px solid ${md.outlineVariant}`, borderRadius: 2 }}>
              <TextField size="small" label={t('admin:rules.mihomo.outbound_name')} value={outbound.name}
                onChange={event => updateRematch(index, { ...outbound, name: event.target.value })} />
              <TextField size="small" label={t('admin:rules.mihomo.target_rematch_name')} value={outbound.target_rematch_name || ''}
                onChange={event => updateRematch(index, { ...outbound, target_rematch_name: event.target.value })} />
              <TextField select size="small" label={t('admin:rules.mihomo.target_sub_rule')} value={outbound.target_sub_rule || ''}
                onChange={event => updateRematch(index, { ...outbound, target_sub_rule: event.target.value })}
                slotProps={{ select: { native: true } }}>
                <option value="">—</option>
                {subRules.filter(rule => rule.name.trim()).map(rule => <option key={rule.name} value={rule.name}>{rule.name}</option>)}
              </TextField>
              <IconButton color="error" onClick={() => patch({ mihomo_rematch_outbounds: rematches.filter((_, i) => i !== index) })}><DeleteIcon /></IconButton>
            </Box>
          ))}
          {!rematches.length && <Typography variant="body2" color="text.secondary">{t('admin:rules.mihomo.no_rematches')}</Typography>}
        </Stack>
      </Box>

      <Divider />

      <Box>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
          <Box>
            <Typography sx={{ fontWeight: 600 }}>{t('admin:rules.mihomo.sub_rules')}</Typography>
            <Typography variant="caption" color="text.secondary">{t('admin:rules.mihomo.sub_rules_hint')}</Typography>
          </Box>
          <Button size="small" startIcon={<AddIcon />} onClick={addSubRule}>{t('admin:rules.members.add')}</Button>
        </Stack>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '240px minmax(0, 1fr)' }, border: `1px solid ${md.outlineVariant}`, borderRadius: 2, overflow: 'hidden', minHeight: 260 }}>
          <List dense disablePadding sx={{ bgcolor: md.surfaceContainerLow, borderRight: { md: `1px solid ${md.outlineVariant}` } }}>
            {subRules.map((rule, index) => (
              <ListItemButton key={`${rule.name}:${index}`} selected={index === selectedIndex} onClick={() => setSelectedSubRuleIndex(index)}>
                <ListItemText primary={rule.name || t('admin:rules.mihomo.unnamed_sub_rule')} />
                <IconButton size="small" color="error" onClick={event => { event.stopPropagation(); removeSubRule(index) }}><DeleteIcon fontSize="small" /></IconButton>
              </ListItemButton>
            ))}
          </List>
          <Box sx={{ p: 1.5, minWidth: 0 }}>
            {selected ? <>
              <TextField fullWidth size="small" label={t('admin:rules.mihomo.sub_rule_name')} value={selected.name}
                onChange={event => updateSubRule(selectedIndex, { ...selected, name: event.target.value })} sx={{ mb: 1 }} />
              <Box sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, overflow: 'hidden' }}>
                <Suspense fallback={<Box sx={{ height: 190, display: 'grid', placeItems: 'center' }}><CircularProgress size={22} /></Box>}>
                  <CodeEditor value={selected.content} onChange={content => updateSubRule(selectedIndex, { ...selected, content })} height="190px" dark={theme.palette.mode === 'dark'} />
                </Suspense>
              </Box>
            </> : <Box sx={{ py: 8, textAlign: 'center', color: md.onSurfaceVariant }}>{t('admin:rules.mihomo.no_sub_rules')}</Box>}
          </Box>
        </Box>
      </Box>

      {issues.map((issue, index) => (
        <Alert key={`${issue.code}:${issue.name}:${index}`} severity={issue.level}>
          {t(`admin:rules.mihomo.issues.${issue.code}`, { ...issue.params, name: issue.name, defaultValue: issue.message })}
        </Alert>
      ))}
    </Stack>
  )
}
