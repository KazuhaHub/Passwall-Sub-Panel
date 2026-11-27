// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { DestTestResult } from '@/api/accessControl'
import { renderAccess } from '@/test/accessControlHarness'
import EvalTrace, { VerdictLine } from './EvalTrace'

const result = (over: Partial<DestTestResult>): DestTestResult => ({
  verdict: 'block', terminating_step: 'g4', notes: [], conditional: false, unpublished: false, nodes: [],
  steps: [
    { step: 1, kind: 'allow', name: 'Office', policy_id: 2, result: 'miss' },
    { step: 2, kind: 'group', rule_id: 'g4x1', group_id: 4, name: 'Guests', result: 'miss' },
    { step: 3, kind: 'group', rule_id: 'g4', group_id: 4, name: 'Guests', result: 'hit' },
  ], ...over,
})

describe('an allowlist group in the test trace', () => {
  it('is named as the group, in its own words', () => {
    renderAccess(<EvalTrace result={result({})} />)
    expect(screen.getAllByText('白名单分组 · Guests')).toHaveLength(2)
  })
  it('a hit on it is a refusal by the allowlist; a report-only one only records', () => {
    const first = renderAccess(<VerdictLine result={result({})} />)
    expect(screen.getByText(/白名单分组「Guests」/)).toBeTruthy()
    first.unmount()
    renderAccess(<VerdictLine result={result({ verdict: 'observe', steps: [{ step: 1, kind: 'group', rule_id: 'g4', group_id: 4, name: 'Guests', report_only: true, result: 'hit' }] })} />)
    expect(screen.getByText(/仅记录/)).toBeTruthy()
  })
})
