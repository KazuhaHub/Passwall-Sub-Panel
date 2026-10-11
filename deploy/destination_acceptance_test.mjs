import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkDestinationResults, sanitizeDestinationResults, nodeCandidate, destinationCases } from './destination-acceptance.mjs'

test('legacy upgrade has an independent complete real execution gate and immutable released sources', async () => {
  const acceptance = await import('./destination-acceptance.mjs')
  assert.equal(typeof acceptance.checkLegacyUpgradeResults, 'function')
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const tests = ['TestNativeLegacyPSPUpgrade', ...['old_panel', 'panel_upgrade', 'legacy_publication', 'mixed_fleet', 'mixed_offline_pause', 'node_upgrade', 'node_downgrade', 'node_reupgrade', 'panel_downgrade_offline', 'panel_downgrade', 'panel_reupgrade'].map(name => `TestNativeLegacyPSPUpgrade/${name}`)]
  const values = [...tests.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkLegacyUpgradeResults(evidence))
  assert.throws(() => checkDestinationResults(evidence))
  for (const name of tests) {
    assert.throws(() => acceptance.checkLegacyUpgradeResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkLegacyUpgradeResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const legacyJob = workflow.slice(workflow.indexOf('  legacy-upgrade:'))
  for (const value of ['0ae05e1e40170c030acd23cdf637310b740b50a6', '8e5e3db72219a8db9081a372722abb1529d22ecd', nodeCandidate.commit, 'native-legacy-panel-bridge.go.txt', 'check-legacy-upgrade', 'PSP_NATIVE_LEGACY_PANEL_BIN', 'PSP_NATIVE_LEGACY_NODE_BIN', 'PSP_NATIVE_MIXED_NODE_BIN']) assert(legacyJob.includes(value), value)
})
import * as acceptance from './destination-acceptance.mjs'

test('panel transition evidence retains bounded application duration and fixed facts only', () => {
  for (const phase of ['downgrade', 'reupgrade']) {
    for (const duration of [0, 5720, 120000, 150000]) {
      const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native panel application: ${phase}=${duration}\n` }])))
      assert.equal(event.Diagnostic, `panel_${phase}_application_ms`)
      assert.equal(event.Observed, duration)
    }
  }
  for (const fact of ['streams', 'capability', 'config_known', 'config_policy', 'report_applied', 'report_empty', 'report_current', 'original_core_exited']) {
    for (const value of ['true', 'false']) {
      const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: panel_${fact}=${value}\n` }])))
      assert.equal(event.Diagnostic, `panel_${fact}`)
      assert.equal(event.Observed, value === 'true')
    }
  }
  for (const value of ['downgrade=150001', 'secret=1', 'reupgrade=-1', 'downgrade=credential', 'reupgrade=1.25']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native panel application: ${value}\n` }])), '\n')
})

function events() {
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
  return [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
}
const encode = values => values.map(value => JSON.stringify(value)).join('\n')
test('node transition diagnostics retain only fixed booleans without private state', () => {
  for (const fact of ['sequence', 'capability', 'policy', 'core', 'listener', 'config', 'roster', 'version', 'original_etags', 'agent_fresh', 'streams', 'minted_empty']) {
    for (const value of ['true', 'false']) {
      const result = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: node_transition_${fact}=${value}\n` }])))
      assert.equal(result.Diagnostic, `node_transition_${fact}`)
      assert.equal(result.Observed, value === 'true')
    }
  }
  for (const text of ['credential=secret', 'config=private', 'address=true']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: node_transition_${text}\n` }])), '\n')
})
test('Xray separately requires actual LKG and pruned-candidate rejection', () => {
  for (const name of ['lkg_rejected', 'pruned_rejected']) {
    assert(destinationCases.includes(name))
    assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith(`/${name}`)))))
  }
})
test('Xray requires actual policy rejection and retry independently of listener failure', () => {
  assert(destinationCases.includes('policy_rejected'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/policy_rejected')))))
})
test('each engine requires a real report-only refresh without replacing its core', () => {
  for (const cases of [destinationCases, acceptance.singBoxCases, acceptance.guardedSingBoxCases]) assert(cases.includes('report_refresh'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/report_refresh')))))
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  assert(workflow.includes("PSP_NATIVE_SOURCE_COMMIT='${{ github.event.pull_request.head.sha || github.sha }}'"))
})
test('each engine requires the real unchanged-sync process identity case', () => {
  for (const cases of [destinationCases, acceptance.singBoxCases, acceptance.guardedSingBoxCases]) assert(cases.includes('idle_sync'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/idle_sync')))))
})
test('native matrix requires the actual core-check timeout case', () => {
  assert(destinationCases.includes('core_check_timeout'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/core_check_timeout')))))
})
test('listener diagnostics retain only fixed booleans and withhold arbitrary labels and values', () => {
  for (const category of ['listener_issue', 'listener_core_rejected', 'listener_agent_fresh', 'listener_policy_ready', 'listener_policy_unchanged', 'listener_epoch', 'listener_config_changed', 'listener_config_preserved', 'listener_config_fresh', 'receipt_generation', 'receipt_mint_fresh', 'receipt_sync_fresh', 'receipt_state', 'receipt_sha', 'receipt_lkg', 'receipt_precheck', 'receipt_streams', 'receipt_transition', 'fallback_report', 'fallback_accepted', 'fallback_empty', 'fallback_exhausted', 'fallback_core', 'fallback_streams', 'fallback_issue']) {
    for (const value of ['true', 'false']) {
      const retained = sanitizeDestinationResults(encode([{ Action: 'output', Package: 'private', Output: `native diagnostic: ${category}=${value}\n` }]))
      assert.equal(JSON.parse(retained).Diagnostic, category)
      assert.equal(JSON.parse(retained).Observed, value === 'true')
    }
  }
  for (const output of ['native diagnostic: listener_secret=true\n', 'native diagnostic: listener_issue=credential\n']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: output }])), '\n')
})
test('retained execution evidence excludes process output and arbitrary metadata', () => {
  const retained = sanitizeDestinationResults(encode([...events(), { Action: 'output', Package: 'private', Output: 'credential email address' }, { Action: 'pass', Package: 'ignored', Output: 'private', Private: 'secret' }]))
  for (const privateText of ['credential', 'email', 'address', 'private', 'secret', 'Output', 'Private']) assert(!retained.includes(privateText))
  assert.doesNotThrow(() => checkDestinationResults(retained))
  const diagnostic = sanitizeDestinationResults(encode([{ Action: 'output', Package: 'private', Output: 'private address: real VLESS traffic did not reach its nonce target; credential secret' }]))
  assert.equal(JSON.parse(diagnostic).Diagnostic, 'traffic_allow_failed')
  assert(!diagnostic.includes('credential'))
  assert(!diagnostic.includes('address'))
})
test('acceptance requires immutable reviewed Node source', () => {
  assert.equal(nodeCandidate.repository, 'KazuhaHub/Passwall-Node')
  assert.match(nodeCandidate.commit, /^[a-f0-9]{40}$/)
})
test('real execution gate rejects absent, skipped, failed, duplicate and incomplete cases', () => {
  assert.doesNotThrow(() => checkDestinationResults(encode(events())))
  for (const values of [[], events().slice(0, -1), events().filter(e => !e.Test?.endsWith('/trial')), [...events(), events()[0]], events().map(e => e.Test?.endsWith('/block') && e.Action === 'pass' ? { ...e, Action: 'skip' } : e), [...events(), { Action: 'fail', Package: 'other/package' }]]) {
    assert.throws(() => checkDestinationResults(encode(values)))
  }
  assert.throws(() => checkDestinationResults('not JSON'))
})
test('workflow exercises the actual Node/PSP boundary and uploads only reviewed evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const required of ['ubuntu-24.04', nodeCandidate.commit, 'destination_acceptance', 'TestNativeDestinationTraffic', 'verifier=check', 'destination-acceptance.mjs "$verifier"', '26.6.27', '26.7.28', '26.9.9', 'PSP_NATIVE_NODE_BIN', 'PSP_NATIVE_CORE_VERSION']) assert(workflow.includes(required), required)
  assert(!workflow.includes('continue-on-error'))
  assert(!workflow.includes('secrets.'))
  assert(!workflow.includes('actions/upload-artifact') || workflow.includes('destination-acceptance-tests.json'))
})
test('the real driver exercises the first report and traffic after a daemon restart', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_acceptance_test.go', import.meta.url), 'utf8')
  assert(destinationCases.includes('restart'))
  for (const required of ['run("restart"', 'probe.arm()', 'probe.first()', 'stopDaemon(caseTest)', 'startDaemon(caseTest)']) assert(driver.includes(required), required)
})
test('the driver must execute every declared real traffic and fault scenario', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_acceptance_test.go', import.meta.url), 'utf8')
  for (const name of destinationCases) assert(driver.includes(`run("${name}"`), name)
})

test('sing-box execution has its own complete gate and cannot borrow Xray passes', () => {
  assert.equal(typeof acceptance.checkSingBoxResults, 'function')
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeSingBoxDestinationTraffic', ...acceptance.singBoxCases.map(name => `TestNativeSingBoxDestinationTraffic/${name}`)]
  const values = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkSingBoxResults(evidence))
  assert.throws(() => checkDestinationResults(evidence))
  assert.throws(() => acceptance.checkSingBoxResults(encode(events())))
  for (const name of names) {
    assert.throws(() => acceptance.checkSingBoxResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkSingBoxResults(encode([...values, { Action: 'run', Package: pkg, Test: name }])))
    assert.throws(() => acceptance.checkSingBoxResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
})

test('receipt diagnostic sequence is bounded and contains no arbitrary output', () => {
  for (const n of [1, 17, 99]) {
    const retained = sanitizeDestinationResults(encode([{ Action: 'output', Output: `native receipt wait: ${n}\n` }]))
    assert.equal(JSON.parse(retained).Diagnostic, 'receipt_wait')
    assert.equal(JSON.parse(retained).Observed, n)
  }
  for (const value of ['0', '100', 'secret', '-1']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native receipt wait: ${value}\n` }])), '\n')
})

