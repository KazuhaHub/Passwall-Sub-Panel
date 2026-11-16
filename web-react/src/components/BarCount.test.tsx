// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { createAppTheme } from '@/theme'
import BarCount, { barPercent } from './BarCount'

afterEach(cleanup)

describe('barPercent', () => {
  it('is relative to the largest value on the page', () => {
    expect(barPercent(50, 100)).toBe(50)
    expect(barPercent(100, 100)).toBe(100)
  })
  it('keeps a small non-zero value visible and a zero one empty', () => {
    expect(barPercent(1, 10000)).toBe(2)
    expect(barPercent(0, 100)).toBe(0)
    expect(barPercent(5, 0)).toBe(0)
  })
})

describe('BarCount', () => {
  it('writes the number with its grouping beside the bar', () => {
    render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><BarCount value={2210} max={4000} /></ThemeProvider>)
    expect(screen.getByText('2,210')).toBeTruthy()
    expect(screen.getByTestId('bar-fill').getAttribute('style')).toContain('55%')
  })
})
