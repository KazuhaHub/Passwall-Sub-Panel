import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { moduleNodeCandidate, checkDestinationGlobalBootstrapResults } from './destination-acceptance.mjs'

export function checkGlobalBootstrapEvidence(tests, metrics, database = 'sqlite') {
  const need = value => { if (!value) throw new Error('Global bootstrap evidence invalid; private content withheld') }
  checkDestinationGlobalBootstrapResults(tests)
  need(typeof tests === 'string' && Buffer.byteLength(tests) <= 128 * 1024)
  for (const line of tests.trim().split(/\r?\n/)) {
    let event
    try { event = JSON.parse(line) } catch { need(false) }
    need(event && Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed'].includes(key)))
    need(['start', 'run', 'pass'].includes(event.Action) && event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app')
    need(event.Test === undefined || event.Test === 'TestNativeDestinationGlobalCohortBootstrap')
    need(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
  }
  need(typeof metrics === 'string' && Buffer.byteLength(metrics) <= 4096)
  let report
  try { report = JSON.parse(metrics) } catch { need(false) }
  const fields = ['schema_version', 'candidate', 'core_version', 'database', 'requested_owners', 'bootstrap_stage', 'started_native_processes', 'unique_live_parents', 'unique_live_cores', 'independent_fresh_states', 'public_cache_copies', 'complete_stream_pairs', 'nonce_controls', 'shared_users', 'pressure_complete']
  need(report && Object.keys(report).sort().join() === fields.sort().join())
  need(['sqlite', 'mysql', 'postgres'].includes(database) && report.database === database)
  need(report.schema_version === 1 && report.candidate === moduleNodeCandidate.commit && report.core_version === '26.6.27')
  for (const field of ['requested_owners', 'started_native_processes', 'unique_live_parents', 'unique_live_cores', 'independent_fresh_states', 'complete_stream_pairs', 'nonce_controls']) need(report[field] === 65)
  need(report.bootstrap_stage === 9)
  need(report.public_cache_copies === 64 && report.shared_users === 1 && report.pressure_complete === 0)
  return { actual_native_owners: 65, actual_cores: 65, independent_states: 65, public_cache_copies: 64, shared_users: 1, pressure_complete: false }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv.length !== 4 && process.argv.length !== 5) throw new Error()
    console.log(JSON.stringify(checkGlobalBootstrapEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'), process.argv[4])))
  } catch {
    console.error('Global bootstrap evidence invalid; private content withheld')
    process.exitCode = 1
  }
}
