import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { blockOverflowNames, checkDestinationBlockOverflowResults, isSafeDestinationDiagnostic } from './destination-acceptance.mjs'

const require = condition => { if (!condition) throw new Error('Block overflow evidence failed; private content withheld') }
const count = value => Number.isSafeInteger(value) && value >= 0
const fields = (value, keys) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join() === [...keys].sort().join())
const reasons = new Set(['node_dropped', 'node_unmatched', 'queue_full', 'collect_off', 'stale_collect_revision', 'ingest_error', 'unknown_subject', 'out_of_range', 'over_budget'])

export function checkDestinationBlockOverflowMetrics(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 128 * 1024)
  const report = JSON.parse(input)
  fields(report, ['schema_version', 'candidate', 'core_version', 'cpu_ticks_per_second', 'phases'])
  require(report.schema_version === 1 && report.candidate === '0614879497263defbda2bbcad6cdc81b3cdfd483' && report.core_version === '26.6.27')
  require(count(report.cpu_ticks_per_second) && report.cpu_ticks_per_second > 0 && report.cpu_ticks_per_second <= 10000)
  require(Array.isArray(report.phases) && report.phases.length === 1)
  const phase = report.phases[0]
  fields(phase, ['name', 'rules', 'duration_ms', 'attempted', 'delivered', 'blocked', 'transport_failures', 'target_requests', 'control_successes', 'heartbeat_advances', 'window_delivered', 'late_delivered', 'hit_rules', 'hit_events', 'budget_max_rows', 'losses', 'samples'])
  require(phase.name === 'block_overflow' && phase.rules === 1)
  for (const field of ['rules', 'duration_ms', 'attempted', 'delivered', 'blocked', 'transport_failures', 'target_requests', 'control_successes', 'heartbeat_advances', 'late_delivered', 'hit_rules', 'hit_events', 'budget_max_rows']) require(count(phase[field]))
  require(phase.duration_ms >= 300000 && phase.duration_ms <= 305000)
  require(Array.isArray(phase.window_delivered) && phase.window_delivered.length === 300 && phase.window_delivered.every(n => count(n) && n >= 1000))
  const total = phase.window_delivered.reduce((sum, n) => sum + n, 0)
  require(count(total) && phase.attempted === total && phase.delivered === total && phase.blocked === total)
  require(phase.transport_failures === 0 && phase.target_requests === 0 && phase.late_delivered === 0 && phase.control_successes >= 300 && phase.heartbeat_advances >= 30)
  require(phase.hit_rules === 1 && phase.hit_events > 0 && phase.hit_events <= total && phase.budget_max_rows === 20000)
  require(Array.isArray(phase.losses) && phase.losses.length <= reasons.size)
  const seen = new Set()
  for (const loss of phase.losses) {
    fields(loss, ['reason', 'rows', 'events', 'unmatched'])
    require(reasons.has(loss.reason) && !seen.has(loss.reason)); seen.add(loss.reason)
    require(['rows', 'events', 'unmatched'].every(key => count(loss[key])) && loss.rows + loss.events + loss.unmatched > 0)
  }
  require(phase.losses.some(loss => loss.reason === 'over_budget' && loss.rows > 0))
  // Only this fixture creates a unique destination key with count=1 for each
  // connection, so a discarded row represents one known event here.
  require(phase.hit_events + phase.losses.reduce((sum, loss) => sum + loss.rows + loss.events + loss.unmatched, 0) === total)
  require(Array.isArray(phase.samples) && phase.samples.length >= 32 && phase.samples.length <= 40)
  const first = phase.samples[0], last = phase.samples.at(-1)
  let previous
  for (const sample of phase.samples) {
    fields(sample, ['elapsed_ms', 'core_goroutines', 'core_fds', 'agent_cpu_ticks', 'core_cpu_ticks'])
    require(Object.values(sample).every(count) && sample.core_goroutines > 0 && sample.core_fds > 0)
    require(sample.core_goroutines <= first.core_goroutines + 8192 && sample.core_fds <= first.core_fds + 2048)
    if (previous) require(sample.elapsed_ms > previous.elapsed_ms && sample.elapsed_ms - previous.elapsed_ms <= 20000 && sample.agent_cpu_ticks >= previous.agent_cpu_ticks && sample.core_cpu_ticks >= previous.core_cpu_ticks)
    previous = sample
  }
  require(first.elapsed_ms <= 1000 && last.elapsed_ms >= phase.duration_ms + 20000 && last.elapsed_ms <= phase.duration_ms + 60000)
  require(last.core_goroutines <= first.core_goroutines + 128 && last.core_fds <= first.core_fds + 32)
  require(last.agent_cpu_ticks > first.agent_cpu_ticks && last.core_cpu_ticks > first.core_cpu_ticks)
  return { seconds: 300, delivered: total, stored: phase.hit_events }
}

export function checkDestinationBlockOverflowEvidence(results, metrics) {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  checkDestinationBlockOverflowResults(results)
  for (const line of results.split(/\r?\n/).filter(line => line.trim())) {
    const event = JSON.parse(line)
    require(Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed', 'Diagnostic', 'Observed'].includes(key)))
    require(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app' && ['start', 'run', 'pass', 'output'].includes(event.Action))
    require(event.Test === undefined || blockOverflowNames.includes(event.Test))
    require(event.Action !== 'run' || blockOverflowNames.includes(event.Test))
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'output') require(isSafeDestinationDiagnostic(event))
    else require(event.Diagnostic === undefined && event.Observed === undefined)
  }
  return checkDestinationBlockOverflowMetrics(metrics)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 4)
    const result = checkDestinationBlockOverflowEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))
    console.log(`${result.seconds} seconds / ${result.delivered} real block deliveries / ${result.stored} persisted events with reconciled overflow passed`)
  } catch {
    console.error('Block overflow evidence failed; private content withheld')
    process.exitCode = 1
  }
}
