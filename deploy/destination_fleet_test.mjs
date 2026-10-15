import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

test('fleet setup failure retains only closed completion booleans before the first scenario', async () => {
  const source = await import('./destination-acceptance.mjs')
  for (const name of ['cohort', 'candidates', 'database', 'session', 'cadence', 'principals', 'peers', 'server', 'daemons', 'membership']) {
    for (const Observed of [true, false]) {
      const Diagnostic = `fleet_setup_${name}`
      const retained = source.sanitizeDestinationResults(JSON.stringify({ Action: 'output', Output: `private credential: native diagnostic: ${Diagnostic}=${Observed}\n` }))
      assert.deepEqual(JSON.parse(retained), { Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Diagnostic, Observed })
      assert(source.isSafeDestinationDiagnostic({ Diagnostic, Observed }))
    }
  }
  for (const text of ['secret=true', 'database=private', 'session=123', 'membership=true private']) assert.equal(source.sanitizeDestinationResults(JSON.stringify({ Action: 'output', Output: `native diagnostic: fleet_setup_${text}\n` })), '\n')
})
import { test } from 'node:test'

const stages = ['native_ready', 'native_publication', 'native_pause', 'native_resume', 'third_party_ready', 'allowlist_trial', 'allowlist_enforce', 'one_peer_offline', 'allowlist_pause', 'allowlist_reopen']
test('fleet evidence requires all real cohort stages and rejects missing, duplicate, failed or skipped execution', async () => {
  const source = await import('./destination-acceptance.mjs')
  assert.deepEqual(source.fleetCases, stages)
  const names = ['TestNativeDestinationFleet', ...stages.map(name => `TestNativeDestinationFleet/${name}`)]
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const events = names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }])
  events.push({ Action: 'pass', Package: pkg })
  const input = rows => rows.map(event => JSON.stringify(event)).join('\n') + '\n'
  source.checkFleetResults(input(events))
  assert.throws(() => source.checkFleetResults(input(events.filter(event => event.Test !== names[5]))))
  for (const Action of ['run', 'pass', 'fail', 'skip']) assert.throws(() => source.checkFleetResults(input([...events, { Action, Package: pkg, Test: names[1] }])))
  const privateInput = input([...events, { Action: 'output', Package: pkg, Test: names[5], Output: 'private minted token or row body' }])
  const safe = source.sanitizeDestinationResults(privateInput)
  assert(!safe.includes('private'))
  source.checkFleetResults(safe)
  for (const Diagnostic of ['fleet_peer_0_observation', 'fleet_peer_1_observation', 'fleet_peer_0_streams', 'fleet_peer_1_streams', 'fleet_peer_0_policy', 'fleet_peer_1_policy', 'fleet_backend_xui_ready', 'fleet_backend_sui_ready', 'fleet_backend_xui_pull', 'fleet_backend_sui_pull', 'fleet_backend_auth_error', 'fleet_backend_timeout', 'fleet_backend_registry_error', 'fleet_backend_sql_error']) {
    for (const Observed of [true, false]) assert(source.isSafeDestinationDiagnostic({ Diagnostic, Observed }))
    assert(!source.isSafeDestinationDiagnostic({ Diagnostic, Observed: 'private credential' }))
  }
  assert(!source.isSafeDestinationDiagnostic({ Diagnostic: 'fleet_peer_8_policy', Observed: true }))
  for (const Diagnostic of ['fault_body_present', 'fault_body_valid', 'fault_body_digest', 'fault_body_rules', 'fault_body_collect', 'fleet_runtime_present', 'fleet_prior_present', 'fleet_kind_matches', 'fleet_generation_matches', 'fleet_minted_present', 'fleet_reported_present', 'fleet_report_state', 'fleet_report_sha', 'fleet_heartbeat_fresh', 'fleet_mint_fresh', 'fleet_exact_receipt', 'fleet_backend_sui_running', 'fleet_backend_sui_bind_error', 'fleet_backend_sui_storage_error', 'fleet_backend_sui_start_error']) {
    for (const Observed of [true, false]) {
      const safeFact = source.sanitizeDestinationResults(input([{ Action: 'output', Package: pkg, Output: `native diagnostic: ${Diagnostic}=${Observed}\n` }]))
      assert.deepEqual(JSON.parse(safeFact), { Action: 'output', Package: pkg, Diagnostic, Observed })
      assert(source.isSafeDestinationDiagnostic(JSON.parse(safeFact)))
    }
    assert.equal(source.sanitizeDestinationResults(input([{ Action: 'output', Package: pkg, Output: `native diagnostic: ${Diagnostic}=private credential\n` }])).trim(), '')
  }
})

