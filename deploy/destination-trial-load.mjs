// The trial workload has its own evidence boundary; the two existing webhook
// phases and their functional/metric gates remain independently required.
import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationTrialLoadResults, isSafeDestinationDiagnostic, trialLoadNames } from './destination-acceptance.mjs'

const require = condition => { if (!condition) throw new Error('Trial load evidence invalid; private content withheld') }
const reasons = new Set(['node_dropped', 'node_unmatched', 'queue_full', 'collect_off', 'stale_collect_revision', 'ingest_error', 'unknown_subject', 'out_of_range', 'over_budget'])

export function checkDestinationTrialLoadMetrics(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 128 * 1024)
  const report = JSON.parse(input)
  const fields = ['schema_version', 'candidate', 'core_version', 'users', 'duration_ms', 'trial_windows', 'user_deliveries', 'trial_delivered', 'trial_allowed', 'trial_target_requests', 'trial_stored_rows', 'trial_stored_events', 'trial_over_budget_rows', 'trial_identified_rows', 'trial_budget_max_rows', 'block_windows', 'block_delivered', 'block_denied', 'block_stored_events', 'block_loss_rows', 'block_loss_events', 'transport_failures', 'late_delivered', 'control_successes', 'heartbeat_advances', 'trial_losses', 'samples', 'cpu_ticks_per_second']
  require(report && typeof report === 'object' && !Array.isArray(report) && Object.keys(report).sort().join() === fields.sort().join())
  require(report.schema_version === 1 && report.candidate === 'cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9' && report.core_version === '26.6.27')
  for (const name of fields.filter(name => !['candidate', 'core_version', 'trial_windows', 'user_deliveries', 'block_windows', 'trial_losses', 'samples'].includes(name))) require(Number.isSafeInteger(report[name]) && report[name] >= 0)
  require(report.users === 200 && report.duration_ms >= 300000 && report.duration_ms <= 305000)
  require(Array.isArray(report.user_deliveries) && report.user_deliveries.length === 200 && report.user_deliveries.every(n => Number.isSafeInteger(n) && n > 0))
  require(Array.isArray(report.trial_windows) && report.trial_windows.length === 300 && report.trial_windows.every(n => Number.isSafeInteger(n) && n >= 1000))
  require(Array.isArray(report.block_windows) && report.block_windows.length === 300 && report.block_windows.every(n => Number.isSafeInteger(n) && n >= 100))
  const trial = report.trial_windows.reduce((sum, n) => sum + n, 0), block = report.block_windows.reduce((sum, n) => sum + n, 0)
  require(Number.isSafeInteger(trial) && Number.isSafeInteger(block))
  require(report.user_deliveries.reduce((sum, n) => sum + n, 0) === trial)
  require(report.trial_delivered === trial && report.trial_allowed === trial && report.trial_target_requests === trial)
  require(report.trial_stored_rows > 0 && report.trial_stored_events === report.trial_stored_rows && report.trial_stored_events <= trial && report.trial_over_budget_rows > 0)
  require(Array.isArray(report.trial_losses) && report.trial_losses.length <= reasons.size)
  const seen = new Set()
  for (const loss of report.trial_losses) {
    require(loss && Object.keys(loss).sort().join() === 'events,reason,rows,unmatched')
    require(reasons.has(loss.reason) && !seen.has(loss.reason)); seen.add(loss.reason)
    require(['rows', 'events', 'unmatched'].every(key => Number.isSafeInteger(loss[key]) && loss[key] >= 0) && loss.rows + loss.events + loss.unmatched > 0)
  }
  require(report.trial_losses.find(loss => loss.reason === 'over_budget')?.rows === report.trial_over_budget_rows)
  // One distinct virtual site and one event per request makes row/event
  // reconciliation valid for this workload only, not for aggregated traffic.
  require(report.trial_stored_events + report.trial_losses.reduce((sum, loss) => sum + loss.rows + loss.events + loss.unmatched, 0) === trial)
  require(report.trial_identified_rows === 0 && report.trial_budget_max_rows > 0 && report.trial_budget_max_rows <= 20000)
  require(report.block_delivered === block && report.block_denied === block && report.block_stored_events === block)
  require(report.block_loss_rows === 0 && report.block_loss_events === 0 && report.transport_failures === 0 && report.late_delivered === 0)
  require(report.control_successes >= 300 && report.heartbeat_advances >= 30)
  require(report.cpu_ticks_per_second > 0 && report.cpu_ticks_per_second <= 10000)
  require(Array.isArray(report.samples) && report.samples.length >= 32 && report.samples.length <= 40)
  const first = report.samples[0], last = report.samples.at(-1)
  let previous
  for (const sample of report.samples) {
    require(sample && Object.keys(sample).sort().join() === 'agent_cpu_ticks,core_cpu_ticks,core_fds,core_goroutines,elapsed_ms')
    require(Object.values(sample).every(n => Number.isSafeInteger(n) && n >= 0) && sample.core_goroutines > 0 && sample.core_fds > 0)
    require(sample.core_goroutines <= first.core_goroutines + 8192 && sample.core_fds <= first.core_fds + 2048)
    if (previous) require(sample.elapsed_ms > previous.elapsed_ms && sample.elapsed_ms - previous.elapsed_ms <= 20000 && sample.agent_cpu_ticks >= previous.agent_cpu_ticks && sample.core_cpu_ticks >= previous.core_cpu_ticks)
    previous = sample
  }
  require(first.elapsed_ms <= 1000 && last.elapsed_ms >= report.duration_ms + 20000 && last.elapsed_ms <= report.duration_ms + 60000)
  require(last.core_goroutines <= first.core_goroutines + 128 && last.core_fds <= first.core_fds + 32)
  require(last.agent_cpu_ticks > first.agent_cpu_ticks && last.core_cpu_ticks > first.core_cpu_ticks)
  return { users: 200, seconds: 300, trial, block }
}

export function checkDestinationTrialLoadEvidence(results, metrics) {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  checkDestinationTrialLoadResults(results)
  for (const line of results.split(/\r?\n/).filter(line => line.trim())) {
    const event = JSON.parse(line)
    require(Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed', 'Diagnostic', 'Observed'].includes(key)))
    require(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app' && ['start', 'run', 'pass', 'output'].includes(event.Action))
    require(event.Test === undefined || trialLoadNames.includes(event.Test))
    require(event.Action !== 'run' || trialLoadNames.includes(event.Test))
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'output') require(isSafeDestinationDiagnostic(event))
    else require(event.Diagnostic === undefined && event.Observed === undefined)
  }
  return checkDestinationTrialLoadMetrics(metrics)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 4)
    const result = checkDestinationTrialLoadEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))
    console.log(`${result.users} users / ${result.seconds} seconds / ${result.trial} allowed trial and ${result.block} denied block deliveries passed`)
  } catch {
    console.error('Trial load evidence invalid; private content withheld')
    process.exitCode = 1
  }
}
