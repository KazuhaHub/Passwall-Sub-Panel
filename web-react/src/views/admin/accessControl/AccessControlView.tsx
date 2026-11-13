import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Divider, IconButton, ListItemIcon, ListItemText, Menu, MenuItem, Tab, Tabs, Button, useMediaQuery, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutlineOutlined'
import SearchIcon from '@mui/icons-material/Search'
import TuneIcon from '@mui/icons-material/Tune'
import { Navigate, useLocation, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { isDestUnavailable } from '@/api/accessControl'
import HelpTip from '@/components/HelpTip'
import PageHeader from '@/components/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useDrawerParam, type DrawerState } from '@/hooks/useDrawerParam'
import { useDestLists, useDestPolicies, useDestSettings, useDestStatus, usePauseDest, usePublishDest } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { useCan } from '@/utils/permissions'
import AccessSettingsDialog from './AccessSettingsDialog'
import AccountDrawerHost from './AccountDrawerHost'
import {
  parseAccessSheet, parseAccessTab, parseListId, parseNodeFilter, type AccessNavState, type AccessSheet,
  type AccessTab, type NodeFilter, type SettingsFocus,
} from './accessParams'
import { pauseConfirm, resumeConfirm } from './confirmCopy'
import { reportDestError } from './destErrors'
import ListsTab from './lists/ListsTab'
import ListEntriesSheet from './lists/ListEntriesSheet'
import PoliciesTab from './policies/PoliciesTab'
import CoverageSheet from './sheets/CoverageSheet'
import ExemptionsSheet from './sheets/ExemptionsSheet'
import TestSheet from './sheets/TestSheet'
import VerdictBar from './VerdictBar'

const HELP_KEY: Record<AccessTab, string> = {
  policies: 'admin:access_control.help.policies',
  lists: 'admin:access_control.help.lists',
}

/**
 * ACCESS CONTROL: one admin-only page for where proxy users may go. The
 * destination policies native nodes enforce (what to block, what to let
 * through anyway), the lists they read, which accounts are exempt, and how far
 * the policy has landed across the fleet. The page verdict (VerdictBar) answers
 * the second question first; the tabs answer the first.
 *
 * The URL owns the tab, the lists tab's filter, the sheet that is open and the
 * account drawer (accessParams). A destination or a search term never goes in
 * it (R12): a test sheet's prefill rides in history state.
 */
export default function AccessControlView() {
  const { t } = useTranslation(['admin', 'common'])
  const md = useTheme().palette.md
  const phone = useMediaQuery(useTheme().breakpoints.down('sm'))
  const canView = useCan('access.view')
  const scope = useQueryScope()
  const location = useLocation()
  const [params, setParams] = useSearchParams()
  const tab = parseAccessTab(params.get('tab'))
  const status = useDestStatus(scope, { enabled: canView })
  // The help texts name tunables (the regexp cap, the apply window, the refresh interval); they are read, never written in.
  const policiesRead = useDestPolicies(scope, { enabled: canView })
  const listsRead = useDestLists(scope, { enabled: canView })
  const settingsRead = useDestSettings(scope, { enabled: canView && tab === 'policies' })
  const pause = usePauseDest(scope)
  const publish = usePublishDest(scope)
  const pendingEdit = useRef<number | null>(null)
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const [settings, setSettings] = useState<{ open: boolean; focus?: SettingsFocus }>({ open: false })

  // One drawer at a time. The page-level sheets are one string param; the
  // account drawer another. `list=<id>` rides with `sheet=list`.
  const sheet = useDrawerParam<AccessSheet>('sheet', { parse: parseAccessSheet, exclusive: ['user'], companions: ['list', 'node_state'] })
  const account = useDrawerParam('user', { exclusive: ['sheet', 'list', 'node_state'] })
  const prefill = (location.state as DrawerState | null)?.prefill

  // Replace, not push: a tab switch is a view of the same page, and Back should leave the page.
  const setTab = (next: AccessTab, state?: AccessNavState) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', next)
    return out
  }, { replace: true, state })

  const openListDialog = (id: number) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', 'lists')
    out.delete('sheet')
    out.delete('list')
    return out
  }, { replace: true, state: { openList: id } satisfies AccessNavState })
  // The edit that was waiting for Back to land (see onEdit below).
  useEffect(() => {
    if (pendingEdit.current === null || sheet.value === 'list') return
    const id = pendingEdit.current
    pendingEdit.current = null
    openListDialog(id)
    // openListDialog is a plain function of this render; this runs once per landed location.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.key])

  if (!canView) return <Navigate to="/admin/dashboard" replace />

  const minutes = applyMinutes(status.data)
  const confirmArgs = { t, minutes }
  const openSettings = (focus?: SettingsFocus) => { setMenu(null); setSettings({ open: true, focus }) }
  const nodeFilter = parseNodeFilter(params.get('node_state'))
  const setNodeFilter = (next: NodeFilter | null) => setParams(prev => {
    const out = new URLSearchParams(prev)
    if (next) out.set('node_state', next)
    else out.delete('node_state')
    return out
  }, { replace: true })

  const doPublish = async () => {
    try {
      await publish.mutateAsync()
    } catch (err) {
      reportDestError(t, err)
    }
  }
  const doPause = async (paused: boolean) => {
    const ok = await confirm(paused ? pauseConfirm(confirmArgs) : resumeConfirm(confirmArgs))
    if (!ok) return
    try {
      await pause.mutateAsync(paused)
      pushSnack(t(paused ? 'admin:access_control.menu.paused_snack' : 'admin:access_control.menu.resumed_snack'), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  // A test result's "open this policy": leave the sheet, switch to the policies
  // tab, and hand it the policy to highlight and open (state, never the URL).
  const openPolicy = (id: number) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', 'policies')
    out.delete('sheet')
    out.delete('list')
    return out
  }, { replace: true, state: { openPolicy: id, highlightPolicy: id } satisfies AccessNavState })

  const unwired = status.isError && isDestUnavailable(status.error)

  const headerActions = (
    <Box data-header-actions sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1, width: { xs: '100%', sm: 'auto' } }}>
      <Box sx={{ order: 1, ml: { xs: 'auto', sm: 0 } }}>
        {phone
          ? (
            <IconButton aria-label={t('admin:access_control.header.test')} onClick={() => sheet.open('test')}>
              <SearchIcon />
            </IconButton>
          )
          : (
            <Button variant="outlined" startIcon={<SearchIcon />} onClick={() => sheet.open('test')}>
              {t('admin:access_control.header.test')}
            </Button>
          )}
      </Box>
      <Box sx={{ order: { xs: 3, sm: 2 }, flex: { xs: '1 1 100%', sm: '0 0 auto' }, minWidth: 0 }}>
        <UserAutocomplete value={null} onChange={id => { if (id) account.open(id) }}
          label={t('admin:access_control.header.pick_user')} width={280} fullWidth={phone} />
      </Box>
      <Box sx={{ order: { xs: 2, sm: 3 } }}>
        <IconButton aria-label={t('admin:access_control.menu.label')} aria-haspopup="menu" onClick={e => setMenu(e.currentTarget)}>
          <MoreVertIcon />
        </IconButton>
      </Box>
      <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
        <MenuItem onClick={() => openSettings()}>
          <ListItemIcon><TuneIcon fontSize="small" /></ListItemIcon>
          <ListItemText>{t('admin:access_control.menu.settings')}</ListItemText>
        </MenuItem>
        <Divider />
        <MenuItem disabled={status.data?.paused === true} onClick={() => { setMenu(null); void doPause(true) }}
          sx={{ color: md.error }}>
          <ListItemIcon><PauseCircleOutlineIcon fontSize="small" sx={{ color: md.error }} /></ListItemIcon>
          <ListItemText>{t('admin:access_control.menu.pause')}</ListItemText>
        </MenuItem>
      </Menu>
    </Box>
  )

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}>
      {/* PageHeader shrink-wraps its actions, which cannot give the phone's picker a full row: on a phone the
          actions are their own full-width block under the heading instead. */}
      <PageHeader title={t('admin:access_control.title')} subtitle={t('admin:access_control.subtitle')}
        actions={phone ? undefined : headerActions} />
      {phone && <Box sx={{ mb: 2 }}>{headerActions}</Box>}

      {unwired
        ? <Alert severity="info">{t('admin:access_control.unwired')}</Alert>
        : (
          <>
            <VerdictBar onOpenNodes={f => sheet.open('nodes', { extra: f ? { node_state: f } : undefined })}
              onOpenLists={() => setTab('lists')} onResume={() => doPause(false)} onPublish={doPublish} />
            <Box sx={{ display: 'flex', alignItems: 'stretch', mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
              <Tabs value={tab} onChange={(_, v: AccessTab) => setTab(v)} variant="scrollable" allowScrollButtonsMobile
                sx={{ flex: 1, minWidth: 0 }}>
                <Tab value="policies" label={t('admin:access_control.tab_policies')} />
                <Tab value="lists" label={t('admin:access_control.tab_lists')} />
              </Tabs>
              <HelpTip textKey={HELP_KEY[tab]} labelKey="admin:access_control.help.label" textValues={{
                max_regexps: policiesRead.data?.budget.regexps.limit ?? '…',
                seconds: settingsRead.data?.effective.dest_policy_apply_min_seconds ?? '…',
                hours: listsRead.data?.refresh_hours ?? '…',
              }} />
            </Box>
            {/* Only the open tab mounts, so only its reads are made. */}
            {tab === 'policies' && (
              <PoliciesTab onOpenExemptions={() => sheet.open('exemptions')} onOpenSettings={openSettings}
                onGoToLists={open => setTab('lists', open ? { openList: open } : undefined)} />
            )}
            {tab === 'lists' && (
              <ListsTab onOpenEntries={id => sheet.open('list', { extra: { list: String(id) } })} onOpenSettings={openSettings}
                onOpenPolicy={id => openPolicy(id)} />
            )}
          </>
        )}

      <TestSheet open={sheet.value === 'test'} onClose={sheet.close} prefill={prefill} onOpenPolicy={openPolicy} />
      <CoverageSheet open={sheet.value === 'nodes'} onClose={sheet.close} filter={nodeFilter}
        onFilterChange={setNodeFilter} onOpenSettings={openSettings} />
      <ExemptionsSheet open={sheet.value === 'exemptions'} onClose={sheet.close}
        onOpenUser={id => account.open(id, { replace: true })} />
      <ListEntriesSheet listId={sheet.value === 'list' ? parseListId(params.get('list')) : null} onClose={sheet.close}
        onEdit={id => {
          // A sheet that was PUSHED is closed by going Back, so history keeps one
          // page entry; the dialog is opened once that navigation has landed.
          if ((location.state as DrawerState | null)?.drawer === 'sheet') {
            pendingEdit.current = id
            sheet.close()
            return
          }
          openListDialog(id)
        }}
        onTest={target => sheet.open('test', { replace: true, prefill: { target } })} />
      <AccessSettingsDialog open={settings.open} focus={settings.focus} onClose={() => setSettings({ open: false })} />
      <AccountDrawerHost userId={account.value} onClose={account.close} />
    </Box>
  )
}
