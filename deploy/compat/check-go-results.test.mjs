import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// ---------------------------------------------------------------------------
// THE WHOLE POINT OF THIS FILE: "we did not measure it" must never come back
// green. A green Go run is not evidence that the tests that matter ran — `go
// test` exits 0 when every test it selected passes, and it exits 0 when it
// selected none of them. `-run` matching nothing, a skip taken because the
// fixture variable is unset, a step that lost its exit code to a pipe: each of
// those produces a clean exit and no coverage, which is exactly the failure the
// compatibility matrix exists to prevent.
// ---------------------------------------------------------------------------

const CHECKER = fileURLToPath(new URL('check-go-results.mjs', import.meta.url))
const PROFILES = fileURLToPath(new URL('profiles.json', import.meta.url))
const PROFILE = 'node-wire-v1'
const PKG = 'github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync'

const REQUIRED = [
  'TestLive_RealNodeAgentContract',
  'TestLive_RealNodeMigratedServerContract',
  'TestLive_RealNodeTaskEvidenceReceipt',
  'TestLive_RealNodeTaskExpiryContract'
]

// The expiry test is a table over two modes. Its parent passes whenever the
// modes it did run passed, so a mode that skipped — or never appeared — is
// invisible unless the profile names it.
const EXPIRY_SUBTESTS = ['late-completion-terminal-replay', 'delayed-body-unknown-journal']

// Every required ITEM, not just every required test. The expiry profile also
// requires each of its two modes, so a log that ran nothing is missing six
// things and the report has to name all six — listing only the parents would
// leave the modes looking merely unmentioned.
const REQUIRED_ITEMS = [
  'TestLive_RealNodeAgentContract',
  'TestLive_RealNodeMigratedServerContract',
  'TestLive_RealNodeTaskEvidenceReceipt',
  'TestLive_RealNodeTaskExpiryContract',
  'TestLive_RealNodeTaskExpiryContract/delayed-body-unknown-journal',
  'TestLive_RealNodeTaskExpiryContract/late-completion-terminal-replay'
]

function line(action, fields = {}) {
  return JSON.stringify({ Time: '2026-09-19T00:00:00Z', Action: action, ...fields })
}

// The one log that may come back green: every required test and subtest runs and
// passes, and so does the package.
function passingLog() {
  const lines = []
  for (const name of REQUIRED.slice(0, -1)) {
    lines.push(line('run', { Package: PKG, Test: name }))
    lines.push(line('pass', { Package: PKG, Test: name, Elapsed: 0.1 }))
  }
  const parent = REQUIRED.at(-1)
  lines.push(line('run', { Package: PKG, Test: parent }))
  for (const mode of EXPIRY_SUBTESTS) {
    lines.push(line('run', { Package: PKG, Test: `${parent}/${mode}` }))
    lines.push(line('pass', { Package: PKG, Test: `${parent}/${mode}`, Elapsed: 0.1 }))
  }
  lines.push(line('pass', { Package: PKG, Test: parent, Elapsed: 0.2 }))
  lines.push(line('pass', { Package: PKG, Elapsed: 1 }))
  return lines
}

function withProfiles(overrides) {
  const dir = mkdtempSync(join(tmpdir(), 'psp-profiles-'))
  const path = join(dir, 'profiles.json')
  writeFileSync(path, JSON.stringify({ ...JSON.parse(readFileSync(PROFILES, 'utf8')), ...overrides }))
  return path
}

