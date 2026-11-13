// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '@/test/accessControlHarness'
import { renderAccess } from '@/test/accessControlHarness'
import ParseReport from './ParseReport'

describe('ParseReport', () => {
  it('does not crash on samples:null / warnings:null', () => {
    const report = { accepted: 1, ignored: 0, rewritten: 0, duplicates: 0, samples: null, warnings: null, warning_count: 0 } as never
    renderAccess(<ParseReport report={report} />)
    expect(document.body.textContent).toBeTruthy()
    expect(screen.queryByText(/undefined/)).toBeNull()
  })
})
