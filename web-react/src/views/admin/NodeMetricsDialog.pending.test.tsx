// @vitest-environment jsdom
import type { ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads } from '@/test/adminSaveHarness'
import NodeMetricsDialog from './NodeMetricsDialog'
import type { NodeMetricsCurrent } from '@/api/nodeMetrics'
import type { Server } from '@/api/servers'

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

// This dialog's data-load effect runs for the first time at mount (`open` is
// always true here), which is exactly when React's dev-only StrictMode
// double-invokes an effect to check its cleanup — so adminSaveHarness's own
// StrictMode-wrapped `mount` would double the very first fetch and throw off
// every call-count assertion below. Repeats its wrapper stack minus
// <StrictMode>.
function mount(page: ReactElement) {
  const client = makeTestQueryClient()
  return render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <QueryClientProvider client={client}>{page}</QueryClientProvider>
      </ThemeProvider>
    </MemoryRouter>,
  )
}

/**
 * SLOW-NETWORK FEEDBACK for the metrics dialog: switching the range chip or
 * the Diagnostics tab re-fetches over data that is already on screen, and a
 * long-running Refresh keeps the button disabled with no spinner. These pin
 * that all three now show progress instead of going silent.
 */

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const server: Server = {
  id: 7, name: 'node-1', panel_type: 'psp', capabilities: [], url: 'psp://agt_1',
  has_api_token: false, has_password: false, auth_method: '', insecure_https: false,
}

function summary(overrides: Partial<NodeMetricsCurrent['summary']> = {}) {
  const blank = {
    cpu_percent: null, cpu_scope: null, system_cpu_percent: null, cgroup_cpu_cores_percent: null,
    cgroup_cpu_quota_percent: null, cgroup_cpu_capacity_percent: null,
    cgroup_cpu_throttled_period_percent: null, memory_used_percent: null, memory_scope: null,
    disk_used_percent: null, rx_bps: null, tx_bps: null, tcp_retrans_percent: null, core_rss_bytes: null,
  }
  return { ...blank, ...overrides }
}

function currentBody(overrides: Partial<NodeMetricsCurrent> = {}): NodeMetricsCurrent {
  return {
    available: true,
    freshness: 'fresh',
    received_at: '2026-09-18T12:00:00Z',
    collected_at: '2026-09-18T11:59:59Z',
    summary: summary({ cpu_percent: 41.2 }),
    ...overrides,
  }
}

const emptyHistory = { resolution: 'minute', from: '', to: '', series: [] }

describe('NodeMetricsDialog range switch', () => {
  it('keeps the stale chart visible and shows progress instead of going silent while a new range loads', async () => {
    let currentCalls = 0
    const gate = deferred<{ data: NodeMetricsCurrent }>()
    api.get.mockImplementation((url: string) => {
      if (url === '/admin/servers/7/node-metrics/current') {
        currentCalls += 1
        return currentCalls === 1 ? Promise.resolve({ data: currentBody() }) : gate.promise
      }
      if (url === '/admin/servers/7/node-metrics/history') return Promise.resolve({ data: emptyHistory })
      throw new Error(`Unexpected GET ${url}`)
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)

    // First load settles: the CPU figure from the initial fetch is on screen.
    await waitFor(() => expect(screen.getByText('41.2%')).toBeTruthy())
    expect(screen.queryAllByRole('progressbar')).toHaveLength(0)

    // Switching range re-fetches; the stale 41.2% must stay up rather than
    // blanking, and something has to say a fetch is running. (Both the
    // LinearProgress above the chart and the active chip's own spinner icon
    // carry role="progressbar", hence the plural query.)
    fireEvent.click(screen.getByText('24h'))
    await waitFor(() => expect(currentCalls).toBe(2))
    expect(screen.getAllByRole('progressbar').length).toBeGreaterThan(0)
    expect(screen.getByText('41.2%')).toBeTruthy()

    // A second range click while the first is still in flight must not start
    // a third overlapping request.
    fireEvent.click(screen.getByText('7d'))
    expect(currentCalls).toBe(2)

    gate.resolve({ data: currentBody({ summary: summary({ cpu_percent: 55.5 }) }) })
    await waitFor(() => expect(screen.getByText('55.5%')).toBeTruthy())
    expect(screen.queryAllByRole('progressbar')).toHaveLength(0)
  })
})

describe('NodeMetricsDialog Diagnostics tab', () => {
  it('shows a loading indicator instead of leaving the cards on the dash while node-health loads', async () => {
    const gate = deferred<{ data: unknown }>()
    api.get.mockImplementation((url: string) => {
      if (url === '/admin/servers/7/node-metrics/current') return Promise.resolve({ data: currentBody() })
      if (url === '/admin/servers/7/node-metrics/history') return Promise.resolve({ data: emptyHistory })
      if (url === '/admin/servers/7/node-health') return gate.promise
      throw new Error(`Unexpected GET ${url}`)
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('41.2%')).toBeTruthy())

    fireEvent.click(screen.getByRole('tab', { name: 'admin:nodeMetrics.tabDiagnostics' }))

    expect(await screen.findByText('common:status.loading')).toBeTruthy()
    // The dash placeholder must not be the only thing shown while it loads —
    // that reads as "unhealthy" or "unknown" rather than "still fetching".
    expect(screen.getByRole('progressbar')).toBeTruthy()

    gate.resolve({ data: { freshness: 'fresh', resource_health: 'healthy', findings: [] } })
    await waitFor(() => expect(screen.queryByText('common:status.loading')).toBeNull())
    expect(screen.getByText('healthy')).toBeTruthy()
  })
})

describe('NodeMetricsDialog manual refresh', () => {
  it('shows a spinner on Refresh now for the whole request and ignores a second click', async () => {
    installReads({
      '/admin/servers/7/node-metrics/current': currentBody(),
      '/admin/servers/7/node-metrics/history': emptyHistory,
    })
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation(() => gate.promise)
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('41.2%')).toBeTruthy())

    const refreshButton = screen.getByRole('button', { name: 'admin:nodeMetrics.refresh' })
    fireEvent.click(refreshButton)

    await waitFor(() => expect((refreshButton as HTMLButtonElement).disabled).toBe(true))
    expect(screen.getByRole('progressbar')).toBeTruthy()

    fireEvent.click(refreshButton)
    expect(api.post).toHaveBeenCalledTimes(1)

    // The refresh then polls node-metrics/current on a real 2s cadence for up
    // to 30s looking for a changed sample — unrelated to the busy indicator
    // this test pins, so it stops here rather than chasing that loop closed.
  })
})
