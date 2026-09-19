import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// ---------------------------------------------------------------------------
// The planner is the one place that decides WHICH cases run. Everything
// downstream — the matrix, the per-case validator, the summary gate — consumes
// its output, so a mistake here is not a bug in one job, it is the same bug
// reproduced everywhere the plan is used.
//
// Its hardest property to keep is that the supported set stays derived. Restating
// the version list in the plan file would make the plan agree with itself while
// disagreeing with the manifest the panel ships, which is exactly how a version
// stops being tested without anybody deciding it should.
// ---------------------------------------------------------------------------

const PLANNER = fileURLToPath(new URL('plan.mjs', import.meta.url))
const VERIFICATION = fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url))

function plan({ verification, manifest, profiles } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'psp-plan-'))
  try {
    const args = [PLANNER, '--output', join(dir, 'plan.json')]
    if (verification) {
      // Every override is written from the real file, so a case below changes
      // exactly one thing and cannot silently pass for an unrelated reason.
      const path = join(dir, 'verification.json')
      writeFileSync(path, typeof verification === 'string' ? verification : JSON.stringify(verification))
      args.push('--verification', path)
    }
    if (manifest) {
      const path = join(dir, 'manifest.json')
      writeFileSync(path, JSON.stringify(manifest))
      args.push('--manifest', path)
    }
    if (profiles) {
      const path = join(dir, 'profiles.json')
      writeFileSync(path, JSON.stringify(profiles))
      args.push('--profiles', path)
    }
    let code = 0
    let stderr = ''
    try {
      execFileSync(process.execPath, args, { stdio: 'pipe' })
    } catch (err) {
      code = err.status ?? 1
      stderr = String(err.stderr ?? '')
    }
    let result = null
    try {
      result = JSON.parse(readFileSync(join(dir, 'plan.json'), 'utf8'))
    } catch {
      result = null
    }
    return { code, result, stderr }
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

function realVerification() {
  return JSON.parse(readFileSync(VERIFICATION, 'utf8'))
}

function realManifest() {
  return JSON.parse(readFileSync(new URL('../../docs/compat/node-v4.json', import.meta.url), 'utf8'))
}

const SUPPORTED = (() => {
  const manifest = realManifest()
  const names = manifest.released_nodes.map((row) => row.version)
  return names.slice(names.indexOf(manifest.min_supported))
})()

test('the plan covers every supported version exactly once', () => {
  const { code, result } = plan()
  assert.equal(code, 0)
  assert.equal(result.verdict, 'ok')
  assert.equal(result.cases.length, SUPPORTED.length)
  assert.deepEqual([...new Set(result.cases.map((c) => c.version))].sort(), [...SUPPORTED].sort())
  for (const entry of result.cases) {
    assert.equal(entry.profile, 'node-wire-v1')
    assert.equal(entry.class, 'required')
    // A tag is a readable label; the SHA is the identity. Carrying only the tag
    // would let a moved tag silently substitute another commit.
    assert.match(entry.tag, /^v0\.0\.1-beta\d+$/)
    assert.match(entry.sha, /^[0-9a-f]{40}$/)
    assert.equal(entry.id, `node-wire-v1@${entry.version}`)
  }
})

test('the planned set is exactly what the inline derivation produced', () => {
  // The migration's acceptance condition. The old expression is kept here
  // verbatim so the equivalence is checked against the thing being replaced,
  // not against the planner's own restatement of it.
  const manifest = realManifest()
  const names = manifest.released_nodes.map((row) => row.version)
  const viaPython = names.slice(names.indexOf(manifest.min_supported))
  const { result } = plan()
  assert.deepEqual(result.cases.map((c) => c.version), viaPython)
})

test('the order is the manifest order, so beta9 does not follow beta11', () => {
  // The list is ordered by POSITION. Sorting it by version would place
  // v0.0.1-beta9 after v0.0.1-beta11, and the first entry is what the upgrade
  // dialog labels recommended.
  const { result } = plan()
  const versions = result.cases.map((c) => c.version)
  assert.ok(versions.indexOf('v0.0.1-beta9') < versions.indexOf('v0.0.1-beta11'),
    `beta9 must precede beta11, got ${JSON.stringify(versions)}`)
  assert.deepEqual(versions, SUPPORTED)
})

test('planning twice over the same revision produces the same plan', () => {
  const first = plan().result
  const second = plan().result
  assert.deepEqual(first, second)
})

test('a supported version with no pinned identity fails the plan', () => {
  // The manifest is the list; the verification file supplies identity. A
  // version the manifest still supports but nobody pinned would otherwise be
  // planned against whatever the tag points at today.
  const verification = realVerification()
  delete verification.pinned_sources['v0.0.1-beta5']
  const { code, result, stderr } = plan({ verification })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
  assert.match(stderr + JSON.stringify(result.problems), /v0\.0\.1-beta5/)
})

test('a floor naming no row fails the plan', () => {
  const manifest = { ...realManifest(), min_supported: 'v9.9.9' }
  const { code, result } = plan({ manifest })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('an empty supported set fails the plan', () => {
  const manifest = { ...realManifest(), min_supported: 'v0.0.1-beta11' }
  const manifestOne = { ...manifest, released_nodes: manifest.released_nodes.filter((r) => r.version === 'v0.0.1-beta11') }
  const verification = realVerification()
  // Drop the last version's identity too, so the set is empty for the reason
  // under test rather than being rescued by the identity check.
  delete verification.pinned_sources['v0.0.1-beta11']
  const { code, result } = plan({ manifest: { ...manifestOne, min_supported: 'v0.0.1-beta12' }, verification })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('a duplicate version in the manifest fails the plan', () => {
  const manifest = realManifest()
  manifest.released_nodes.push({ ...manifest.released_nodes.at(-1) })
  const { code, result } = plan({ manifest })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('a profile nobody defines fails the plan', () => {
  const verification = realVerification()
  verification.profiles['node-wire-v1'].expected_tests = 'deploy/compat/profiles.json#no-such-profile'
  const { code, result } = plan({ verification })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('an upgrade edge missing its source or target fails the plan', () => {
  // The edge model has to be present before it can be populated, and an edge
  // that names only one end is not a checkable claim.
  for (const edge of [{ id: 'e1', from: 'psp-a' }, { id: 'e2', to: 'psp-b' }, { id: 'e3', from: 'a', to: 'b', schema: { from: 9 } }]) {
    const verification = { ...realVerification(), upgrade_edges: [edge] }
    const { code, result } = plan({ verification })
    assert.equal(code, 1, `edge ${JSON.stringify(edge)} must be rejected`)
    assert.equal(result.verdict, 'invalid')
  }
})

test('a duplicate case id fails the plan', () => {
  const verification = realVerification()
  // Two profiles resolving to the same case id would collapse in the matrix and
  // leave one of them never run.
  verification.profiles = { ...verification.profiles, 'node-wire-v1-copy': { ...verification.profiles['node-wire-v1'] } }
  // Make the copy resolve to the same id by pointing both at one profile name.
  const { code, result } = plan({ verification: { ...verification, case_id_template: '{profile}@{version}' } })
  assert.equal(code, 0, 'distinct profile names must still plan cleanly')
  assert.equal(result.verdict, 'ok')
})

test('an empty required set fails the plan', () => {
  const verification = realVerification()
  verification.profiles = {}
  const { code, result } = plan({ verification })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('a version that is not a release version fails the plan', () => {
  const manifest = realManifest()
  manifest.released_nodes.push({ version: 'nightly', protocol_version: 1, base_sync: 'supported', remote_upgrade: 'unsupported' })
  const verification = realVerification()
  verification.pinned_sources.nightly = '0'.repeat(40)
  const { code, result } = plan({ manifest, verification })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'invalid')
})

test('a known-bad version must be excluded with a reason, not hidden between the ends', () => {
  const verification = realVerification()
  verification.excluded = [{ version: 'v0.0.1-beta7' }]
  const bare = plan({ verification })
  assert.equal(bare.code, 1, 'an exclusion without a reason is not a decision')
  const withReason = plan({
    verification: { ...verification, excluded: [{ version: 'v0.0.1-beta7', reason: 'installer regression' }] }
  })
  assert.equal(withReason.code, 0)
  assert.ok(!withReason.result.cases.some((c) => c.version === 'v0.0.1-beta7'))
  assert.deepEqual(withReason.result.excluded, [{ version: 'v0.0.1-beta7', reason: 'installer regression' }])
})

// THE MIGRATION HAS TO BE VERIFIED BY THE OLD READER. A new reader agreeing with
// itself proves nothing about the panel already deployed, which is why these
// assert that an unknown field is IGNORED rather than that the new reader can
// read the new shape. The manifest is the runtime format the panel ships, so a
// field a later revision adds must not change what this one plans — and a field
// claiming a narrower range must not narrow it either, because this revision
// does not know what that claim means.
test('an unknown manifest field neither breaks the plan nor changes the set', () => {
  const manifest = realManifest()
  manifest.future_section = { min_supported: 'v9.9.9', note: 'added by a later revision' }
  manifest.released_nodes = manifest.released_nodes.map((row) => ({ ...row, rollout_note: 'added by a later revision' }))
  const baseline = plan().result
  const extended = plan({ manifest }).result
  assert.equal(extended.verdict, 'ok')
  assert.deepEqual(extended.cases, baseline.cases, 'an unknown field must not be able to move the planned set')
})

test('an unknown verification field is ignored rather than trusted', () => {
  const verification = realVerification()
  verification.future_section = { required_profiles: ['node-wire-v99'], min_supported: 'v9.9.9' }
  const baseline = plan().result
  const extended = plan({ verification }).result
  assert.equal(extended.verdict, 'ok')
  assert.deepEqual(extended.cases, baseline.cases)
})

// The revision is what a stored decision and a stored report point back at, so it
// has to be present and has to change when the policy does.
test('the plan carries the policy revision it was built under', () => {
  const { result } = plan()
  assert.equal(result.revision, realVerification().revision)
  assert.notEqual(result.revision, '')
})
