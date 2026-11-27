import type { MeProfile } from '@/api/me'
import { Banner } from './Announcement'
import { serviceNotice } from './serviceNotice'
import { useMeT } from './useMeT'

/**
 * WHY THE SUBSCRIPTION IS NOT WORKING, as a coloured banner at the top of every tab, with the same texts as ever. It is
 * the one thing a person must see wherever they are on the page, so it does not belong to a tab. While the service is on
 * there is nothing to say, and nothing is drawn: no all-clear card pushing the page down.
 */
export function ServiceBanner({ profile }: { profile: MeProfile }) {
  const { t, i18n } = useMeT()
  const notice = serviceNotice(profile, t, i18n.language)
  if (!notice) return null
  return <Banner tone={notice.tone === 'neutral' ? 'info' : notice.tone} title={notice.title} testId="service-status">{notice.body}</Banner>
}
