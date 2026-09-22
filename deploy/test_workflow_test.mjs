import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

// test.yml had no guard of its own. release_workflow_test.mjs checks the release
// workflow's scripts and its needs graph, and nothing did the same for the test
// workflow — which is how a heredoc terminator that never reached column zero
// got as far as a syntax check. That one was real: nested inside a loop, it kept
// the indentation the block scalar left it and the script never closed.
//
// The workflow is read as text rather than parsed as YAML, matching the release
// guard, so these assertions keep working on a machine that has no YAML library.

const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')

function job(name) {
  const marker = `  ${name}:\n`
  const start = workflow.indexOf(marker)
  assert(start >= 0, `missing ${name} job`)
  const tail = workflow.slice(start + marker.length)
  const next = /^  [a-z][a-z0-9_-]*:\n/m.exec(tail)
  return tail.slice(0, next?.index ?? tail.length)
}

// Every `run: |` block, with the indentation the block scalar leaves behind
// already stripped the way the runner strips it.
function literalScripts() {
  const lines = workflow.split('\n')
  const scripts = []
  for (let i = 0; i < lines.length; i++) {
    const match = /^(\s*)run: \|$/.exec(lines[i])
    if (!match) continue
    const indent = match[1].length + 2
    const body = []
    while (++i < lines.length) {
      if (lines[i].trim() === '') { body.push(''); continue }
      if (lines[i].search(/\S/) < indent) { i--; break }
      body.push(lines[i].slice(indent))
    }
    // GitHub expressions are not shell; stand in for them so the rest can be
    // parsed. Matrix values are trusted workflow metadata, never evaluated here.
    scripts.push({ line: match.index, body: body.join('\n').replace(/\$\{\{[^}]+\}\}/g, 'fixture') })
  }
  return scripts
}

test('every run script in test.yml parses as shell', () => {
  const scripts = literalScripts()
  assert(scripts.length > 5, `expected the workflow to have run scripts, found ${scripts.length}`)
  const failures = []
  for (const script of scripts) {
    const result = spawnSync('bash', ['-n'], { input: script.body, encoding: 'utf8' })
    if (result.status !== 0) failures.push((result.stderr || '').trim())
  }
  assert.deepEqual(failures, [], 'a run script does not parse; the job would fail before doing anything')
})

// THE SUMMARY GATE MUST EXIST AND MUST NOT BE SKIPPABLE. A job with `needs` and
// no `if: always()` simply does not run when a dependency fails, and a check
// that never runs reads as absent rather than as failed.
test('the compatibility gate is unconditional and covers both Node jobs', () => {
  const gate = job('compatibility')
  assert(gate.includes('if: always()'), 'the gate must run even when a leg failed or was cancelled')
  assert(/needs: \[[^\]]*node-compatibility[^\]]*\]/.test(gate), 'the gate must summarise the released-range job')
  assert(/needs: \[[^\]]*node-contract[^\]]*\]/.test(gate), 'the gate must summarise the pinned-source job')
  assert(gate.includes('deploy/compat/check-case-set.mjs'), 'the gate must check the expected case set')
  assert(gate.includes('actions/download-artifact'), 'the gate must read the uploaded evidence')
})

// The suite that protects the gate has to run somewhere. This asserts the
// checker tests are wired into the job that already runs the deploy guards, so a
// renamed or dropped file cannot quietly stop being executed.
test('the deploy guard suites are all executed by the container job', () => {
  const container = job('container')
  for (const suite of [
    'deploy/check_image_test.mjs',
    'deploy/check_build_test.mjs',
    'deploy/release_workflow_test.mjs',
    'deploy/compat/check-go-results.test.mjs',
    'deploy/compat/check-case-set.test.mjs',
    'deploy/compat/plan.test.mjs',
    'deploy/compat/evidence-index.test.mjs',
    'deploy/compat/contract-source.test.mjs'
  ]) {
    assert(container.includes(suite), `${suite} is not run by any job`)
  }
})

// Every job in this file, by name, as its raw block — the same helpers the
// release guard carries, for the same reason: an invariant over the graph needs
// the graph.
function jobs() {
  const start = workflow.indexOf('\njobs:\n')
  assert(start >= 0, 'the test workflow defines jobs')
  const body = workflow.slice(start + '\njobs:\n'.length)
  const markers = [...body.matchAll(/^  ([a-z][a-z0-9_-]*):\n/gm)]
  const found = new Map()
  markers.forEach((marker, index) => {
    const end = index + 1 < markers.length ? markers[index + 1].index : body.length
    found.set(marker[1], body.slice(marker.index, end))
  })
  assert(found.size > 0, 'the test workflow defines jobs')
  return found
}

