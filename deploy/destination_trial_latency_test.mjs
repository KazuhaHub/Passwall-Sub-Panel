import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const bounds = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 0]
const names = ['fixture_destination_sync_handler_ms', 'psp_node_control_sync_ms', 'psp_poll_ms', 'psp_poll_stage_ms{stage=sink_flush}', 'psp_dest_audit_begin_ms{kind=block}', 'psp_dest_audit_begin_ms{kind=trial}', 'psp_dest_audit_chunk_ms{kind=block}', 'psp_dest_audit_chunk_ms{kind=trial}']
const report = () => ({ schema_version: 1, window_ms: 300000, histograms: names.map((name, i) => {
  const count = i < 2 ? 150 : i < 4 ? 5 : i < 6 ? 20 : 0
  return { name, count, buckets: bounds.map((upper_ms, j) => ({ upper_ms, inf: j === bounds.length - 1, count: j < 2 ? 0 : count })) }
}) })

test('latency evidence requires same-window control, real poll writes and both ingestion kinds', async () => {
  const { checkDestinationTrialLatencyMetrics } = await import('./destination-trial-latency.mjs')
  checkDestinationTrialLatencyMetrics(JSON.stringify(report()), 300000)
  for (const change of [
    r => { r.schema_version = 2 }, r => { r.window_ms-- }, r => { r.histograms.pop() },
    r => { r.histograms[1] = r.histograms[0] }, r => { r.histograms[0].count = 99 },
    r => { r.histograms[2].count = 3 }, r => { r.histograms[3].count = 0 },
    r => { r.histograms[4].count = 0 }, r => { r.histograms[5].count = 0 },
    r => { r.histograms[0].buckets[1].count = 151 },
    r => { r.histograms[0].buckets[3].count = 100 },
    r => { r.histograms[0].buckets.at(-1).count-- },
    r => { r.histograms[0].buckets[0].upper_ms = 0.01 },
    r => { r.histograms[0].buckets[1].inf = true },
    r => { r.histograms[0].sum_ms = 123 }, r => { r.agent_id = 'private' },
    r => { r.histograms[0].name = 'private' },
  ]) { const value = report(); change(value); assert.throws(() => checkDestinationTrialLatencyMetrics(JSON.stringify(value), 300000)) }
  assert.throws(() => checkDestinationTrialLatencyMetrics(JSON.stringify(report()), 299999))
})

test('the hosted trial writes independent latency evidence and retains the original delivery gate', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const text of ['destination-trial-load-latency.json', 'deploy/destination-trial-latency.mjs', 'deploy/destination_trial_latency_test.mjs', 'deploy/destination-trial-load.mjs']) assert(workflow.includes(text), text)
  const driver = readFileSync(new URL('../internal/app/destination_native_trial_load_test.go', import.meta.url), 'utf8')
  assert(driver.indexOf('latencyBefore := nativeTrialLatencySnapshot(t)') < driver.indexOf('start := time.Now()'))
  assert(driver.indexOf('nativeWriteTrialLatency(t, evidence, m.Duration, latencyBefore)') < driver.indexOf('for range 3'))
})
