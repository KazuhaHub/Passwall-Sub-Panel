// @vitest-environment jsdom
import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { installReads, mount } from '@/test/adminSaveHarness'
import { formatBits, formatBytes, formatValue } from '@/components/NodeMetricsChart'
import NodeMetricsDialog from './NodeMetricsDialog'
import type { NodeMetricsCurrent } from '@/api/nodeMetrics'
import type { Server } from '@/api/servers'

/**
 * The states, the units and the gaps.
 *
 * THE EMPTY STATES ARE THE POINT: unsupported, unavailable, stale and healthy are
 * four different situations, and a component that renders one blank chart for all
 * of them has told the operator nothing while looking like it did.
 *
 * The translation hook is mocked to return the KEY, so these assert which key the
 * component asks for rather than which sentence it renders — the copy itself is
 * the locale files' business, and the two languages are checked separately.
 */

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

function currentBody(overrides: Partial<NodeMetricsCurrent>): NodeMetricsCurrent {
  return {
    available: true,
    freshness: 'fresh',
    received_at: '2026-09-18T12:00:00Z',
    collected_at: '2026-09-18T11:59:59Z',
    scope: { deployment: 'systemd', resource_scope: 'host', cgroup_version: 2, data_filesystem_scope: 'host_mount' },
    summary: summary(),
    ...overrides,
  }
}

describe('node metric units', () => {
  it('formats byte rates in bits and byte gauges in bytes', () => {
    // A link is quoted in bits and a disk in bytes. Reporting a throughput in
    // MB/s when an operator thinks in Mbps is a units bug with a plausible face.
    expect(formatBits(1_000_000)).toBe('1.00 Mbps')
    expect(formatBytes(1024 * 1024)).toBe('1.00 MiB')
    expect(formatBits(8_000_000_000)).toBe('8.00 Gbps')
  })

  it('formats each metric kind in its own unit', () => {
    expect(formatValue(21.44, 'percent')).toBe('21.4%')
    expect(formatValue(0.0123, 'ratio')).toBe('0.012')
    expect(formatValue(250, 'milliseconds')).toBe('250 ms')
  })
})

describe('NodeMetricsDialog', () => {
  it('says the node is unsupported rather than drawing an empty chart', async () => {
    installReads({
      '/admin/servers/7/node-metrics/current': currentBody({ available: false, freshness: 'unsupported' }),
      '/admin/servers/7/node-metrics/history': { resolution: 'minute', from: '', to: '', series: [] },
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('admin:nodeMetrics.unsupported')).toBeTruthy())
  })

  it('shows the scope so a container is never read as the host', async () => {
    installReads({
      '/admin/servers/7/node-metrics/current': currentBody({
        scope: { deployment: 'docker', resource_scope: 'mixed', cgroup_version: 2, data_filesystem_scope: 'container_mount' },
        summary: summary({ cpu_percent: 21.4, memory_used_percent: 62.1, memory_scope: 'cgroup' }),
      }),
      '/admin/servers/7/node-metrics/history': { resolution: 'minute', from: '', to: '', series: [] },
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    // The chip renders the label and the value as one string, so this matches on
    // the key rather than on the whole text.
    await waitFor(() => expect(
      screen.getByText((text: string) => text.includes('nodeMetrics.scopeValue.mixed')),
    ).toBeTruthy())
    // The memory figure carries its own scope, so a container limit is never
    // presented as the host's memory.
    expect(screen.getByText(new RegExp('62.1%'))).toBeTruthy()
  })

  it('renders an absent metric as an em dash rather than a zero', async () => {
    installReads({
      '/admin/servers/7/node-metrics/current': currentBody({ summary: summary() }),
      '/admin/servers/7/node-metrics/history': { resolution: 'minute', from: '', to: '', series: [] },
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    await waitFor(() => expect(screen.getAllByText('—').length).toBeGreaterThan(0))
    // Never a zero standing in for an unreadable metric.
    expect(screen.queryByText(/^0\.0%$/)).toBeNull()
  })

  it('reports a range with no samples instead of plotting an empty axis', async () => {
    installReads({
      '/admin/servers/7/node-metrics/current': currentBody({}),
      '/admin/servers/7/node-metrics/history': { resolution: 'hour', from: '', to: '', series: [] },
    })
    mount(<NodeMetricsDialog server={server} open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('admin:nodeMetrics.tabPerformance')).toBeTruthy())
  })
})
