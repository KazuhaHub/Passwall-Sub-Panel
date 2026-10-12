import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationTrialLoadEvidence } from './destination-trial-load.mjs'

const require = condition => { if (!condition) throw new Error('Trial latency evidence invalid; private content withheld') }
const fields = (value, keys) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join() === [...keys].sort().join())
const count = value => Number.isSafeInteger(value) && value >= 0
const bounds = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 0]
const names = ['fixture_destination_sync_handler_ms', 'psp_node_control_sync_ms', 'psp_poll_ms', 'psp_poll_stage_ms{stage=sink_flush}', 'psp_dest_audit_begin_ms{kind=block}', 'psp_dest_audit_begin_ms{kind=trial}', 'psp_dest_audit_chunk_ms{kind=block}', 'psp_dest_audit_chunk_ms{kind=trial}']

export function checkDestinationTrialLatencyMetrics(input, duration) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 32 * 1024)
  const value = JSON.parse(input)
  fields(value, ['schema_version', 'window_ms', 'histograms'])
  require(value.schema_version === 1 && count(duration) && duration >= 300000 && duration <= 305000 && value.window_ms === duration)
  require(Array.isArray(value.histograms) && value.histograms.length === names.length)
  const summary = []
  for (const [i, histogram] of value.histograms.entries()) {
    fields(histogram, ['name', 'count', 'buckets'])
    require(histogram.name === names[i] && count(histogram.count))
    require(histogram.count >= (i < 2 ? 100 : i < 4 ? 4 : i < 6 ? 1 : 0))
    require(Array.isArray(histogram.buckets) && histogram.buckets.length === bounds.length)
    let previous = 0
    for (const [j, bucket] of histogram.buckets.entries()) {
      fields(bucket, ['upper_ms', 'inf', 'count'])
      require(bucket.upper_ms === bounds[j] && bucket.inf === (j === bounds.length - 1))
      require(count(bucket.count) && bucket.count >= previous && bucket.count <= histogram.count)
      previous = bucket.count
    }
    require(previous === histogram.count)
    const p95 = histogram.count ? histogram.buckets.find(b => b.count >= Math.ceil(histogram.count * 0.95)) : undefined
    summary.push({ name: histogram.name, count: histogram.count, p95_upper_ms: p95 && !p95.inf ? p95.upper_ms : null })
  }
  return summary
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 5)
    const results = readFileSync(process.argv[2], 'utf8'), metrics = readFileSync(process.argv[3], 'utf8')
    checkDestinationTrialLoadEvidence(results, metrics)
    const summary = checkDestinationTrialLatencyMetrics(readFileSync(process.argv[4], 'utf8'), JSON.parse(metrics).duration_ms)
    console.log(JSON.stringify(summary))
  } catch {
    console.error('Trial latency evidence failed; private content withheld')
    process.exitCode = 1
  }
}