test('native fleet adds all nine SQL and old/new cohorts without replacing original upgrade proofs', async () => {
  const { destinationMatrixReports } = await import('./destination-matrix.mjs')
  const reports = destinationMatrixReports()
  assert.equal(reports.length, 48)
  assert.equal(reports.reduce((total, report) => total + report.cases.length, 0), 861)
  for (const database of ['sqlite', 'mysql', 'postgres']) for (const cohort of ['old', 'new', 'mixed']) {
    const report = reports.find(report => report.name === `native-fleet-${database}-${cohort}`)
    assert(report)
    assert.deepEqual(report.cases, stages)
    assert.equal(report.parent, 'TestNativeDestinationFleet')
    const engines = reports.find(report => report.name === `native-engine-fleet-${database}-${cohort}`)
    assert(engines)
    assert.deepEqual(engines.cases, stages)
    assert.equal(engines.parent, 'TestNativeDestinationFleet')
  }
  assert.equal(reports.filter(report => report.parent === 'TestNativeLegacyPSPUpgrade').length, 6)
  assert.equal(reports.filter(report => report.name.startsWith('native-fleet-')).length, 9)
  assert.equal(reports.filter(report => report.name.startsWith('native-engine-fleet-')).length, 9)
})

test('fleet workflow pins both daemons, requires production Run and both real adapters, and gates every cohort', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const start = workflow.indexOf('  fleet:')
  const end = workflow.indexOf('  complete-matrix:')
  assert(start > 0 && end > start)
  const job = workflow.slice(start, end)
  for (const value of ['database: [sqlite, mysql, postgres]', 'cohort: [old, new, mixed]', '8e5e3db72219a8db9081a372722abb1529d22ecd', '1ce8f26cfbce8d3decf8855dda4349be10778a23', 'PSP_NATIVE_FLEET_PROFILE', 'PSP_NATIVE_FLEET_OLD_BIN', 'PSP_NATIVE_FLEET_NEW_BIN', 'TestNativeDestinationFleet', 'check-fleet', 'PSP_NATIVE_DB_KIND']) assert(job.includes(value), value)
  assert(!job.includes('continue-on-error'))
  assert(job.includes('layout: [xray-pair, mixed-engines]'))
  assert(job.includes('PSP_NATIVE_FLEET_LAYOUT'))
  assert(job.includes('native-engine-fleet'))
  assert(workflow.slice(end).includes('needs.fleet.result'))
  const driver = readFileSync(new URL('../internal/app/destination_native_fleet_test.go', import.meta.url), 'utf8')
  for (const value of ['a.Run()', 'nativeStartFleetBackends', 'adapter.GetServerStatus', 'adapter.ListClientInbounds', 'nativeVLESSHost', 'nativeAllowlistMembershipMatches', 'partitionSync', 'nativeBindLiveCore']) assert(driver.includes(value), value)
  assert(driver.includes('nativeFleetStreamsApplied(s, peer.nativeFleetNodePlan)'))
  assert(!driver.includes('nativePanelStreamsApplied(s, 1)'))
  assert(job.includes('StreamsRequireOwnCoreAndEveryCurrentValidator'))
})

test('all eight peer diagnostics remain bounded booleans without private output', async () => {
  const source = await import('./destination-acceptance.mjs')
  for (let peer = 0; peer < 8; peer++) for (const fact of ['observation', 'agent', 'heartbeat', 'epoch', 'capability', 'selection', 'engine', 'version', 'running', 'listener', 'legacy', 'streams', 'policy', 'download', 'checksum', 'timeout', 'rejected', 'unsupported', 'permission', 'bind', 'certificate']) {
    for (const Observed of [true, false]) assert(source.isSafeDestinationDiagnostic({ Diagnostic: `fleet_peer_${peer}_${fact}`, Observed }))
    assert(!source.isSafeDestinationDiagnostic({ Diagnostic: `fleet_peer_${peer}_${fact}`, Observed: 'private row or process output' }))
  }
  for (const Diagnostic of ['fleet_peer_8_policy', 'fleet_peer_01_policy', 'fleet_peer_-1_policy', 'fleet_peer_2_credential']) assert(!source.isSafeDestinationDiagnostic({ Diagnostic, Observed: true }))
})
