import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PageHeader } from '@/components/common/PageHeader'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import AuditLogsTab from './logs/AuditLogsTab'
import AuthLogsTab from './logs/AuthLogsTab'
import CertEventsTab from './logs/CertEventsTab'
import EmailLogsTab from './logs/EmailLogsTab'
import SubLogsTab from './logs/SubLogsTab'
import { parseRowId, useAdminT } from './logs/shared'
import { useUserFilter } from './logs/userFilter'

const TABS = ['sub', 'audit', 'auth', 'email', 'certs'] as const

/**
 * The logs: five tabs, each a server-paged list with its own filters. The location and risk tabs moved
 * to the admin-only risk center; their old ?tab=geo|risk links are sent on by LogsRoute (admins) or
 * fall back to 'sub' here (operators, who cannot open the risk center).
 *
 * Which tab, the keyword, the page and the opened record are all in the URL. A tab switch clears the
 * per-list ones (page, keyword, facets, record) and keeps the user filter, which the subscription and
 * sign-in tabs share.
 */
export default function LogsView() {
  const t = useAdminT()
  const [url, setUrl] = useUrlStates({ tab: urlCodecs.oneOf(TABS, 'sub') })
  // The keys the tabs keep their own state under (see useLogView and AuthLogsTab); this page only clears them.
  const [, resetList] = useUrlStates({ page: urlCodecs.int(1), q: urlCodecs.string(), method: urlCodecs.string(), outcome: urlCodecs.string() })
  const sel = useUrlSelection<number>('row', { parse: parseRowId })
  const user = useUserFilter()
  const label = (tab: (typeof TABS)[number]) => t(`logs.tab_${tab}`)

  function switchTab(next: (typeof TABS)[number]) {
    sel.clear()
    setUrl({ tab: next })
    // The per-list state belongs to the list left behind.
    resetList({ page: 1, q: '', method: '', outcome: '' })
  }

  return (
    <div className="p-4 sm:p-6">
      <PageHeader title={t('logs.title')} />
      <Tabs value={url.tab} onValueChange={v => switchTab(v as (typeof TABS)[number])}>
        {/* Five tabs are wider than a phone: the list scrolls inside itself, the page does not. */}
        <TabsList className="max-w-full justify-start overflow-x-auto">
          {TABS.map(tab => <TabsTrigger key={tab} value={tab}>{label(tab)}</TabsTrigger>)}
        </TabsList>
      </Tabs>
      <div className="mt-3">
        {url.tab === 'sub' && <SubLogsTab user={user} />}
        {url.tab === 'audit' && <AuditLogsTab />}
        {url.tab === 'auth' && <AuthLogsTab user={user} />}
        {url.tab === 'email' && <EmailLogsTab />}
        {url.tab === 'certs' && <CertEventsTab />}
      </div>
    </div>
  )
}
