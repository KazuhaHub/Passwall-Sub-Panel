import { describe, expect, it, vi } from 'vitest'

// The wire modules import the shared axios client, which reads the document
// at import time. These are pure-function tests; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import type { AttentionEntry, QueueRow, ReviewBadge, RiskUserSummary } from '@/api/riskCenter'
import {
  availableActions, expectedLevels, holdLabelKey, otherHold, pauseReason, subjectOfRow, subjectOfSummary,
  type RiskActionKind, type RiskSubject,
} from './riskSubject'

const NO_REVIEW: ReviewBadge = { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] }
const GEO: AttentionEntry[] = [{ source: 'geo', level: 'flagged' }]

function subject(over: Partial<RiskSubject> = {}): RiskSubject {
  return {
    id: 7, upn: 'alice', service_state: 'active', service_disabled_reason: '', attention: GEO,
    review: NO_REVIEW, ...over,
  }
}

// The one action matrix (§3.6), for the drawer's action bar and the queue's
// row menu alike: a row and a summary of the same account must offer the
// same actions, or the menu would promise what the drawer then withholds.
describe('availableActions', () => {
  const cases: [string, Partial<RiskSubject>, RiskActionKind[]][] = [
    ['active, geo flagged', {}, ['pause', 'dismiss', 'trust']],
    ['emergency access still counts as serving', { service_state: 'emergency_active' }, ['pause', 'dismiss', 'trust']],
    ['held by the detector', { service_state: 'manual_suspended', service_disabled_reason: 'geo_auto',
      attention: [...GEO, { source: 'geo_auto', level: 'suspended' }] }, ['convert_manual', 'resume', 'dismiss', 'trust']],
    ['an admin geo hold', { service_state: 'manual_suspended', service_disabled_reason: 'geo_anomaly' },
      ['resume', 'dismiss', 'trust']],
    ['a manual service hold', { service_state: 'manual_suspended', service_disabled_reason: 'service_manual' },
      ['resume', 'dismiss', 'trust']],
    ['another hold is read-only here', { service_state: 'blocked_client', service_disabled_reason: 'blocked_client' },
      ['dismiss', 'trust']],
    ['over quota is read-only here', { service_state: 'traffic_exceeded' }, ['dismiss', 'trust']],
    ['dismissed and in force', { review: { ...NO_REVIEW, dismissed: true } }, ['pause', 'undismiss', 'trust']],
    ['dismissed but escalated', { review: { ...NO_REVIEW, dismissed: true, reopened: true, escalated: ['geo'] } },
      ['pause', 'redismiss', 'undismiss', 'trust']],
    ['dismissed but lapsed', { review: { ...NO_REVIEW, dismissed: true, lapsed: true } },
      ['pause', 'redismiss', 'undismiss', 'trust']],
    ['trusted', { review: { ...NO_REVIEW, trusted: true } }, ['pause', 'dismiss', 'untrust']],
    ['trusted with nothing at attention', { attention: [], review: { ...NO_REVIEW, trusted: true } }, ['pause', 'untrust']],
    ['nothing at attention', { attention: [] }, ['pause', 'trust']],
  ]

  it.each(cases)('drawer: %s', (_, over, want) => {
    expect(availableActions(subject(over), 'drawer')).toEqual(want)
  })

  it('the row menu offers trust only where trust would change something', () => {
    // Trust exempts the LOCATION detectors; a devices-only row gains nothing
    // from it, and offering it there reads as the cure for a device count.
    const devices = subject({ attention: [{ source: 'devices', level: 'suspect' }] })
    expect(availableActions(devices, 'menu')).toEqual(['pause', 'dismiss'])
    expect(availableActions(devices, 'drawer')).toEqual(['pause', 'dismiss', 'trust'])

    for (const src of ['geo', 'sub_spread', 'login_country']) {
      expect(availableActions(subject({ attention: [{ source: src, level: 'suspect' }] }), 'menu')).toContain('trust')
    }
    const held = subject({ service_state: 'manual_suspended', service_disabled_reason: 'geo_auto',
      attention: [{ source: 'geo_auto', level: 'suspended' }] })
    expect(availableActions(held, 'menu')).toContain('trust')
    expect(availableActions(subject({ review: { ...NO_REVIEW, trusted: true } }), 'menu')).not.toContain('trust')
  })
})

