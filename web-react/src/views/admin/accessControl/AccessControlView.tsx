import { useEffect, useRef, useState } from 'react'
import { Navigate, useLocation, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { isDestUnavailable } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import UserAutocomplete from '@/components/UserAutocomplete'
import { HelpTip } from '@/components/common/HelpTip'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { pushToast } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useDrawerParam, type DrawerState } from '@/hooks/useDrawerParam'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { IconInfo, IconPaused, IconSearch, IconTune } from '@/lib/icons'
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
import AllowlistTab from './allowlist/AllowlistTab'
import ListsTab from './lists/ListsTab'
import ListEntriesSheet from './lists/ListEntriesSheet'
import PoliciesTab from './policies/PoliciesTab'
import RecordsTab from './records/RecordsTab'
import CoverageSheet from './sheets/CoverageSheet'
import ExemptionsSheet from './sheets/ExemptionsSheet'
import TestSheet from './sheets/TestSheet'
import VerdictBar from './VerdictBar'

const HELP_KEY: Record<AccessTab, string> = {
  policies: 'admin:access_control.help.policies',
  lists: 'admin:access_control.help.lists',
  allowlist: 'admin:access_control.help.allowlist',
  records: 'admin:access_control.help.records',
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
  // The account picker takes a whole row of its own on a phone, where the header actions wrap under the title.
  const phone = useMediaQuery('(max-width: 639px)')
  const canView = useCan('access.view')
  const scope = useQueryScope()
  const location = useLocation()
  const [params, setParams] = useSearchParams()
  const tab = parseAccessTab(params.get('tab'))
  const status = useDestStatus(scope, { enabled: canView })
  // The help texts name tunables (the regexp cap, the apply window, the refresh interval); they are read, never written in.
  const policiesRead = useDestPolicies(scope, { enabled: canView })
  const listsRead = useDestLists(scope, { enabled: canView })
  const settingsRead = useDestSettings(scope, { enabled: canView && (tab === 'policies' || tab === 'records' || tab === 'allowlist') })
  const pause = usePauseDest(scope)
  const publish = usePublishDest(scope)
  const pendingEdit = useRef<number | null>(null)
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

  /** The rail's step and a group's badge: the allowlist tab, on a group's card when one is named. */
  const openAllowlist = (groupId?: number) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', 'allowlist')
    if (groupId) out.set('al_group', String(groupId))
    return out
  }, { replace: true })

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
  const openSettings = (focus?: SettingsFocus) => setSettings({ open: true, focus })
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
      pushToast({ message: t(paused ? 'admin:access_control.menu.paused_snack' : 'admin:access_control.menu.resumed_snack'), severity: 'success' })
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

  // The rest of the header sits in the More menu; pausing is the destructive item, last under its divider.
  const more: MoreMenuItem[] = [
    { id: 'settings', label: t('admin:access_control.menu.settings'), icon: IconTune, onSelect: () => openSettings() },
    { id: 'pause', label: t('admin:access_control.menu.pause'), icon: IconPaused, destructive: true, disabled: status.data?.paused === true, onSelect: () => void doPause(true) },
  ]

  // The picker sits among the header actions on a desktop. That action row is only as wide as its content, so
  // a phone's full-width picker has nothing to fill there: it gets a row of its own under the title instead.
  const picker = (
    <UserAutocomplete value={null} onChange={id => { if (id) account.open(id) }}
      label={t('admin:access_control.header.pick_user')} width={240} fullWidth={phone} />
  )

  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:access_control.title')}
        description={t('admin:access_control.subtitle')}
        secondary={(
          <>
            {!phone && picker}
            <Button variant="outline" onClick={() => sheet.open('test')}>
              <IconSearch />
              {t('admin:access_control.header.test')}
            </Button>
          </>
        )}
        more={more}
      />
      {phone && <div className="-mt-1 mb-4">{picker}</div>}

      {unwired
        ? <Alert><IconInfo aria-hidden /><AlertDescription>{t('admin:access_control.unwired')}</AlertDescription></Alert>
        : (
          <>
            <VerdictBar onOpenNodes={f => sheet.open('nodes', { extra: f ? { node_state: f } : undefined })}
              onOpenLists={() => setTab('lists')} onResume={() => doPause(false)} onPublish={doPublish} />
            <div className="mb-4 flex items-center justify-between gap-2">
              <Tabs value={tab} onValueChange={v => setTab(v as AccessTab)} className="min-w-0">
                <div className="max-w-full overflow-x-auto">
                  <TabsList>
                    <TabsTrigger value="policies">{t('admin:access_control.tab_policies')}</TabsTrigger>
                    <TabsTrigger value="lists">{t('admin:access_control.tab_lists')}</TabsTrigger>
                    <TabsTrigger value="allowlist">{t('admin:access_control.tab_allowlist')}</TabsTrigger>
                    <TabsTrigger value="records">{t('admin:access_control.tab_records')}</TabsTrigger>
                  </TabsList>
                </div>
              </Tabs>
              <HelpTip label={t('admin:access_control.help.label')} text={t(HELP_KEY[tab], {
                max_regexps: policiesRead.data?.budget.regexps.limit ?? '…',
                seconds: settingsRead.data?.effective.dest_policy_apply_min_seconds ?? '…',
                hours: listsRead.data?.refresh_hours ?? '…',
                days: tab === 'allowlist'
                  ? settingsRead.data?.effective.dest_trial_retention_days ?? '…'
                  : settingsRead.data?.effective.dest_hit_retention_days ?? '…',
              })} />
            </div>
            {/* Only the open tab mounts, so only its reads are made. */}
            {tab === 'policies' && (
              <PoliciesTab onOpenExemptions={() => sheet.open('exemptions')} onOpenSettings={openSettings} onOpenAllowlist={openAllowlist}
                onGoToLists={open => setTab('lists', open ? { openList: open } : undefined)} />
            )}
            {tab === 'records' && (
              <RecordsTab onOpenUser={id => account.open(id)} onTestDestination={target => sheet.open('test', { prefill: { target } })}
                onOpenSettings={openSettings} onOpenNodes={() => sheet.open('nodes')} onGoToPolicies={() => setTab('policies')} />
            )}
            {tab === 'allowlist' && (
              <AllowlistTab onOpenEntries={id => sheet.open('list', { extra: { list: String(id) } })} onOpenNodes={() => sheet.open('nodes')}
                onOpenSettings={openSettings} onTestDestination={target => sheet.open('test', { prefill: { target } })} />
            )}
            {tab === 'lists' && (
              <ListsTab onOpenEntries={id => sheet.open('list', { extra: { list: String(id) } })} onOpenSettings={openSettings}
                onOpenPolicy={id => openPolicy(id)} onOpenAllowlist={openAllowlist} />
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
    </div>
  )
}
