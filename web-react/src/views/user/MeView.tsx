import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import {
  resetMyCredentials,
  useEmergencyAccess,
  type MeProfile,
} from '@/api/me'
import { confirm } from '@/components/ConfirmHost'
import { ErrorState } from '@/components/feedback/ErrorState'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTabParam } from '@/hooks/useTabParam'
import { meKeys } from '@/query/keys'
import { useMyProfile, useMyUsage } from '@/query/me'
import { useQueryScope } from '@/query/useQueryScope'
import { AccountMenu } from './me/AccountMenu'
import { AnnouncementBanner, AnnouncementPopup } from './me/Announcement'
import { EmergencyCard } from './me/EmergencyCard'
import { ClientList, RecommendedClient, splitClients } from './me/ImportClients'
import { PasswordDialog } from './me/PasswordDialog'
import { QuickLinks } from './me/QuickLinks'
import { RulesDialog } from './me/RulesDialog'
import { ServiceBanner } from './me/ServiceBanner'
import { serviceNotice } from './me/serviceNotice'
import { ServerStatusTab } from './me/ServerStatusTab'
import { SubscriptionCard } from './me/SubscriptionCard'
import { TrafficTab } from './me/TrafficTab'
import { UsageCard } from './me/UsageCard'
import { enabledSorted } from './me/format'
import { useMeT } from './me/useMeT'
import { useServerStatus } from './me/useServerStatus'
import PasskeyDialog from './PasskeyDialog'
import RecoveryCodesDialog from './RecoveryCodesDialog'
import TwoFactorDialog from './TwoFactorDialog'

const TABS = ['overview', 'traffic', 'clients', 'status'] as const
type Tab = (typeof TABS)[number]