describe('pauseReason', () => {
  // geo_anomaly exists to count geo false positives: only a pause that the
  // location evidence prompted may carry it.
  it('is geo_anomaly when geo or the geo hold is a current source', () => {
    expect(pauseReason(subject())).toBe('geo_anomaly')
    expect(pauseReason(subject({ attention: [{ source: 'geo_auto', level: 'suspended' }] }))).toBe('geo_anomaly')
  })

  it('is the Users page reason otherwise', () => {
    expect(pauseReason(subject({ attention: [{ source: 'devices', level: 'flagged' }] }))).toBe('service_manual')
    expect(pauseReason(subject({ attention: [{ source: 'sub_spread', level: 'flagged' }] }))).toBe('service_manual')
    expect(pauseReason(subject({ attention: [] }))).toBe('service_manual')
  })
})

describe('otherHold and holdLabelKey', () => {
  it('names a hold nobody here may lift', () => {
    expect(otherHold(subject())).toBe(false)
    expect(otherHold(subject({ service_state: 'manual_suspended', service_disabled_reason: 'geo_auto' }))).toBe(false)
    expect(otherHold(subject({ service_state: 'blocked_client', service_disabled_reason: 'blocked_client' }))).toBe(true)
    expect(otherHold(subject({ service_state: 'expired' }))).toBe(true)
  })

  it('labels the three holds the drawer can lift', () => {
    expect(holdLabelKey('geo_auto')).toBe('admin:users.status.geo_auto')
    expect(holdLabelKey('geo_anomaly')).toBe('admin:users.status.geo_manual')
    expect(holdLabelKey('service_manual')).toBe('admin:users.status.service_suspended')
  })
})

describe('expectedLevels', () => {
  it('is every level the admin saw, the hold included', () => {
    const s = subject({ attention: [...GEO, { source: 'devices', level: 'suspect' }, { source: 'geo_auto', level: 'suspended' }] })
    expect(expectedLevels(s)).toEqual({ geo: 'flagged', devices: 'suspect', geo_auto: 'suspended' })
  })
})

describe('subjects', () => {
  const review = {
    dismissed: true, dismissed_at_ms: 1, dismissed_by: 3, dismissed_by_upn: 'admin', note: '',
    levels: { geo: 'flagged' }, reopened: true, lapsed: false, escalated: ['devices'],
    trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '',
  }

  it('from a summary: the service state is the access decision, the hold its reason', () => {
    const s = subjectOfSummary({
      user: { id: 7, upn: 'alice', display_name: '', role: 'user', group_id: 1, group_name: '', enabled: true,
        traffic_limit_bytes: 0, service_disabled_reason: 'geo_auto', service_disabled_at_ms: 5,
        access: { account_state: 'active', service_state: 'manual_suspended', can_login: true, can_use_portal: true,
          can_subscribe: false, proxy_enabled: false } },
      attention: [{ source: 'geo_auto', level: 'suspended' }],
      review,
    } as unknown as RiskUserSummary)
    expect(s).toEqual({
      id: 7, upn: 'alice', service_state: 'manual_suspended', service_disabled_reason: 'geo_auto',
      attention: [{ source: 'geo_auto', level: 'suspended' }],
      review: { dismissed: true, reopened: true, lapsed: false, trusted: false, escalated: ['devices'] },
    })
  })

  it('from a queue row: the same matrix without a fetch', () => {
    const row = {
      user_id: 7, upn: 'alice', service_state: 'active', attention: undefined,
      sources: [{ source: 'devices', level: 'suspect' }],
      review: { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] },
    } as unknown as QueueRow
    const s = subjectOfRow(row)
    expect(s).toEqual({
      id: 7, upn: 'alice', service_state: 'active', service_disabled_reason: '',
      attention: [{ source: 'devices', level: 'suspect' }], review: NO_REVIEW,
    })
    expect(availableActions(s, 'menu')).toEqual(['pause', 'dismiss'])
  })
})
