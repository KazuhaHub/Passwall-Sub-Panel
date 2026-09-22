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

// ONE PACKAGE IS HALF THE RACE SUITE, AND PACKAGES CANNOT BALANCE IT.
//
// internal/adapters/sqlstore is 174s of the roughly 320s the weights file records,
// so longest-processing-time packing gives it a shard to itself: measured on main,
// shard 1 took 229s of test time while its siblings took 93s, 145s and 114s, and
// the run waited on it. No arrangement of PACKAGES shortens a package longer than
// a quarter of the suite, so its TESTS are partitioned instead.
//
// THE DANGEROUS FAILURE IS SILENT. `go test -run` matching nothing exits 0, so a
// broken partition here is a race check that goes green having executed less than
// it claims — which is exactly the failure this job cannot report about itself.
// The assertions below pin the parts that make it safe: the package is excluded
// from the partition (or it would run twice), both halves run even when one fails,
// and the round-robin is a real partition — checked by running the shipped awk
// program over a fixture, not by reading it.
test('the race shards partition the heavy package\'s tests, and cover them all', () => {
  const race = job('race-shard')
  const heavy = /^\s*heavy=(\S+)$/m.exec(race)
  assert(heavy, 'the race job must name the heavy package it takes out of the partition')
  assert(
    race.includes('--exclude "$heavy"'),
    'the heavy package must be excluded from the package partition, or it is tested twice — once whole and once in slices',
  )
  assert(
    race.includes('-run "^(${regex})$" "$heavy"'),
    'the heavy package must be run as a slice of its tests, not skipped',
  )
  assert(
    /tests=\$\(go test -list '\^Test' "\$heavy"/.test(race),
    'the slice must come from the package\'s own test list, so a test added tomorrow is in a shard by construction',
  )
  // A FAILING HALF MUST NOT HIDE THE OTHER. The two invocations are separate
  // because they answer separate questions; `set -e` would end the step on the
  // first, which is why the failure is collected rather than propagated.
  assert(race.includes('|| failed=1'), 'both halves must run even when the first fails')
  assert(race.includes('exit "$failed"'), 'the step must report the failures it collected')
  // `set -e` is what makes `test -n` guards mean anything: without it they do not
  // stop the step, and an empty planner result reaches `go test` with no arguments.
  assert(
    race.includes('set -euo pipefail'),
    'the step must fail on the first unexpected error, or its guards are decorative',
  )

  // AND THE ROUND-ROBIN IS A PARTITION, PROVEN RATHER THAN ASSUMED. The program is
  // read out of the workflow and run over a fixture the size of the real test
  // list: every element must land in exactly one shard, and every shard must print
  // something.
  const roundRobin = /awk -v s="\$\{\{ matrix\.part \}\}" -v m=(\d+) '([^']+)'/.exec(race)
  assert(roundRobin, 'the round-robin that splits the heavy package\'s tests must be in the job')
  const shards = Number(roundRobin[1])
  const program = roundRobin[2]
  const fixture = Array.from({ length: 315 }, (_, i) => `TestFixture${String(i).padStart(3, '0')}`)
  const placed = new Map()
  for (let s = 1; s <= shards; s++) {
    const result = spawnSync('awk', ['-v', `s=${s}`, '-v', `m=${shards}`, program], {
      input: `${fixture.join('\n')}\n`,
      encoding: 'utf8',
    })
    assert.equal(result.status, 0, `the round-robin failed for shard ${s}: ${result.stderr}`)
    const selected = result.stdout.split('\n').filter(Boolean)
    assert(selected.length > 0, `shard ${s} selected no test: a shard that runs nothing of the heavy package is a shard that proves nothing`)
    for (const name of selected) {
      assert(
        !placed.has(name),
        `${name} is in shard ${placed.get(name)} and shard ${s}: the slices overlap, so at least one of them tests less than it reports`,
      )
      placed.set(name, s)
    }
  }
  assert.equal(
    placed.size,
    fixture.length,
    'the slices do not cover every test: a test in no shard is a test nothing runs, and the job stays green',
  )
})

// A CACHE A PULL REQUEST WRITES IS A CACHE NOBODY CAN READ.
//
// A run restores from its own branch or the default branch and from nothing else,
// and a PR's caches are scoped to refs/pull/N/merge — read by no run but a re-run
// of that same PR. So a save made there is write-only: it costs the runner the
// upload and it takes room from the caches main reads. The combined
// `actions/cache@v4` saves on every event, which is exactly this mistake; the
// restore/save pair is the shape that saves where it can be read.
test('caches are restored on every event and saved only from the default branch', () => {
  const all = jobs()
  let restores = 0
  for (const [name, raw] of all) {
    assert(
      !/uses: actions\/cache@/.test(raw),
      `${name} uses the combined actions/cache, which saves on every event — including a pull request, whose save lands in a scope nothing but that PR's re-runs can read`,
    )
    if (!raw.includes('uses: actions/cache/restore@')) continue
    restores += 1
    const save = raw.indexOf('uses: actions/cache/save@')
    assert(save >= 0, `${name} restores a cache and never saves one, so nothing would warm it`)
    assert(
      /if: github\.event_name == 'push' && github\.ref == 'refs\/heads\/main'/.test(raw.slice(save)),
      `${name} saves a cache without gating it on the default branch: that save is readable by nothing, and a key cannot be written twice`,
    )
  }
  assert(
    restores >= 9,
    `every job that compiles Go, plus the browser download, restores a cache — found ${restores}`,
  )
})

// THE SHARD COUNT LIVES IN THREE PLACES, AND THEY ARE ONE DECISION.
//
// The planner partitions the packages into N, the round-robin splits the heavy
// package's tests into N, and the matrix launches N jobs. Disagreement is silent in
// the worst direction: a matrix smaller than the partition leaves whole shards
// unrun while every job that did run is green.
test('the race shard count is written once and agrees with itself', () => {
  const race = job('race-shard')
  const planner = /--shards (\d+) --shard/.exec(race)
  const roundRobin = /-v m=(\d+) /.exec(race)
  const matrix = /part: \[([0-9, ]+)\]/.exec(race)
  assert(planner, 'the race job must name its shard count where it calls the planner')
  assert(roundRobin, 'the race job must name its shard count in the round-robin')
  assert(matrix, 'the race job must name its shard count in the matrix')
  const counts = new Set([Number(planner[1]), Number(roundRobin[1]), matrix[1].split(',').length])
  assert.equal(
    counts.size,
    1,
    `the shard count is written ${counts.size} different ways (${[...counts].join(', ')}): the packages would be partitioned into one number of shards, the heavy package's tests into another, and the matrix would launch a third`,
  )
})

// The pinned Playwright version appears twice — in the install and in the cache key
// that remembers the browser it downloaded — and a cache key that outlives the
// version it names serves a browser the pinned CLI did not ask for.
test('the browser cache is keyed on the Playwright version the step installs', () => {
  const web = job('web')
  const pinned = /playwright@(\d+\.\d+\.\d+)/.exec(web)
  assert(pinned, 'the web job must pin the Playwright version it installs')
  assert(
    web.includes(`playwright-chromium-\${{ runner.os }}-${pinned[1]}`),
    `the browser cache key must name the pinned Playwright version (${pinned[1]})`,
  )
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
