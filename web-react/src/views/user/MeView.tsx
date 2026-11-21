import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import {
  resetMyCredentials,
  useEmergencyAccess,
  type MeProfile,
} from '@/api/me'
import { PageHeader } from '@/components/common/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { ErrorState } from '@/components/feedback/ErrorState'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTabParam } from '@/hooks/useTabParam'
import { meKeys } from '@/query/keys'
import { useMyProfile, useMyUsage } from '@/query/me'
import { useQueryScope } from '@/query/useQueryScope'
import { AnnouncementBanner, AnnouncementPopup } from './me/Announcement'
import { EmergencyCard } from './me/EmergencyCard'
import { ClientList, RecommendedClient, splitClients } from './me/ImportClients'
import { PasswordDialog } from './me/PasswordDialog'
import { QuickLinks } from './me/QuickLinks'
import { RulesDialog } from './me/RulesDialog'
import { hasSecurityRows, SecurityTab } from './me/SecurityTab'
import { serviceNotice } from './me/serviceNotice'
import { ServerStatusTab } from './me/ServerStatusTab'
import { StatusCard } from './me/StatusCard'
import { SubscriptionCard } from './me/SubscriptionCard'
import { TrafficTab } from './me/TrafficTab'
import { UsageCard } from './me/UsageCard'
import { enabledSorted } from './me/format'
import { useMeT } from './me/useMeT'
import { useServerStatus } from './me/useServerStatus'
import PasskeyDialog from './PasskeyDialog'
import RecoveryCodesDialog from './RecoveryCodesDialog'
import TwoFactorDialog from './TwoFactorDialog'

const TABS = ['overview', 'traffic', 'clients', 'status', 'security'] as const
type Tab = (typeof TABS)[number]

/**
 * THE USER PORTAL: the one page an ordinary subscriber sees, mostly on a phone. It answers, in this order, whether the
 * subscription works (the status card), how much traffic is left and when the account expires, the subscription link
 * with its copy button, QR code and the import button of the client recommended for this device, and then account
 * security. Everything else is a tab: the full client list, the traffic chart, server status, security. `?tab=` keeps the
 * old values (overview, traffic, clients, status) and adds `security`.
 */
