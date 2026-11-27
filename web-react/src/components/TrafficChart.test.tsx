// @vitest-environment jsdom
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import TrafficChart from './TrafficChart'

// ECharts draws on a canvas, which jsdom does not have; what is tested here is what surrounds the picture.
const setOption = vi.fn()
vi.mock('echarts/core', () => ({
  use: () => {},
  init: () => ({ setOption, resize: () => {}, dispose: () => {} }),
}))
vi.mock('echarts/charts', () => ({ LineChart: {} }))
vi.mock('echarts/components', () => ({ GridComponent: {}, TooltipComponent: {}, LegendComponent: {} }))
vi.mock('echarts/renderers', () => ({ CanvasRenderer: {} }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => (o ? `${k} ${Object.values(o).join(' ')}` : k),
    i18n: { language: 'en-US' },
  }),
}))

afterEach(() => { cleanup(); vi.clearAllMocks() })

const items = [
  { date: '2026-11-01', up_bytes: 1024, down_bytes: 2048, total_bytes: 3072 },
  { date: '2026-11-02', up_bytes: 0, down_bytes: 0, total_bytes: 0 },
]

describe('TrafficChart', () => {
  it('describes the chart in words and offers the same numbers as a table', () => {
    render(<TrafficChart items={items} />)

    // The canvas itself says nothing to a screen reader, so the box carries a summary.
    expect(screen.getByRole('img').getAttribute('aria-label')).toContain('3.00 KB')
    const table = screen.getByRole('table', { hidden: true })
    const rows = within(table).getAllByRole('row', { hidden: true })
    expect(rows).toHaveLength(3)
    // Same byte format as every other traffic figure on the page.
    expect(within(rows[1]).getByText('1.00 KB', { exact: true })).toBeTruthy()
    expect(within(rows[2]).getAllByText('0')).toHaveLength(3)
  })

  it('tells the three series apart by line pattern as well as colour, with no always-on markers', () => {
    render(<TrafficChart items={items} />)

    const series = setOption.mock.calls[0][0].series as { lineStyle: { type: string }; showSymbol: boolean }[]
    expect(new Set(series.map(s => s.lineStyle.type)).size).toBe(3)
    expect(series.every(s => s.showSymbol === false)).toBe(true)
    expect(setOption.mock.calls[0][0].legend.show).toBe(false)
  })

  it('says there is no traffic instead of drawing empty axes', () => {
    render(<TrafficChart items={[]} />)

    expect(screen.getByText('traffic.chart.empty')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
    expect(setOption).not.toHaveBeenCalled()
  })
})