// `needs` is written both ways in this file: a bare name, and a bracketed list.
function needsOf(raw) {
  const listed = /^    needs: \[(.*)\]$/m.exec(raw)
  if (listed) return listed[1].split(',').map((name) => name.trim().replace(/['"]/g, ''))
  const single = /^    needs: ([A-Za-z0-9_-]+)$/m.exec(raw)
  return single ? [single[1]] : []
}

// THE BUG THAT COST A RELEASE, APPLIED TO THE WORKFLOW EVERYBODY TOUCHES.
//
// A skipped job skips its whole downstream chain, transitively, and `always()` on
// a job in between does not restore the jobs under it: the exempted job runs and
// reports success while its dependents are skipped anyway. The reproduction is in
// release_workflow_test.mjs; this is the same rule stated for this graph.
//
// A job-level condition here is allowed only when the job is a leaf (nothing
// needs it), or when it carries `always()` — which is what lets the gates report
// on a chain that did not succeed instead of being skipped by it.
test('a job that can be skipped by its own condition has nothing below it', () => {
  const all = jobs()
  for (const [name, raw] of all) {
    const condition = /^    if: (.*)$/m.exec(raw)
    if (!condition || condition[1].includes('always()')) continue
    const dependents = [...all]
      .filter(([other, block]) => other !== name && needsOf(block).includes(name))
      .map(([other]) => other)
    assert.deepEqual(
      dependents,
      [],
      `${name} can be skipped by its own condition (${condition[1]}) and is needed by ${dependents.join(', ')}. A skipped job skips its whole downstream chain, transitively, and always() on the job in between does not restore it — gate a step instead, or make the job a leaf.`,
    )
  }
})

// ONE JOB MUST PRODUCE THE REQUIRED `build (cross-compile release targets)`
// CONTEXT, AND IT MUST COMPILE EVERY TARGET.
//
// It was a six-leg matrix plus a one-line gate, which spent seven slots to compile
// six binaries and made the required context the name of the job that did nothing.
// These assertions keep the two properties the merge is worth having: the job that
// carries the required name is the job that compiles, and every release target is
// still in it. A target dropped from the list would be a platform that stops being
// checked here — silently, since the remaining five would still compile.
test('the build job compiles every release target, and reports all of them', () => {
  const build = job('build')
  for (const target of [
    'linux/amd64',
    'linux/arm64',
    'darwin/amd64',
    'darwin/arm64',
    'windows/amd64',
    'windows/arm64',
  ]) {
    assert(build.includes(target), `the build job must still compile ${target}`)
  }
  assert(
    !/^    strategy:/m.test(build),
    'the required build context must be one job: a matrix reports as several checks, and branch protection needs the one stable name',
  )
  // NOT `set -e`. The matrix ran with `fail-fast: false` so one bad target could
  // not hide the others; a loop with `set -e` would put that back.
  assert(
    /set -uo pipefail/.test(build),
    'the cross-compile loop must not stop at the first failing target — the matrix ran with fail-fast: false, and that is what the loop replaces',
  )
  assert(!/set -euo pipefail/.test(build), 'set -e in the cross-compile loop aborts before the remaining targets are tried')
  assert(build.includes('failed=1'), 'a failing target must be recorded rather than ending the step')
  assert(build.includes('exit "$failed"'), 'the step must report the failures it collected')
})

// The pinned-source contract job must not learn which Node revision to test from
// go.mod. Doing so made the evidence move with every dependency bump, and would
// have removed the job's entry point the moment the PN root module was dropped —
// the test would have gone with the dependency rather than outliving it. The
// revision is named in docs/compat/verification-v1.json instead.
test('the contract job takes its Node revision from the manifest, not from go.mod', () => {
  const contract = job('node-contract')
  assert(
    contract.includes('deploy/compat/contract-source.mjs'),
    'the contract job must read its pinned source from the manifest',
  )
  assert(
    !/go list -m[^\n]*passwall-node/.test(contract),
    'the contract job must not resolve the Node module through go list (that is go.mod again, one step removed)',
  )
  assert(
    contract.includes('rev-parse HEAD') && contract.includes('= "$source_commit"'),
    'the contract job must assert the checked-out commit IS the pinned one, or a moved tag passes silently',
  )
})