export default function MeView() {
  const { t } = useMeT()
  const [rawTab, setTab] = useTabParam<Tab>('tab', 'overview', TABS)
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const profileQuery = useMyProfile(scope)
  const usageQuery = useMyUsage(scope)
  const profile = profileQuery.data
  const usage = usageQuery.data ?? null
  const server = useServerStatus(rawTab === 'status')
  const split = splitClients(profile ?? {})

  const [pwdOpen, setPwdOpen] = useState(false)
  const [twoFAOpen, setTwoFAOpen] = useState(false)
  const [passkeyOpen, setPasskeyOpen] = useState(false)
  const [recoveryOpen, setRecoveryOpen] = useState(false)
  const [rulesOpen, setRulesOpen] = useState(false)
  // Bumped on every reload so the subscription card re-hides its link (see SubscriptionCard).
  const [maskSignal, setMaskSignal] = useState(0)

  async function load() {
    setMaskSignal(n => n + 1)
    // Usage is auxiliary: the profile read is the one the page cannot render without, so it alone decides the error state.
    await Promise.all([profileQuery.refetch(), usageQuery.refetch()])
  }

  async function reset() {
    const ok = await confirm({
      title: t('sub.reset'), message: t('sub.reset_warn'), destructive: true, confirmText: t('sub.reset'), initialFocus: 'cancel',
    })
    if (!ok) return
    const r = await resetMyCredentials()
    pushSnack(t('sub.reset_ok', { uuid: r.uuid }), 'success')
    await load()
  }

  async function tryEmergency() {
    try {
      // Not a hook: an API call (src/api/me.ts) whose name starts with `use`.
      // eslint-disable-next-line react-hooks/rules-of-hooks
      const res = await useEmergencyAccess()
      // The backend answers 200 only on success (forbidden and quota-reached come back as HTTP errors and throw below).
      const until = res.extended_until ?? res.until
      pushSnack(t('emergency.granted', { until: until ? new Date(until).toLocaleString() : '' }), 'success')
      // Merge the response's fresh counts in at once, so the card does not show the old "remaining" until the reload lands.
      queryClient.setQueryData<MeProfile>(meKeys.profile(scope), prev => prev ? {
        ...prev,
        expire_at: res.extended_until ?? res.until ?? prev.expire_at,
        emergency_access: prev.emergency_access ? {
          ...prev.emergency_access,
          used_count: res.used_count ?? prev.emergency_access.used_count,
          remaining: res.remaining ?? prev.emergency_access.remaining,
          max_count: res.max_count ?? prev.emergency_access.max_count,
          available: false,
          status: 'active',
          emergency_until: res.emergency_until ?? prev.emergency_access.emergency_until,
        } : prev.emergency_access,
      } : prev)
      await load()
    } catch (e) {
      pushSnack((e as { message?: string }).message ?? t('emergency.no_quota'), 'warning')
    }
  }

  // On a phone the tab strip scrolls sideways; a tab reached by address (?tab=security) or by the "More clients" shortcut
  // would otherwise be selected out of sight, and the person could not tell where they are.
  useEffect(() => {
    document.querySelector('[role="tab"][aria-selected="true"]')?.scrollIntoView?.({ inline: 'center', block: 'nearest' })
  }, [rawTab, profile])

  // The page frame: the shell gives its content area no padding, so the page pads itself and centres a narrow column,
  // which is what a phone shows full width and a desktop shows as one readable strip.
  const frame = (children: React.ReactNode) => <div className="mx-auto w-full max-w-3xl p-4 sm:p-6">{children}</div>

  // Only the FIRST load may take the page down. A refresh started by a dialog's onChanged() must not: tearing the tree
  // down unmounts that dialog, and PasskeyDialog would lose the one-time recovery codes it was just handed between the
  // server returning them and the person seeing them. The server returns them exactly once.
  if (profileQuery.isPending) return frame(<PageSkeleton />)
  // The whole page hangs off the profile, so a failed read says so and offers a retry instead of a blank screen.
  if (!profile) return frame(<ErrorState title={t('load_failed')} reason={profileQuery.error} retry={() => load()} />)

  const securityOn = hasSecurityRows(profile)
  const tab: Tab = rawTab === 'security' && !securityOn ? 'overview' : rawTab
  const announcement = profile.global_announcement
  const quickLinks = enabledSorted(profile.quick_links)
  const passkeyCount = profile.passkey_credentials?.length ?? 0
  // A service hold is the thing to know on every tab, so it follows the person to the others; the all-clear does not.
  const held = serviceNotice(profile, t, undefined) !== null

  return frame(
    <>
      <PageHeader
        title={profile.display_name || profile.upn}
        description={profile.display_name ? `${profile.upn}${profile.email ? ` · ${profile.email}` : ''}` : profile.email || undefined}
      />
      <div className="flex flex-col gap-4">
        {announcement?.enabled && announcement.title && <AnnouncementBanner announcement={announcement} />}
        {(tab === 'overview' || held) && <StatusCard profile={profile} usage={usage} usageLoading={usageQuery.isPending} />}

        <Tabs value={tab} onValueChange={v => setTab(v as Tab)}>
          <div className="-mx-4 overflow-x-auto px-4 pb-1 sm:mx-0 sm:px-0">
            <TabsList>
              <TabsTrigger value="overview">{t('tabs.overview')}</TabsTrigger>
              <TabsTrigger value="traffic">{t('tabs.traffic')}</TabsTrigger>
              <TabsTrigger value="clients">{t('tabs.clients')}</TabsTrigger>
              <TabsTrigger value="status">{t('tabs.server_status')}</TabsTrigger>
              {securityOn && <TabsTrigger value="security">{t('tabs.security')}</TabsTrigger>}
            </TabsList>
          </div>
        </Tabs>

        {tab === 'overview' && (
          <>
            <UsageCard
              limitBytes={profile.traffic_limit_bytes} usage={usage} usageLoading={usageQuery.isPending}
              expireAt={profile.expire_at ?? null} resetPeriod={profile.traffic_reset_period} charged={server.charged}
            />
            <SubscriptionCard subUrl={profile.sub_url} maskSignal={maskSignal} onReset={reset} onOpenRules={() => setRulesOpen(true)} />
            {split.hero && <RecommendedClient profile={profile} client={split.hero} onShowMore={() => setTab('clients')} />}
            {profile.emergency_access?.enabled && <EmergencyCard emergency={profile.emergency_access} onUse={tryEmergency} />}
            <QuickLinks links={quickLinks} />
          </>
        )}
        {tab === 'clients' && (
          <>
            {split.hero && <RecommendedClient profile={profile} client={split.hero} />}
            <ClientList profile={profile} clients={split.others} hasHero={!!split.hero} />
          </>
        )}
        {tab === 'traffic' && <TrafficTab historyDays={profile.traffic_history_days} />}
        {tab === 'status' && <ServerStatusTab nodes={server.nodes} loading={server.loading} />}
        {tab === 'security' && securityOn && (
          <SecurityTab
            profile={profile}
            onPassword={() => setPwdOpen(true)} onTotp={() => setTwoFAOpen(true)}
            onPasskeys={() => setPasskeyOpen(true)} onRecovery={() => setRecoveryOpen(true)}
          />
        )}
      </div>

      <AnnouncementPopup announcement={announcement} />
      <PasswordDialog open={pwdOpen} onClose={() => setPwdOpen(false)} />
      <RulesDialog open={rulesOpen} canEdit={profile.can_edit_personal_rules} onClose={() => setRulesOpen(false)} />
      <TwoFactorDialog
        open={twoFAOpen} enabled={!!profile.totp_enabled} hasPasskey={passkeyCount > 0}
        onClose={() => setTwoFAOpen(false)} onChanged={() => { void load() }}
      />
      <PasskeyDialog
        open={passkeyOpen} available={!!profile.passkey_available} credentials={profile.passkey_credentials ?? []}
        onClose={() => setPasskeyOpen(false)} onChanged={() => { void load() }}
      />
      <RecoveryCodesDialog
        open={recoveryOpen} remaining={profile.recovery_codes_remaining ?? 0} hasPasskey={passkeyCount > 0}
        totpEnabled={!!profile.totp_enabled} onClose={() => setRecoveryOpen(false)} onChanged={() => { void load() }}
      />
    </>,
  )
}
