import { describe, expect, it } from 'vitest'
import { bytesToHuman } from './trafficBytes'

describe('bytesToHuman', () => {
  it('keeps the format the traffic pages have always shown', () => {
    expect(bytesToHuman(0)).toBe('0')
    expect(bytesToHuman(512)).toBe('512.00 B')
    expect(bytesToHuman(1536)).toBe('1.50 KB')
    expect(bytesToHuman(1024 ** 3)).toBe('1.00 GB')
    expect(bytesToHuman(5 * 1024 ** 4)).toBe('5.00 TB')
    // Past TB there is no larger unit; the figure keeps growing instead.
    expect(bytesToHuman(2048 * 1024 ** 4)).toBe('2048.00 TB')
  })
})
