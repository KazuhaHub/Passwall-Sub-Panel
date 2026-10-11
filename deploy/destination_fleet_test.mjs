import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
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
  for (const Diagnostic of ['fleet_peer_0_observation', 'fleet_peer_1_observation', 'fleet_peer_0_streams', 'fleet_peer_1_streams', 'fleet_peer_0_policy', 'fleet_peer_1_policy']) {
    for (const Observed of [true, false]) assert(source.isSafeDestinationDiagnostic({ Diagnostic, Observed }))
    assert(!source.isSafeDestinationDiagnostic({ Diagnostic, Observed: 'private credential' }))
  }
  assert(!source.isSafeDestinationDiagnostic({ Diagnostic: 'fleet_peer_2_policy', Observed: true }))
})

test('native fleet adds all nine SQL and old/new cohorts without replacing original upgrade proofs', async () => {
  const { destinationMatrixReports } = await import('./destination-matrix.mjs')
  const reports = destinationMatrixReports()
  assert.equal(reports.length, 39)
  assert.equal(reports.reduce((total, report) => total + report.cases.length, 0), 771)
  for (const database of ['sqlite', 'mysql', 'postgres']) for (const cohort of ['old', 'new', 'mixed']) {
    const report = reports.find(report => report.name === `native-fleet-${database}-${cohort}`)
    assert(report)
    assert.deepEqual(report.cases, stages)
    assert.equal(report.parent, 'TestNativeDestinationFleet')
  }
  assert.equal(reports.filter(report => report.parent === 'TestNativeLegacyPSPUpgrade').length, 6)
})

test('fleet workflow pins both daemons, requires production Run and both real adapters, and gates every cohort', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const start = workflow.indexOf('  fleet:')
  const end = workflow.indexOf('  complete-matrix:')
  assert(start > 0 && end > start)
  const job = workflow.slice(start, end)
  for (const value of ['database: [sqlite, mysql, postgres]', 'cohort: [old, new, mixed]', '8e5e3db72219a8db9081a372722abb1529d22ecd', 'cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9', 'PSP_NATIVE_FLEET_PROFILE', 'PSP_NATIVE_FLEET_OLD_BIN', 'PSP_NATIVE_FLEET_NEW_BIN', 'TestNativeDestinationFleet', 'check-fleet', 'PSP_NATIVE_DB_KIND']) assert(job.includes(value), value)
  assert(!job.includes('continue-on-error'))
  assert(workflow.slice(end).includes('needs.fleet.result'))
  const driver = readFileSync(new URL('../internal/app/destination_native_fleet_test.go', import.meta.url), 'utf8')
  for (const value of ['a.Run()', 'nativeStartFleetBackends', 'adapter.GetServerStatus', 'adapter.ListClientInbounds', 'nativeVLESSHost', 'nativeAllowlistMembershipMatches', 'partitionSync', 'nativeBindLiveCore']) assert(driver.includes(value), value)
})
