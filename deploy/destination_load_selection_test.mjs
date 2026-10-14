import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const jobs = ['webhook-load', 'idle-cost', 'queue-pressure', 'sql-pressure', 'trial-load', 'block-overflow', 'mixed-pressure', 'global-bootstrap', 'global-batch']
const scopes = ['mixed-pressure', 'idle-cost', 'global-batch']
const choices = ['all', ...scopes, 'global-batch-profile']
const condition = "github.event_name != 'workflow_dispatch' || (inputs.suite || 'all') == 'all'"
const job = name => {
  const marker = '\n  ' + name + ':\n'
  const start = workflow.indexOf(marker)
  assert(start >= 0)
  const tail = workflow.slice(start + marker.length)
  return tail.slice(0, /^  [a-z][a-z-]*:\n/m.exec(tail)?.index ?? tail.length)
}

test('manual load selection defaults to all and cannot narrow PR or main checks', () => {
  assert.match(workflow, /workflow_dispatch:\n    inputs:\n      suite:\n/)
  assert.match(workflow, /type: choice\n        default: all\n/)
  for (const scope of choices) assert(workflow.includes('          - ' + scope + '\n'))
  assert(workflow.includes('branches: [main]') && workflow.includes('  pull_request:\n'))
  for (const name of jobs) {
    const expected = condition + (scopes.includes(name) ? " || inputs.suite == '" + name + "'" : '') + (name === 'global-batch' ? " || inputs.suite == 'global-batch-profile'" : '')
    assert.equal(job(name).match(/^\s+if: (.+)$/m)?.[1], expected)
  }
})

test('manual selection rejects unknown values without inserting input into shell code', () => {
  const selection = job('validate-selection')
  assert(selection.includes("PSP_ACCEPTANCE_SUITE: ${{ inputs.suite || 'all' }}"))
  assert(selection.includes('case "$PSP_ACCEPTANCE_SUITE" in'))
  assert(selection.includes('all|mixed-pressure|idle-cost|global-batch|global-batch-profile) ;;'))
  assert(selection.includes("*) printf '%s\\n' 'Unknown acceptance selection'; exit 1 ;;"))
  const shell = selection.slice(selection.indexOf('run: |'))
  assert(!shell.includes('inputs.suite') && !workflow.includes('continue-on-error'))
  const script = shell.trimEnd().split('\n').slice(1).map(line => line.slice(10)).join('\n')
  for (const scope of choices) {
    const result = spawnSync('bash', ['-c', script], { encoding: 'utf8', env: { ...process.env, PSP_ACCEPTANCE_SUITE: scope } })
    assert.equal(result.status, 0, result.stderr)
    assert.equal(result.stdout.trim(), 'Acceptance selection: ' + scope)
  }
  for (const scope of ['unknown', 'all; echo injected', '$(echo injected)', 'all\nexit 0']) {
    const result = spawnSync('bash', ['-c', script], { encoding: 'utf8', env: { ...process.env, PSP_ACCEPTANCE_SUITE: scope } })
    assert.equal(result.status, 1, result.stderr)
    assert.equal(result.stdout.trim(), 'Unknown acceptance selection')
  }
})

test('setup profiling is opt-in and keeps the complete pressure gate and evidence boundary', () => {
  const batch = job('global-batch')
  assert(batch.includes("PSP_NATIVE_SETUP_PROFILE: ${{ inputs.suite == 'global-batch-profile' && '1' || '0' }}"))
  assert(batch.includes('PSP_NATIVE_SETUP_PROFILE="$PSP_NATIVE_SETUP_PROFILE"'))
  assert(batch.includes('-test.timeout=20m'))
  assert(batch.includes('node deploy/destination-global-batch.mjs'))
  assert(batch.includes("if: always() && inputs.suite == 'global-batch-profile'"))
  assert(batch.includes('"$(command -v go)" tool pprof -top -nodecount=30'))
  assert(batch.includes("tool pprof -top -cum -nodecount=30 -show='^github.com/KazuhaHub/passwall-sub-panel/internal/'"))
  const upload = batch.slice(batch.indexOf('uses: actions/upload-artifact'))
  assert(!upload.includes('.pprof') && !upload.includes('profile'))
})
