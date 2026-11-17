// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mount } from '@/test/adminSaveHarness'
import ProbeQualityChip from './ProbeQualityChip'

const q = (over = {}) => ({ state: 'ok' as const, median_rtt_ms: 42.4, loss_percent: 1.2, withheld: false, ...over })

describe('ProbeQualityChip', () => {
  it.each(['ok', 'degraded', 'down'] as const)('shows %s with rtt and loss', state => {
    mount(<ProbeQualityChip probe={q({ state })} />)
    const chip = screen.getByTestId('probe-quality')
    expect(chip.dataset.state).toBe(state)
    expect(chip.textContent).toContain(`admin:nodes.probe.state_${state}`)
    expect(chip.textContent).toContain('admin:nodes.probe.rtt')
    expect(chip.textContent).toContain('admin:nodes.probe.loss')
    expect(screen.queryByTestId('probe-quality-withheld')).toBeNull()
  })
  it('shows no data and no numbers when unknown', () => {
    mount(<ProbeQualityChip probe={q({ state: 'unknown', median_rtt_ms: 0, loss_percent: 0 })} />)
    const chip = screen.getByTestId('probe-quality')
    expect(chip.textContent).toBe('admin:nodes.probe.state_unknown')
  })
  it('marks withheld from either flag', () => {
    const { unmount } = mount(<ProbeQualityChip probe={q({ state: 'down' })} withheld />)
    expect(screen.getByTestId('probe-quality-withheld')).toBeTruthy()
    unmount()
    mount(<ProbeQualityChip probe={q({ state: 'down', withheld: true })} />)
    expect(screen.getByTestId('probe-quality-withheld')).toBeTruthy()
  })
  it('renders nothing without a probe', () => {
    mount(<ProbeQualityChip />)
    expect(screen.queryByTestId('probe-quality')).toBeNull()
  })
})
