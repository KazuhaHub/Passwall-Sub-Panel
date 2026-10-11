import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkDestinationResults, sanitizeDestinationResults, nodeCandidate, destinationCases } from './destination-acceptance.mjs'
import * as acceptance from './destination-acceptance.mjs'

function events() {
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
  return [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
}
const encode = values => values.map(value => JSON.stringify(value)).join('\n')
test('native matrix requires the actual core-check timeout case', () => {
  assert(destinationCases.includes('core_check_timeout'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/core_check_timeout')))))
})
test('listener diagnostics retain only fixed booleans and withhold arbitrary labels and values', () => {
  for (const category of ['listener_issue', 'listener_core_rejected', 'listener_agent_fresh', 'listener_policy_ready', 'listener_policy_unchanged', 'listener_epoch', 'listener_config_changed', 'listener_config_preserved', 'listener_config_fresh', 'receipt_generation', 'receipt_mint_fresh', 'receipt_sync_fresh', 'receipt_state', 'receipt_sha', 'receipt_lkg', 'receipt_precheck', 'receipt_streams', 'receipt_transition']) {
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
