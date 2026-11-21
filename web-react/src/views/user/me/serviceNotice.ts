import type { TFunction } from 'i18next'
import type { MeProfile } from '@/api/me'
import type { Tone } from '@/components/common/tones'
import { serviceStateOf } from './format'

export interface ServiceNotice {
  /** The service state this explains; also a stable hook for tests. */
  id: string
  tone: Tone
  title: string
  body: string
}

/**
 * Why the subscription is not working, in plain words, or null while the service is on.
 *
 * Two independent axes decide access (the backend resolves them in AccessSnapshot): the ACCOUNT axis gates signing in
 * and the SERVICE axis gates the subscription and the proxy. A person who can read this page is past the account
 * axis, so what is explained here is the service axis, and each text says the person can still sign in. Quota and
 * expiry are service holds only, which is what lets an over-quota user come here to find out why.
 *
 * `access.service_state` wins over the flat `service_status`, as before. States this page does not know (a newer
 * backend) give no notice rather than a wrong one.
 */
export function serviceNotice(p: MeProfile, t: TFunction, language: string | undefined): ServiceNotice | null {
  const state = serviceStateOf(p)
  switch (state) {
    case 'expired':
      return { id: state, tone: 'danger', title: t('service.expired_title'), body: t('service.expired_body') }
    case 'traffic_exceeded':
      return { id: state, tone: 'warning', title: t('service.traffic_title'), body: t('service.traffic_body') }
    case 'blocked_client':
      return {
        id: state, tone: 'danger', title: t('service.blocked_title'),
        body: p.service_disable_detail || t('service.blocked_body'),
      }
    case 'manual_suspended':
      // The detector's own time-boxed hold: the access snapshot reports it as manual_suspended like any other hold.
      if (p.service_disabled_reason === 'geo_auto') {
        return {
          id: 'geo_auto', tone: 'warning', title: t('service.geo_auto_title'),
          // The stored detail is Chinese and states the minutes (the traffic poll's geoAutoSuspendDetail); a Chinese UI
          // keeps it. Other languages get the localized generic sentence instead of Chinese text. An admin's own note
          // (below) is theirs, in whatever language they wrote it, and is shown as it is.
          body: (language?.startsWith('zh') && p.service_disable_detail) || t('service.geo_auto_body'),
        }
      }
      return { id: state, tone: 'danger', title: t('service.suspended_title'), body: p.service_disable_detail || t('service.suspended_body') }
    case 'account_disabled':
      return { id: state, tone: 'danger', title: t('service.account_title'), body: t('service.account_body') }
    default:
      return null
  }
}