test('sing-box driver and hosted workflow execute every declared scenario', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_singbox_test.go', import.meta.url), 'utf8')
  for (const name of acceptance.singBoxCases) assert(driver.includes(`run("${name}"`), name)
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const value of ['sing-box', '1.14.0', 'TestNativeSingBoxDestinationTraffic', 'check-singbox']) assert(workflow.includes(value), value)
})

test('guarded sing-box keeps an independent immutable candidate and complete execution gate', () => {
  assert.equal(acceptance.guardedNodeCandidate?.repository, nodeCandidate.repository)
  assert.equal(acceptance.guardedNodeCandidate?.commit, 'a6163c8b5c10a9c8129cf660020fe2c8fa2b85db')
  assert.notEqual(acceptance.guardedNodeCandidate.commit, nodeCandidate.commit)
  assert.deepEqual(acceptance.guardedSingBoxCases, [...acceptance.singBoxCases.filter(name => name !== 'private_baseline'), 'private_guard'])
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeGuardedSingBoxDestinationTraffic', ...acceptance.guardedSingBoxCases.map(name => `TestNativeGuardedSingBoxDestinationTraffic/${name}`)]
  const values = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkGuardedSingBoxResults(evidence))
  assert.throws(() => acceptance.checkSingBoxResults(evidence))
  for (const name of names) {
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode([...values, { Action: 'run', Package: pkg, Test: name }])))
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(events())))
  const baseline = values.map(event => event.Test ? { ...event, Test: event.Test.replace('TestNativeGuardedSingBoxDestinationTraffic', 'TestNativeSingBoxDestinationTraffic').replace('/private_guard', '/private_baseline') } : event)
  assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(baseline)))
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const required of [nodeCandidate.commit, acceptance.guardedNodeCandidate.commit, '${{ matrix.node }}', 'check-guarded-singbox', 'TestNativeGuardedSingBoxDestinationTraffic']) assert(workflow.includes(required), required)
  const driver = readFileSync(new URL('../internal/app/destination_native_singbox_test.go', import.meta.url), 'utf8')
  for (const required of ['func TestNativeGuardedSingBoxDestinationTraffic', 'run("private_guard"', 'privateRequests.Load()', 'privateDestinations(false)', 'strconv.Itoa(privatePort)']) assert(driver.includes(required), required)
})
