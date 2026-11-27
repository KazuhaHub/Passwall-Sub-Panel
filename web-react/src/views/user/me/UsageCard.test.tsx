// @vitest-environment jsdom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '../testHarness'
import { UsageCard } from './UsageCard'

const GB = 1024 ** 3
const inDays = (n: number) => new Date(Date.now() + n * 86400000).toISOString()
const usage = (gb: number) => ({ user_id: 1, permanent_total_bytes: 0, period_used_bytes: gb * GB, today_used_bytes: 0 })
const card = (over: { expireAt?: string | null; usedGB?: number } = {}) => (
  <UsageCard limitBytes={100 * GB} usage={usage(over.usedGB ?? 10)} usageLoading={false} expireAt={over.expireAt === undefined ? null : over.expireAt} resetPeriod="monthly" />
)
const bar = () => screen.getByRole('progressbar', { name: 'Period used' }).firstElementChild!

describe('UsageCard expiry', () => {
  it('says the days left in words, in the warning tone from 7 days down', () => {
    render(card({ expireAt: inDays(7) }))
    const left = screen.getByText('in 7 day(s)')
    expect(left.className).toContain('text-warning-ink')
  })

  it('keeps a distant expiry in the neutral tone', () => {
    render(card({ expireAt: inDays(30) }))
    expect(screen.getByText('in 30 day(s)').className).not.toContain('text-warning-ink')
  })

  it('says how long ago an expired account ended, in the danger tone', () => {
    render(card({ expireAt: inDays(-3) }))
    expect(screen.getByText("expired 3d ago").className).toContain('text-destructive')
  })
})

describe('UsageCard traffic bar', () => {
  it.each([[50, 'bg-brand'], [70, 'bg-warning-ink'], [89, 'bg-warning-ink'], [90, 'bg-destructive']])('draws %s%% used with %s', (gb, cls) => {
    render(card({ usedGB: gb }))
    expect(bar().className).toContain(cls)
  })
})