/**
 * THE USER PORTAL: the one page an ordinary subscriber sees. Top to bottom: the announcement and, if the service is held,
 * why (coloured banners); who is signed in, with the security menu; the tabs (overview, traffic, clients, server
 * status). The overview leads with the client recommended for this device, then two columns: the subscription link with
 * its QR code, and the shortcuts, on the left; usage and emergency access on the right. On a phone the columns fold into
 * one stack in the order link, usage, shortcuts, emergency access. Traffic, clients and server status are one card each.
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

  // On a phone the tab strip scrolls sideways; a tab reached by address (?tab=status) or by the "More clients" shortcut
  // would otherwise be selected out of sight, and the person could not tell where they are.
  useEffect(() => {
    document.querySelector('[role="tab"][aria-selected="true"]')?.scrollIntoView?.({ inline: 'center', block: 'nearest' })
  }, [rawTab, profile])

  // The page frame: the shell gives its content area no padding, so the page pads itself and centres a column,
  // which a phone shows full width and a desktop shows up to 1200px wide, where the overview has room for two columns.
  const frame = (children: React.ReactNode) => <div className="mx-auto w-full max-w-[75rem] p-4 sm:p-6">{children}</div>

  // Only the FIRST load may take the page down. A refresh started by a dialog's onChanged() must not: tearing the tree
  // down unmounts that dialog, and PasskeyDialog would lose the one-time recovery codes it was just handed between the
  // server returning them and the person seeing them. The server returns them exactly once.
  if (profileQuery.isPending) return frame(<PageSkeleton />)
  // The whole page hangs off the profile, so a failed read says so and offers a retry instead of a blank screen.
  if (!profile) return frame(<ErrorState title={t('load_failed')} reason={profileQuery.error} retry={() => load()} />)

  const tab: Tab = rawTab
  // Whether there is a service hold to explain: the banner row is only laid out when it, or an announcement, has something to say.
  const held = serviceNotice(profile, t, undefined) !== null
  const announcement = profile.global_announcement
  const quickLinks = enabledSorted(profile.quick_links)
  const passkeyCount = profile.passkey_credentials?.length ?? 0

  return frame(
    <>
      {/* Banners first, whatever the tab: an announcement and, when the service is held, the reason. */}
      {((announcement?.enabled && announcement.title) || held) && (
        <div className="mb-4 flex flex-col gap-3">
          {announcement?.enabled && announcement.title && <AnnouncementBanner announcement={announcement} />}
          <ServiceBanner profile={profile} />
        </div>
      )}

      <div className="mb-5 flex items-center gap-3 sm:mb-6 sm:gap-4">
        <span aria-hidden className="grid size-12 shrink-0 place-items-center rounded-full bg-brand text-lg font-medium text-brand-foreground">
          {(profile.display_name || profile.upn).charAt(0).toUpperCase()}
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-xl leading-tight font-medium">{profile.display_name || profile.upn}</h1>
          <p className="truncate text-sm text-muted-foreground">{profile.upn}{profile.email ? ` · ${profile.email}` : ''}</p>
        </div>
        <AccountMenu
          profile={profile}
          onPassword={() => setPwdOpen(true)} onTotp={() => setTwoFAOpen(true)}
          onPasskeys={() => setPasskeyOpen(true)} onRecovery={() => setRecoveryOpen(true)}
        />
      </div>

      <Tabs value={tab} onValueChange={v => setTab(v as Tab)}>
        <div className="-mx-4 mb-4 overflow-x-auto px-4 pb-1 sm:mx-0 sm:mb-6 sm:px-0">
          <TabsList>
            <TabsTrigger value="overview">{t('tabs.overview')}</TabsTrigger>
            <TabsTrigger value="traffic">{t('tabs.traffic')}</TabsTrigger>
            <TabsTrigger value="clients">{t('tabs.clients')}</TabsTrigger>
            <TabsTrigger value="status">{t('tabs.server_status')}</TabsTrigger>
          </TabsList>
        </div>
      </Tabs>

      <div className="flex flex-col gap-4 sm:gap-6">
        {tab === 'overview' && split.hero && <RecommendedClient profile={profile} client={split.hero} onShowMore={() => setTab('clients')} />}
        {tab === 'clients' && split.hero && <RecommendedClient profile={profile} client={split.hero} />}

        {tab === 'overview' && (
          /* Two independent columns from md up, so a tall card on one side opens no gap on the other. Below md the column
             wrappers are `contents`, which removes them from the layout and makes every card a direct child of this one
             stack, where `order` puts them in the phone sequence: link, usage, shortcuts, emergency access. */
          <div className="flex flex-col gap-4 sm:gap-6 md:flex-row md:items-start">
            <div className="contents md:flex md:min-w-0 md:flex-[1.5] md:flex-col md:gap-6">
              <div className="order-1 md:order-none">
                <SubscriptionCard subUrl={profile.sub_url} maskSignal={maskSignal} onReset={reset} onOpenRules={() => setRulesOpen(true)} />
              </div>
              {quickLinks.length > 0 && <div className="order-3 md:order-none"><QuickLinks links={quickLinks} /></div>}
            </div>
            <div className="contents md:flex md:min-w-0 md:flex-1 md:flex-col md:gap-6">
              <div className="order-2 md:order-none">
                <UsageCard
                  limitBytes={profile.traffic_limit_bytes} usage={usage} usageLoading={usageQuery.isPending}
                  expireAt={profile.expire_at ?? null} resetPeriod={profile.traffic_reset_period} charged={server.charged}
                />
              </div>
              {profile.emergency_access?.enabled && <div className="order-4 md:order-none"><EmergencyCard emergency={profile.emergency_access} onUse={tryEmergency} /></div>}
            </div>
          </div>
        )}
        {tab === 'clients' && <ClientList profile={profile} clients={split.others} hasHero={!!split.hero} />}
        {tab === 'traffic' && <TrafficTab historyDays={profile.traffic_history_days} />}
        {tab === 'status' && <ServerStatusTab nodes={server.nodes} loading={server.loading} />}
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
