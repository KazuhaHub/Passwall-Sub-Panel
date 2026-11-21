import { describe, expect, it } from 'vitest'
import { resolveTrendWindow } from './range'

const FOREVER = Number.POSITIVE_INFINITY

describe('resolveTrendWindow', () => {
  it('passes a valid pair through and offers every range under unlimited retention', () => {
    const w = resolveTrendWindow(30, 'day', FOREVER)
    expect(w).toMatchObject({ rangeDays: 30, period: 'day' })
    expect(w.rangeOptions).toEqual([1, 7, 30, 90, 180, 365])
  })

  it('forces hour buckets for today, and drops hour for a month or more', () => {
    expect(resolveTrendWindow(1, 'week', FOREVER).period).toBe('hour')
    expect(resolveTrendWindow(30, 'hour', FOREVER).period).toBe('day')
    expect(resolveTrendWindow(7, 'hour', FOREVER).period).toBe('hour')
  })

  it('hides ranges beyond retention but always keeps today', () => {
    const w = resolveTrendWindow(7, 'day', 30)
    expect(w.rangeOptions).toEqual([1, 7, 30])
    expect(resolveTrendWindow(7, 'day', 3).rangeOptions).toEqual([1])
  })

  it('clamps a requested range that retention no longer offers to the largest one that is', () => {
    expect(resolveTrendWindow(365, 'day', 30).rangeDays).toBe(30)
    // Retention shorter than a week leaves only today, and today is hourly.
    expect(resolveTrendWindow(30, 'day', 3)).toMatchObject({ rangeDays: 1, period: 'hour' })
  })

  it('caps the ranges offered for hour buckets at a week', () => {
    expect(resolveTrendWindow(7, 'hour', FOREVER).rangeOptions).toEqual([1, 7])
  })
})
