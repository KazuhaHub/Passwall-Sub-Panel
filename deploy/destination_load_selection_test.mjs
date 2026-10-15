import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const jobs = ['webhook-load', 'idle-cost', 'queue-pressure', 'sql-pressure', 'trial-load', 'block-overflow', 'mixed-pressure', 'global-bootstrap', 'global-batch']
const scopes = ['mixed-pressure', 'idle-cost', 'global-batch']
const choices = ['all', ...scopes, 'global-batch-profile', 'global-receipt-profile', 'global-traffic-profile']
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
    const expected = condition + (scopes.includes(name) ? " || inputs.suite == '" + name + "'" : '') + (name === 'global-batch' ? " || inputs.suite == 'global-batch-profile' || inputs.suite == 'global-receipt-profile' || inputs.suite == 'global-traffic-profile'" : '')
    assert.equal(job(name).match(/^\s+if: (.+)$/m)?.[1], expected)
  }
})

test('manual selection rejects unknown values without inserting input into shell code', () => {
  const selection = job('validate-selection')
  assert(selection.includes("PSP_ACCEPTANCE_SUITE: ${{ inputs.suite || 'all' }}"))
  assert(selection.includes('case "$PSP_ACCEPTANCE_SUITE" in'))
  assert(selection.includes('all|mixed-pressure|idle-cost|global-batch|global-batch-profile|global-receipt-profile|global-traffic-profile) ;;'))
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
  assert(batch.includes("PSP_NATIVE_SETUP_PROFILE: ${{ (inputs.suite == 'global-batch-profile' || inputs.suite == 'global-receipt-profile' || inputs.suite == 'global-traffic-profile') && '1' || '0' }}"))
  assert(batch.includes("PSP_NATIVE_SETUP_PROFILE_STAGE: ${{ inputs.suite == 'global-traffic-profile' && 'traffic' || inputs.suite == 'global-receipt-profile' && 'receipt' || 'membership' }}"))
  assert(batch.includes('PSP_NATIVE_SETUP_PROFILE="$PSP_NATIVE_SETUP_PROFILE"'))
  assert(batch.includes('-test.timeout=20m'))
  assert(batch.includes('node deploy/destination-global-batch.mjs'))
  assert(batch.includes("if: always() && (inputs.suite == 'global-batch-profile' || inputs.suite == 'global-receipt-profile' || inputs.suite == 'global-traffic-profile')"))
  assert(batch.includes('profile_args=()') && batch.includes('sudo install -m 0600 /dev/null'))
  assert(batch.includes('-test.blockprofilerate=10000000') && batch.includes('-test.mutexprofilefraction=10'))
  assert(batch.includes('"$(command -v go)" tool pprof -top -nodecount=30'))
  assert(batch.includes("tool pprof -top -cum -nodecount=30 -show='^github.com/KazuhaHub/passwall-sub-panel/internal/'"))
  assert(batch.includes('for sample in alloc_space alloc_objects; do'))
  assert(batch.includes('-sample_index="$sample"'))
  assert(batch.includes('-base="$RUNNER_TEMP/destination-global-setup.alloc-before.pprof"'))
  assert(batch.includes('"$RUNNER_TEMP/destination-global-setup.alloc-after.pprof"'))
  assert(batch.includes('CPU/allocation phase: %s') && batch.includes('traffic at most 120s'))
  assert(batch.includes("PSP_PROFILE_PHASE: ${{ inputs.suite == 'global-traffic-profile' && 'traffic' || inputs.suite == 'global-receipt-profile' && 'receipt' || 'membership' }}"))
  const source = readFileSync(new URL('../internal/app/destination_native_global_batch_test.go', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
  assert(source.includes('profileStage != "membership" && profileStage != "receipt" && profileStage != "traffic"'))
  assert(source.includes('if profile == "1" && profileStage == "traffic" {'))
  assert(source.includes('nativeStartSetupProfile(t, root, seconds*time.Second).stop'))
  assert(source.indexOf('t.Cleanup(stopTrafficProfile)') < source.indexOf('start := time.Now()'))
  assert(source.indexOf('m.TrafficMS, m.Failures =') < source.indexOf('if stopTrafficProfile != nil {'))
  assert(source.indexOf('if stopTrafficProfile != nil {') < source.indexOf('drainReason, drainComplete := '))
  const upload = batch.slice(batch.indexOf('uses: actions/upload-artifact'))
  assert(!upload.includes('.pprof') && !upload.includes('profile'))
})
