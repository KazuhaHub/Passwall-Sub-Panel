import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationLoadResults, isSafeDestinationDiagnostic, loadNames } from './destination-acceptance.mjs'

const candidate = '77912a6541eede8db9493d8be0cd6151cfded6e4'
const require = condition => { if (!condition) throw new Error('Native load evidence failed; private content withheld') }
const fields = (object, names) => require(object && typeof object === 'object' && !Array.isArray(object) && Object.keys(object).sort().join() === [...names].sort().join())
const count = value => Number.isSafeInteger(value) && value >= 0
const reasons = new Set(['node_dropped', 'node_unmatched', 'queue_full', 'collect_off', 'stale_collect_revision', 'ingest_error', 'unknown_subject', 'out_of_range', 'over_budget'])

export function checkDestinationLoadMetrics(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 128 * 1024)
  const report = JSON.parse(input)
  fields(report, ['schema_version', 'candidate', 'core_version', 'cpu_ticks_per_second', 'phases'])
  require(report.schema_version === 1 && report.candidate === candidate && report.core_version === '26.6.27')
  require(count(report.cpu_ticks_per_second) && report.cpu_ticks_per_second > 0 && report.cpu_ticks_per_second <= 10000)
  require(Array.isArray(report.phases) && report.phases.length === 2)
  let delivered = 0
  for (const [index, phase] of report.phases.entries()) {
    fields(phase, ['name', 'rules', 'duration_ms', 'attempted', 'delivered', 'blocked', 'transport_failures', 'target_requests', 'control_successes', 'heartbeat_advances', 'window_delivered', 'late_delivered', 'hit_rules', 'hit_events', 'losses', 'samples'])
    require(phase.name === ['single_rule', 'fifty_rules'][index] && phase.rules === [1, 50][index])
    for (const key of ['rules', 'duration_ms', 'attempted', 'delivered', 'blocked', 'transport_failures', 'target_requests', 'control_successes', 'heartbeat_advances', 'late_delivered', 'hit_rules', 'hit_events']) require(count(phase[key]))
    require(phase.duration_ms >= 300000 && phase.duration_ms <= 305000)
    require(Array.isArray(phase.window_delivered) && phase.window_delivered.length === 300 && phase.window_delivered.every(n => count(n) && n >= 1000))
    const total = phase.window_delivered.reduce((sum, n) => sum + n, 0)
    require(count(total) && phase.delivered === total && phase.attempted === total && phase.blocked === total)
    require(phase.transport_failures === 0 && phase.target_requests === 0 && phase.late_delivered === 0)
    require(phase.control_successes >= 300 && phase.heartbeat_advances >= 30 && phase.hit_rules === phase.rules && phase.hit_events > 0 && phase.hit_events <= phase.blocked)
    require(Array.isArray(phase.losses) && phase.losses.length <= reasons.size)
    const seen = new Set()
    for (const loss of phase.losses) {
      fields(loss, ['reason', 'rows', 'events', 'unmatched'])
      require(reasons.has(loss.reason) && !seen.has(loss.reason)); seen.add(loss.reason)
      require(['rows', 'events', 'unmatched'].every(key => count(loss[key])) && loss.rows + loss.events + loss.unmatched > 0)
    }
    require(phase.hit_events + phase.losses.reduce((sum, loss) => sum + loss.events + loss.unmatched, 0) === phase.blocked)
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
    delivered += phase.delivered
  }
  return { phases: 2, seconds: 600, delivered }
}

export function checkDestinationLoadEvidence(results, metrics) {
  checkDestinationLoadResults(results)
  for (const line of results.split(/\r?\n/).filter(line => line.trim())) {
    const event = JSON.parse(line)
    require(Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed', 'Diagnostic', 'Observed'].includes(key)))
    require(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app')
    require(['start', 'run', 'pass', 'output'].includes(event.Action))
    require(event.Test === undefined || loadNames.includes(event.Test))
    require(event.Action !== 'run' || loadNames.includes(event.Test))
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'output') require(isSafeDestinationDiagnostic(event))
    else require(event.Diagnostic === undefined && event.Observed === undefined)
  }
  return checkDestinationLoadMetrics(metrics)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 4)
    const result = checkDestinationLoadEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))
    console.log(`${result.phases} five-minute load phases / ${result.delivered} real blocked deliveries passed`)
  } catch {
    console.error('Native load evidence failed; private content withheld')
    process.exitCode = 1
  }
}
