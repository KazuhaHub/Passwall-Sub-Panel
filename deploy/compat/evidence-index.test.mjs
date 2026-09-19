import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// ---------------------------------------------------------------------------
// R10 STEP 4: "build an evidence index from the complete case manifest, the
// results, the source SHA, and the archive and image digests".
//
// WHY AN INDEX AND NOT JUST THE ARTIFACTS. Every other check here answers a
// question about one run. A compatibility claim outlives the run that supported
// it — it is read months later, by someone deciding whether a combination is
// covered — and at that point the run's logs are gone and the artifacts have
// been replaced. The index is what survives: what was claimed, by which source,
// against which exact published artifacts, with which results.
//
// It is also the thing that makes "the evidence does not support that claim"
// checkable rather than a matter of opinion.
// ---------------------------------------------------------------------------

const INDEXER = fileURLToPath(new URL('evidence-index.mjs', import.meta.url))

function build(root, { cases = {}, digests = {}, plan } = {}) {
  const evidence = join(root, 'evidence')
  mkdirSync(evidence, { recursive: true })
  for (const [id, result] of Object.entries(cases)) {
    const dir = join(evidence, id)
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, 'result.json'), JSON.stringify(result))
  }
  const planPath = join(root, 'plan.json')
  writeFileSync(planPath, JSON.stringify(plan ?? { verdict: 'ok', revision: 'r1', cases: Object.keys(cases).map((id) => ({ id })) }))
  const digestPath = join(root, 'digests.json')
  writeFileSync(digestPath, JSON.stringify(digests))
  return { evidence, planPath, digestPath }
}

function index(root, options = {}) {
  const { evidence, planPath, digestPath } = build(root, options)
  const output = join(root, 'index.json')
  let code = 0
  let stderr = ''
  try {
    execFileSync(process.execPath, [
      INDEXER,
      '--evidence', evidence,
      '--plan', planPath,
      '--digests', digestPath,
      '--source-sha', options.sourceSha ?? 'a'.repeat(40),
      '--output', output
    ], { stdio: 'pipe' })
  } catch (err) {
    code = err.status ?? 1
    stderr = String(err.stderr ?? '')
  }
  let result = null
  try {
    result = JSON.parse(readFileSync(output, 'utf8'))
  } catch {
    result = null
  }
  return { code, result, stderr }
}

const passing = (id) => ({ case: id, verdict: 'pass' })

test('an index names the source, the cases and the results', () => {
  const root = mkdtempSync(join(tmpdir(), 'psp-index-'))
  try {
    const { code, result } = index(root, {
      cases: { 'node-wire-v1@v0.0.1-beta1': passing('node-wire-v1@v0.0.1-beta1'), 'node-wire-v1@v0.0.1-beta2': passing('node-wire-v1@v0.0.1-beta2') },
      digests: { 'passwall-sub-panel_v4.0.0_linux_amd64.tar.gz': 'b'.repeat(64) }
    })
    assert.equal(code, 0)
    assert.equal(result.source_sha, 'a'.repeat(40))
    assert.equal(result.revision, 'r1')
    assert.equal(result.cases.length, 2)
    assert.deepEqual(result.cases.map((c) => c.id).sort(), ['node-wire-v1@v0.0.1-beta1', 'node-wire-v1@v0.0.1-beta2'])
    assert.equal(result.cases[0].verdict, 'pass')
    // The digests are what tie the claim to the exact bytes that were tested, so
    // they travel with the index rather than beside it.
    assert.equal(result.artifacts['passwall-sub-panel_v4.0.0_linux_amd64.tar.gz'], 'b'.repeat(64))
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('a required case with no result is a failure, not an omission', () => {
  // An index that quietly leaves out a case it could not find is worse than no
  // index: it reads as a complete record of a smaller claim.
  const root = mkdtempSync(join(tmpdir(), 'psp-index-'))
  try {
    const { code, result } = index(root, {
      cases: { 'node-wire-v1@v0.0.1-beta1': passing('node-wire-v1@v0.0.1-beta1') },
      plan: {
        verdict: 'ok', revision: 'r1',
        cases: [{ id: 'node-wire-v1@v0.0.1-beta1' }, { id: 'node-wire-v1@v0.0.1-beta2' }]
      }
    })
    assert.equal(code, 1)
    assert.deepEqual(result.missing, ['node-wire-v1@v0.0.1-beta2'])
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('a case that did not pass is carried in the index, not filtered out', () => {
  // The index is the record. Dropping failures would make every index look clean
  // and leave the actual outcome to be inferred from something that expires.
  const root = mkdtempSync(join(tmpdir(), 'psp-index-'))
  try {
    const { code, result } = index(root, {
      cases: { 'node-wire-v1@v0.0.1-beta1': { case: 'node-wire-v1@v0.0.1-beta1', verdict: 'missing' } }
    })
    assert.equal(code, 0, 'a failing case is recorded, not rejected — the gate decides, this records')
    assert.equal(result.cases[0].verdict, 'missing')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('the plan revision is carried, so a later reader can tell which policy produced it', () => {
  const root = mkdtempSync(join(tmpdir(), 'psp-index-'))
  try {
    const { result } = index(root, {
      cases: { 'node-wire-v1@v0.0.1-beta1': passing('node-wire-v1@v0.0.1-beta1') },
      plan: { verdict: 'ok', revision: '2026-09-19.1', cases: [{ id: 'node-wire-v1@v0.0.1-beta1' }] }
    })
    assert.equal(result.revision, '2026-09-19.1')
    assert.equal(result.plan_verdict, 'ok')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('a malformed digests file is refused rather than indexed as empty', () => {
  const root = mkdtempSync(join(tmpdir(), 'psp-index-'))
  try {
    const evidence = join(root, 'evidence')
    mkdirSync(evidence, { recursive: true })
    const planPath = join(root, 'plan.json')
    writeFileSync(planPath, JSON.stringify({ verdict: 'ok', revision: 'r1', cases: [] }))
    const digestPath = join(root, 'digests.json')
    writeFileSync(digestPath, '{"broken":')
    let code = 0
    try {
      execFileSync(process.execPath, [INDEXER, '--evidence', evidence, '--plan', planPath, '--digests', digestPath, '--source-sha', 'a'.repeat(40), '--output', join(root, 'i.json')], { stdio: 'pipe' })
    } catch (err) {
      code = err.status ?? 1
    }
    assert.notEqual(code, 0, 'an unreadable digest source must not become an empty artifact map')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