// Runs the checker over a log and returns both the exit code and whatever
// structured result it managed to write. A checker that reports a rejection
// without saying WHAT was missing leaves the reader with a red build and no
// next step, so almost every case below asserts on the result too.
function check({ lines = passingLog(), raw, goExitCode = 0, profile = PROFILE, profiles = PROFILES } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'psp-compat-'))
  try {
    const input = join(dir, 'events.jsonl')
    writeFileSync(input, raw ?? lines.join('\n') + '\n')
    const output = join(dir, 'result.json')
    let code = 0
    try {
      execFileSync(process.execPath, [
        CHECKER, '--profile', profile, '--input', input,
        '--go-exit-code', String(goExitCode), '--output', output, '--profiles', profiles
      ], { stdio: 'pipe' })
    } catch (err) {
      code = err.status ?? 1
    }
    let result = null
    try {
      result = JSON.parse(readFileSync(output, 'utf8'))
    } catch {
      result = null
    }
    return { code, result }
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

test('a complete, passing run is the only shape that passes', () => {
  const { code, result } = check()
  assert.equal(code, 0)
  assert.equal(result.verdict, 'pass')
  assert.deepEqual(result.missing, [])
  assert.deepEqual(result.failed, [])
  assert.deepEqual(result.skipped, [])
  assert.deepEqual(result.unexpected, [])
  // The denominator stays visible: the result names every test it required, so
  // a green line can be read against what was actually asked for.
  assert.deepEqual(result.requiredTests, REQUIRED)
  assert.equal(result.package, PKG)
})

test('an empty log is a rejection that names every missing test', () => {
  const { code, result } = check({ lines: [] })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'missing')
  assert.deepEqual(result.missing, REQUIRED_ITEMS)
})

test('a log holding only the package result is not a pass', () => {
  // Exactly what `-run` matching nothing produces, and exactly what reads as
  // success to anything that only looks at the exit code.
  const { code, result } = check({ lines: [line('pass', { Package: PKG, Elapsed: 0.01 })] })
  assert.equal(code, 1)
  assert.deepEqual(result.missing, REQUIRED_ITEMS)
})

test('a test that ran and passed in another package does not count', () => {
  // Same name, wrong package. Matching on the test name alone would accept it
  // and quietly drop the coverage.
  const lines = passingLog().filter((row) => !row.includes('TestLive_RealNodeAgentContract'))
  const decoy = line('pass', { Package: 'example.com/other', Test: REQUIRED[0] })
  const { code, result } = check({ lines: [...lines, decoy] })
  assert.equal(code, 1)
  assert.deepEqual(result.missing, [REQUIRED[0]])
})

test('a skipped required test is a rejection', () => {
  const lines = passingLog().filter((row) => !row.includes(`"Test":"${REQUIRED[0]}"`))
  lines.unshift(line('run', { Package: PKG, Test: REQUIRED[0] }))
  lines.splice(1, 0, line('skip', { Package: PKG, Test: REQUIRED[0] }))
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.deepEqual(result.skipped, [REQUIRED[0]])
})

test('a skipped REQUIRED SUBTEST is a rejection even though its parent passes', () => {
  // The case a parent-only profile cannot see. The parent's terminal state is
  // `pass`, so everything that checks tests rather than subtests calls this a
  // clean run while one of the two modes was never exercised.
  const parent = REQUIRED.at(-1)
  const lines = passingLog().filter((row) => !row.includes(`${parent}/${EXPIRY_SUBTESTS[0]}"`))
  lines.splice(lines.findIndex((row) => row.includes(`"Action":"pass","Package":"${PKG}","Test":"${parent}"`)) , 0,
    line('run', { Package: PKG, Test: `${parent}/${EXPIRY_SUBTESTS[0]}` }),
    line('skip', { Package: PKG, Test: `${parent}/${EXPIRY_SUBTESTS[0]}` }))
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.deepEqual(result.skipped, [`${parent}/${EXPIRY_SUBTESTS[0]}`])
})

test('a skipped parent reports its required modes as skipped, not missing', () => {
  // Exactly what an unset PSP_LIVE_NODE_REPO produces: the parent skips, so its
  // modes never get a chance to run. Calling those modes "missing" sends the
  // reader hunting for a renamed or deleted subtest when the cause is the
  // parent's own skip, one line above. Reclassified rather than excused: the
  // run is still a rejection and the modes are still not covered.
  const parent = REQUIRED.at(-1)
  const lines = passingLog().filter((row) => !row.includes(parent))
  lines.unshift(line('run', { Package: PKG, Test: parent }), line('skip', { Package: PKG, Test: parent }))
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.deepEqual(result.missing, [])
  assert.deepEqual(result.skipped, [
    parent,
    `${parent}/${EXPIRY_SUBTESTS[1]}`,
    `${parent}/${EXPIRY_SUBTESTS[0]}`
  ])
})

