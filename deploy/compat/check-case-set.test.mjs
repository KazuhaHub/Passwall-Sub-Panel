import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { after, test } from 'node:test'

// ---------------------------------------------------------------------------
// The gate over every compatibility case. A per-case validator (R02) says
// whether ONE case was measured; this says whether the SET was. The distinction
// is the whole job: a matrix where a leg was cancelled, or an artifact upload
// that silently failed, leaves every case that DID report perfectly green, and
// the run reads as complete while a version went untested.
//
// So the expected set is derived, never read back from the reports directory.
// "Every report I found is fine" is a statement that is trivially true of an
// empty directory.
// ---------------------------------------------------------------------------

const CHECKER = fileURLToPath(new URL('check-case-set.mjs', import.meta.url))
const MANIFEST = fileURLToPath(new URL('../../docs/compat/node-v4.json', import.meta.url))

// A TWO-RELEASE MANIFEST, BECAUSE MOST OF THESE CASES ARE ABOUT A SET.
//
// The shipped manifest carries one release — the products publish one, and there
// is no deployment whose set has to stay covered while it is narrowed — and a set
// of one cannot show a dropped leg, a failed report, an unreadable one, or a
// narrowed floor: every one of those cases needs a case that SURVIVES it. So the
// gate is driven against the shipped manifest plus a release, and what the cases
// below exercise is the gate rather than the file.
const EXTRA_RELEASES = ['4.0.1', '4.0.2', '4.0.3']
const manifestDir = mkdtempSync(join(tmpdir(), 'psp-manifest-'))
const MANIFEST_TWO = join(manifestDir, 'node-v4.json')
after(() => rmSync(manifestDir, { recursive: true, force: true }))

function twoReleaseManifest() {
  const manifest = JSON.parse(readFileSync(MANIFEST, 'utf8'))
  manifest.released_nodes = [
    ...manifest.released_nodes,
    ...EXTRA_RELEASES.map((version) => ({ version, protocol_version: 1, base_sync: 'supported', remote_upgrade: 'conditional' }))
  ]
  return manifest
}
writeFileSync(MANIFEST_TWO, JSON.stringify(twoReleaseManifest()))

// The versions the panel still supports, matched by POSITION at min_supported.
function supportedVersions(path = MANIFEST_TWO) {
  const manifest = JSON.parse(readFileSync(path, 'utf8'))
  const names = manifest.released_nodes.map((row) => row.version)
  return names.slice(names.indexOf(manifest.min_supported))
}

const VERSIONS = supportedVersions()
const CASES = VERSIONS.map((version) => `node-wire-v1@${version}`)

function writeCase(root, id, { verdict = 'pass', raw } = {}) {
  const dir = join(root, id)
  mkdirSync(dir, { recursive: true })
  writeFileSync(join(dir, 'result.json'), raw ?? JSON.stringify({ profile: id.split('@')[0], verdict }))
  writeFileSync(join(dir, 'go-events.jsonl'), '')
  writeFileSync(join(dir, 'environment.json'), JSON.stringify({ psp_sha: 'x', node_sha: 'y' }))
}

function check(build, { extraArgs: given = [] } = {}) {
  let extraArgs = [...given]
  const dir = mkdtempSync(join(tmpdir(), 'psp-caseset-'))
  try {
    const reports = join(dir, 'evidence')
    mkdirSync(reports, { recursive: true })
    build(reports)
    const output = join(dir, 'case-set.json')
    let code = 0
    try {
      if (!extraArgs.includes('--manifest')) extraArgs = [...extraArgs, '--manifest', MANIFEST_TWO]
      execFileSync(process.execPath, [CHECKER, '--reports', reports, '--output', output, ...extraArgs], { stdio: 'pipe' })
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

const everyCase = (reports) => CASES.forEach((id) => writeCase(reports, id))

test('a complete set of passing reports is the only shape that passes', () => {
  const { code, result } = check(everyCase)
  assert.equal(code, 0)
  assert.equal(result.verdict, 'pass')
  assert.deepEqual(result.expected, CASES)
  assert.deepEqual(result.missing, [])
  assert.deepEqual(result.notPassing, [])
})

test('a missing case fails the gate even though every report present is green', () => {
  // The cancelled-matrix-leg case. Nothing about the reports that survived is
  // wrong; the run is still incomplete.
  const dropped = CASES[3]
  const { code, result } = check((reports) => {
    everyCase(reports)
    rmSync(join(reports, dropped), { recursive: true })
  })
  assert.equal(code, 1)
  assert.equal(result.verdict, 'incomplete')
  assert.deepEqual(result.missing, [dropped])
})

test('an empty reports directory is incomplete, not vacuously complete', () => {
  // "Every report I found passed" must not be satisfiable by finding none.
  const { code, result } = check(() => {})
  assert.equal(code, 1)
  assert.deepEqual(result.missing, CASES)
})

test('a report whose own verdict is not a pass fails the gate', () => {
  const failed = CASES[2]
  const { code, result } = check((reports) => {
    everyCase(reports)
    writeCase(reports, failed, { verdict: 'missing' })
  })
  assert.equal(code, 1)
  assert.deepEqual(result.notPassing, [{ case: failed, verdict: 'missing' }])
})

test('an unreadable report is not a pass', () => {
  const broken = CASES[1]
  const { code, result } = check((reports) => {
    everyCase(reports)
    writeCase(reports, broken, { raw: '{"verdict":' })
  })
  assert.equal(code, 1)
  assert.deepEqual(result.missing, [broken])
})

test('a case with no result file at all is missing', () => {
  const hollow = CASES[0]
  const { code, result } = check((reports) => {
    everyCase(reports)
    writeFileSync(join(reports, hollow, 'result.json'), '')
  })
  assert.equal(code, 1)
  assert.deepEqual(result.missing, [hollow])
})

test('reports for cases nobody asked about are recorded', () => {
  // Extra evidence is not itself a failure — but it has to be visible, because
  // a name that drifted between the matrix and the profile shows up here first.
  const { code, result } = check((reports) => {
    everyCase(reports)
    writeCase(reports, 'node-wire-v1@9.9.9')
  })
  assert.equal(code, 0)
  assert.deepEqual(result.unexpected, ['node-wire-v1@9.9.9'])
})

test('a shortened version list narrows the expected set rather than being ignored', () => {
  // The floor is the single source for which versions are still covered; the
  // gate must follow it, or a floor move would leave the gate demanding reports
  // for versions the matrix no longer runs.
  const manifest = JSON.parse(readFileSync(MANIFEST_TWO, 'utf8'))
  const trimmed = join(mkdtempSync(join(tmpdir(), 'psp-manifest-')), 'node-v4.json')
  writeFileSync(trimmed, JSON.stringify({ ...manifest, min_supported: EXTRA_RELEASES.at(-1) }))
  const { code, result } = check(everyCase, { extraArgs: ['--manifest', trimmed] })
  assert.equal(code, 0)
  assert.deepEqual(result.expected, [`node-wire-v1@${EXTRA_RELEASES.at(-1)}`])
})
