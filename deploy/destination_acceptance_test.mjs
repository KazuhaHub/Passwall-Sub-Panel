import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkDestinationResults, sanitizeDestinationResults, nodeCandidate, destinationCases } from './destination-acceptance.mjs'

function events() {
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
  return [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
}
const encode = values => values.map(value => JSON.stringify(value)).join('\n')
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
  for (const required of ['ubuntu-24.04', nodeCandidate.commit, 'destination_acceptance', 'TestNativeDestinationTraffic', 'destination-acceptance.mjs check', '26.6.27', '26.7.28', '26.9.9', 'PSP_NATIVE_NODE_BIN', 'PSP_NATIVE_CORE_VERSION']) assert(workflow.includes(required), required)
  assert(!workflow.includes('continue-on-error'))
  assert(!workflow.includes('secrets.'))
  assert(!workflow.includes('actions/upload-artifact') || workflow.includes('destination-acceptance-tests.json'))
})
test('the real driver exercises the first report and traffic after a daemon restart', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_acceptance_test.go', import.meta.url), 'utf8')
  assert(destinationCases.includes('restart'))
  for (const required of ['run("restart"', 'probe.arm()', 'probe.first()', 'stopDaemon(caseTest)', 'startDaemon(caseTest)']) assert(driver.includes(required), required)
})
