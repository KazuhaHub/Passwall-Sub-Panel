import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationIdleCostResults, moduleNodeCandidate } from './destination-acceptance.mjs'

const require = value => { if (!value) throw new Error('Idle cost evidence invalid; private content withheld') }
const count = value => Number.isSafeInteger(value) && value >= 0
const fields = (value, names) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join() === [...names].sort().join())

export function checkIdleCostEvidence(results, metrics) {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024 && typeof metrics === 'string' && Buffer.byteLength(metrics) <= 16 * 1024)
  checkDestinationIdleCostResults(results)
  for (const line of results.split(/\r?\n/).filter(Boolean)) {
    let event
    try { event = JSON.parse(line) } catch { require(false) }
    require(event && Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed'].includes(key)))
    require(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app' && ['start', 'run', 'pass'].includes(event.Action))
    require(event.Test === undefined || event.Test === 'TestNativeDestinationIdleCost')
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
  }
  let report
  try { report = JSON.parse(metrics) } catch { require(false) }
  fields(report, ['schema_version', 'candidate', 'core_version', 'published_domains', 'applied_domains', 'core_domains', 'cpu_ticks_per_second', 'control_successes', 'tracer_bound', 'cycles'])
  require(report.schema_version === 1 && report.candidate === moduleNodeCandidate.commit && report.core_version === '26.6.27')
  require(report.published_domains === 50000 && report.applied_domains === 50000 && report.core_domains === 50000 && report.control_successes === 2 && report.tracer_bound === 1)
  require(count(report.cpu_ticks_per_second) && report.cpu_ticks_per_second > 0 && report.cpu_ticks_per_second <= 1000000)
  require(Array.isArray(report.cycles) && report.cycles.length === 10)
  for (const [i, cycle] of report.cycles.entries()) {
    fields(cycle, ['phase', 'index', 'elapsed_ms', 'agent_cpu_ticks', 'core_cpu_ticks', 'accepted_syncs', 'state_writes'])
    require(cycle.phase === (i < 5 ? 'plain' : 'traced') && cycle.index === i % 5 + 1 && cycle.accepted_syncs === 1)
    require(count(cycle.elapsed_ms) && cycle.elapsed_ms > 0 && cycle.elapsed_ms <= 30000 && count(cycle.agent_cpu_ticks) && count(cycle.core_cpu_ticks))
    if (i < 5) require(cycle.state_writes === null)
    else {
      fields(cycle.state_writes, ['db_bytes', 'wal_bytes', 'journal_bytes', 'write_calls', 'sync_calls'])
      require(Object.values(cycle.state_writes).every(count))
      const written = cycle.state_writes.db_bytes + cycle.state_writes.wal_bytes + cycle.state_writes.journal_bytes
      require(count(written) && (cycle.state_writes.write_calls > 0 || written === 0))
    }
  }
  const mean = values => values.reduce((sum, value) => sum + value, 0) / values.length
  const plain = report.cycles.slice(0, 5), traced = report.cycles.slice(5)
  const cpu = (cycles, field) => mean(cycles.map(c => c[field] * 1000 / report.cpu_ticks_per_second))
  return { domains: 50000, plain_agent_cpu_ms_mean: cpu(plain, 'agent_cpu_ticks'), plain_core_cpu_ms_mean: cpu(plain, 'core_cpu_ticks'), traced_agent_cpu_ms_mean: cpu(traced, 'agent_cpu_ticks'), traced_state_bytes_mean: mean(traced.map(c => c.state_writes.db_bytes + c.state_writes.wal_bytes + c.state_writes.journal_bytes)), logical_syscall_bytes: true }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 4)
    console.log(JSON.stringify(checkIdleCostEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))))
  } catch {
    console.error('Idle cost evidence invalid; private content withheld')
    process.exitCode = 1
  }
}