test('a failing required test is a rejection', () => {
  const lines = passingLog().map((row) =>
    row.includes(`"Test":"${REQUIRED[1]}"`) ? row.replace('"Action":"pass"', '"Action":"fail"') : row)
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.deepEqual(result.failed, [REQUIRED[1]])
})

test('a test that fails and then passes again is not a pass', () => {
  // Conflicting terminal states. Taking the last one would let a retry or a
  // replayed stream launder a failure into a success.
  const lines = passingLog().map((row) =>
    row.includes(`"Test":"${REQUIRED[1]}"`) ? row.replace('"Action":"pass"', '"Action":"fail"') : row)
  lines.push(line('pass', { Package: PKG, Test: REQUIRED[1], Elapsed: 0.2 }))
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.ok(result.failed.includes(REQUIRED[1]), `failed = ${JSON.stringify(result.failed)}`)
})

test('a truncated JSON stream is a rejection', () => {
  const full = passingLog().join('\n')
  const { code, result } = check({ raw: full.slice(0, Math.floor(full.length / 2)) })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'unreadable')
})

test('a non-JSON line is a rejection', () => {
  const { code, result } = check({ raw: passingLog().join('\n') + '\npanic: something\n' })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'unreadable')
})

test('a test that never reached a terminal state is a rejection', () => {
  // The process was killed mid-run. `run` with no `pass` is not evidence.
  const { code, result } = check({ lines: [line('run', { Package: PKG, Test: REQUIRED[0] })] })
  assert.equal(code, 1)
  assert.ok(result.missing.includes(REQUIRED[0]), `missing = ${JSON.stringify(result.missing)}`)
})

test('a passing test with a non-zero Go exit code is a rejection', () => {
  // How a failure is laundered: the tests pass, something else in the process
  // did not, and only the exit code knows.
  const { code, result } = check({ goExitCode: 1 })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'go-exit-nonzero')
})

test('an unexpected test is reported rather than silently ignored', () => {
  // A profile is a closed set. Anything that ran without being declared means
  // the run is not the run the profile describes — which is what a mistyped
  // `-run` regex produces in the over-matching direction.
  const lines = passingLog()
  lines.push(line('run', { Package: PKG, Test: 'TestSomethingElse' }))
  lines.push(line('pass', { Package: PKG, Test: 'TestSomethingElse', Elapsed: 0.1 }))
  const { code, result } = check({ lines })
  assert.equal(code, 1)
  assert.deepEqual(result.unexpected, ['TestSomethingElse'])
})

test('a declared not-applicable item is reported as N/A and does not count as a pass', () => {
  const parent = REQUIRED.at(-1)
  const profiles = withProfiles({
    [PROFILE]: {
      ...JSON.parse(readFileSync(PROFILES, 'utf8'))[PROFILE],
      notApplicable: {
        [parent]: {
          reason: 'the pinned build predates this mode',
          subtests: [EXPIRY_SUBTESTS[1]]
        }
      }
    }
  })
  const lines = passingLog().filter((row) => !row.includes(EXPIRY_SUBTESTS[1]))
  const { code, result } = check({ lines, profiles })
  // The run is complete for everything the profile still requires.
  assert.equal(code, 0)
  assert.equal(result.verdict, 'pass')
  // But the waiver is visible and is NOT counted as coverage.
  assert.deepEqual(result.notApplicable, [{ test: `${parent}/${EXPIRY_SUBTESTS[1]}`, reason: 'the pinned build predates this mode' }])
  assert.ok(!result.passed.includes(`${parent}/${EXPIRY_SUBTESTS[1]}`))
})

test('an undeclared skip of something not in the profile still cannot pass silently', () => {
  // Negative control for the row above: without the waiver, the same log fails.
  const lines = passingLog().filter((row) => !row.includes(EXPIRY_SUBTESTS[1]))
  const { code } = check({ lines })
  assert.equal(code, 1)
})
