import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
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
  // AND IT MUST SAY WHICH ARTIFACTS. An unscoped download reads whatever the run
  // happens to contain, so any artifact a job adds — a build cache, a debug dump —
  // becomes a way for this gate to fail while its own evidence is intact.
  assert(
    /pattern: 'node-\*-evidence'/.test(gate),
    'the gate must download only its own evidence: an unscoped download depends on every artifact in the run being extractable',
  )
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

// One step of a job, by its name, up to the next step: the name line, its
// options and its script.
function step(jobBlock, name) {
  const start = jobBlock.indexOf(`      - name: ${name}\n`)
  assert(start >= 0, `missing step "${name}"`)
  const tail = jobBlock.slice(start + 1)
  const next = /^      - /m.exec(tail)
  return tail.slice(0, next?.index ?? tail.length)
}

// THE SHIPPED IMAGE HAS TO START, NOT ONLY PRINT ITS VERSION. Every other check in
// the container job runs `--version`, which exits before config generation, the
// seed, the database or the bind, and every Go job compiles against the .gitkeep
// placeholder, so the SPA inside the binary is seen nowhere else. The step must go
// through the real entrypoint (the privilege drop is part of what boots), prove the
// module the page loads is served as well as the page, and stop the panel the way
// compose does.
test('the container job boots the source image and fetches the SPA it embeds', () => {
  const boot = step(job('container'), 'The image boots on a fresh SQLite and serves the SPA it embeds')
  assert(/docker run -d [^]*psp-source-check\)/.test(boot), 'the boot step must start the source image detached')
  assert(!boot.includes('--entrypoint'), 'the boot step must go through the image entrypoint, which is what drops to the panel user')
  assert(boot.includes('/health'), 'the boot step must wait on the health endpoint')
  assert(boot.includes('<script type="module"'), 'the boot step must check that / is the built SPA, not the placeholder')
  assert(boot.includes('"$base/$entry"'), 'the boot step must fetch the module the page loads, not only the page')
  assert(boot.includes('stat -c %u /app/data/panel.db)" = 10001'), 'the boot step must prove the database was created by the unprivileged user')
  assert(boot.includes("{{.State.ExitCode}}') = 0") || boot.includes(`{{.State.ExitCode}}' "$cid")" = 0`), 'the boot step must stop the panel and require a clean exit')
})

// THE REAL PANELS ARE THE CEILINGS A HUMAN REVIEWED, AND THEY MOVE TOGETHER.
//
// third-party-isolated floated on :latest while the launcher and its README said
// the caller pins, so a red run on main could mean a broken adapter or an upstream
// release, and its evidence could not say which. It runs the reviewed ceilings now,
// by tag and digest. Raising max_tested_xui or max_tested_sui without the image
// (or the reverse) fails here, because Dependabot does not read a workflow's env
// and nothing else would notice the two drifting apart.
function highestVersion(versions) {
  const parse = (v) => v.split('.').map(Number)
  return versions.reduce((best, v) => {
    const [a, b] = [parse(v), parse(best)]
    for (let i = 0; i < Math.max(a.length, b.length); i++) {
      if ((a[i] ?? 0) !== (b[i] ?? 0)) return (a[i] ?? 0) > (b[i] ?? 0) ? v : best
    }
    return best
  })
}

test('the isolated third-party panels are pinned to the recorded ceilings', () => {
  const readCompat = (file) => JSON.parse(readFileSync(new URL(`../docs/compat/${file}`, import.meta.url), 'utf8'))
  const ceilings = {
    PSP_LIVE_3XUI_IMAGE: {
      repo: 'ghcr.io/mhsanaei/3x-ui',
      version: highestVersion(readCompat('3x-ui-v4.json').entries.map((entry) => entry.max_tested_xui)),
    },
    PSP_LIVE_SUI_IMAGE: {
      repo: 'ghcr.io/alireza0/s-ui',
      version: highestVersion(readCompat('sui-v4.json').sui_entries.map((entry) => entry.max_tested_sui)),
    },
  }
  const isolated = job('third-party-isolated')
  for (const [name, { repo, version }] of Object.entries(ceilings)) {
    const pinned = new RegExp(`^      ${name}: \\$\\{\\{ vars\\.${name} \\|\\| '([^']+)' \\}\\}$`, 'm').exec(isolated)
    assert(pinned, `third-party-isolated must set ${name} once, at job level, with a pinned default`)
    assert.match(
      pinned[1],
      new RegExp(`^${repo.replace(/[.]/g, '\\.')}:v${version.replace(/[.]/g, '\\.')}@sha256:[0-9a-f]{64}$`),
      `${name} must default to ${repo}:v${version}@sha256:…, the ceiling docs/compat records: a floating tag turns main red on an upstream release, and a stale one tests a panel nobody claims`,
    )
  }
  assert(
    isolated.includes('> evidence/images.txt'),
    'the evidence must record which images ran, or a verdict from a repository-variable override cannot be told from one on the pins',
  )
})

// A BUILD TAG IS A FILE THE PLAIN `go vet ./...` NEVER OPENS.
//
// The reinstall acceptance is behind `node_reinstall_acceptance`, and its only compile
// was a path-filtered workflow whose list named six of the fifteen packages it
// imports — so a signature change in the other nine merged green and broke the case
// silently. go_static vets it with its tag now. This keeps that true for the next
// tagged suite as well, wherever it is put: every tracked Go file with a build
// constraint must be opened by a `go vet` line in go_static whose tags satisfy that
// constraint on the job's runner (linux/amd64, gc, cgo), or its compile depends on a
// filter happening to fire. The plain `go vet ./...` counts, with no tags, so a file
// that is merely linux-only is covered by it.
function buildConstraintHolds(expression, tags) {
  const satisfied = new Set(['linux', 'unix', 'amd64', 'gc', 'cgo', ...tags])
  let rest = expression.trim()
  let program = ''
  while (rest) {
    const token = /^(\(|\)|!|&&|\|\||[A-Za-z0-9_.]+)\s*/.exec(rest)
    assert(token, `cannot read the build constraint "${expression}"`)
    const word = token[1]
    program += /^[A-Za-z0-9_.]+$/.test(word) ? ` ${satisfied.has(word) || /^go1\.\d+$/.test(word)} ` : word
    rest = rest.slice(token[0].length)
  }
  // Only true, false and the operators Go shares with JavaScript reach this.
  return Function(`return (${program})`)()
}

function packagePatternCovers(pattern, dir) {
  const path = pattern.replace(/^\.\//, '')
  if (path === '...') return true
  if (path.endsWith('/...')) {
    const prefix = path.slice(0, -'/...'.length)
    return dir === prefix || dir.startsWith(`${prefix}/`)
  }
  return path === dir
}

test('every build-tagged Go file is type-checked by go_static', () => {
  const root = fileURLToPath(new URL('../', import.meta.url))
  // TRACKED FILES, NOT A WALK OF THE TREE, so an installed node_modules or a
  // scratch checkout cannot add Go files that are nobody's to vet. vendor/ and
  // testdata/ are left out for the reason `./...` leaves them out, and `ignore` is
  // the constraint a file uses to ask to be left out of every build, vet included.
  const tagged = execFileSync('git', ['ls-files', '-z', '--', '*.go'], { cwd: root, encoding: 'utf8' })
    .split('\0')
    .filter((file) => file !== '' && !/(^|\/)(vendor|testdata)\//.test(file))
    .flatMap((file) => {
      const constraint = /^\/\/go:build (.+)$/m.exec(readFileSync(join(root, file), 'utf8'))
      if (!constraint || constraint[1].trim() === 'ignore') return []
      return [{ dir: file.includes('/') ? file.slice(0, file.lastIndexOf('/')) : '.', file, constraint: constraint[1] }]
    })
  assert(
    tagged.length > 0,
    'no tracked Go file has a build constraint, so this checked nothing: if the reinstall acceptance is gone, its vet step in go_static and this guard go with it',
  )
  const vets = [...job('go_static').matchAll(/go vet (?:-tags (\S+) )?(\.\/\S*)/g)].map(([, tags, pattern]) => ({
    tags: tags ? tags.split(',') : [],
    pattern,
  }))
  for (const { dir, file, constraint } of tagged) {
    assert(
      vets.some(({ tags, pattern }) => packagePatternCovers(pattern, dir) && buildConstraintHolds(constraint, tags)),
      `${file} is built only under "${constraint}", and no \`go vet\` line in go_static opens it. Its compile then depends on a path-filtered workflow happening to run, which is how a broken acceptance merges green.`,
    )
  }
  // The evaluator itself, on the cases that matter: a tag vet does not pass, and a
  // platform go_static's runner is not.
  assert.equal(buildConstraintHolds('linux && node_reinstall_acceptance', ['node_reinstall_acceptance']), true)
  assert.equal(buildConstraintHolds('linux && node_reinstall_acceptance', []), false)
  assert.equal(buildConstraintHolds('darwin && node_reinstall_acceptance', ['node_reinstall_acceptance']), false)
  assert.equal(buildConstraintHolds('!windows && (foo || bar)', ['bar']), true)
})

// A PACKAGE THAT BRANCHES ON THE DIALECT HAS TO BE RUN ON THE DIALECTS.
//
// The traffic rollup chose its upsert by `Dialector.Name()`, and for as long as its
// tests ran on SQLite only, the MySQL branch was proven by a DryRun of its text —
// the same branch whose empty ON DUPLICATE KEY UPDATE had already failed in
// production. The postgres and mysql lanes run it now. This keeps that true for the
// next package that asks which database it is on: any Go file outside sqlstore that
// reads the dialect must be in a package both lanes test.
test('every package that branches on the SQL dialect is tested by both dialect lanes', () => {
  const root = fileURLToPath(new URL('../', import.meta.url))
  const branching = execFileSync('git', ['ls-files', '-z', '--', '*.go'], { cwd: root, encoding: 'utf8' })
    .split('\0')
    .filter((file) => file !== '' && !file.endsWith('_test.go') && !/(^|\/)(vendor|testdata)\//.test(file))
    .filter((file) => !file.startsWith('internal/adapters/sqlstore/'))
    .filter((file) => /\bDialector\.Name\(\)/.test(readFileSync(join(root, file), 'utf8')))
    .map((file) => file.slice(0, file.lastIndexOf('/')))
  assert(
    branching.includes('internal/service/rollup'),
    'the rollup no longer reads the dialect, so this guard checked nothing: drop it from the lanes and from here together',
  )
  for (const lane of ['postgres', 'mysql']) {
    const command = /go test -count=1 -timeout=\S+ ((?:\.\/\S+ )+)2>&1/.exec(job(lane))
    assert(command, `the ${lane} lane must run go test over named packages`)
    const patterns = command[1].trim().split(/\s+/)
    assert(patterns.includes('./internal/adapters/sqlstore/...'), `the ${lane} lane must still test sqlstore`)
    for (const dir of new Set(branching)) {
      assert(
        patterns.some((pattern) => packagePatternCovers(pattern, dir)),
        `${dir} branches on the SQL dialect and the ${lane} lane does not test it, so only SQLite ever executes that branch`,
      )
    }
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

// THE STATIC JOB'S BUILD CACHE KEY NAMES EVERY TOOL IT COMPILES, AT ITS PIN.
//
// A cache key is written once. Keyed on go.sum alone, go_static's entry is the one
// main saved after go.sum last moved; every later run restores it and no later save
// can add to it, so a tool added or bumped since compiles from source on every run,
// on every event, until go.sum happens to change. With each `go run` and
// `go install` pin in the key, the change that moves a pin moves the key, and the
// next main push saves a cache that has built it.
test('go_static\'s build cache key names every tool the job compiles, at its pin', () => {
  const steps = job('go_static').replace(/^\s*#.*$/gm, '')
  const keys = [...steps.matchAll(/^ +key: (go-build-static-.+)$/gm)].map((m) => m[1])
  assert.equal(keys.length, 2, 'go_static restores one build cache and saves it')
  assert.equal(keys[0], keys[1], 'go_static must save under the key it restores, or no run ever hits it')
  const tools = [...steps.matchAll(/\bgo (?:run|install) \S*\/([a-z0-9-]+)@(v\d+\.\d+\.\d+)/g)]
  assert(tools.length >= 3, `go_static compiles actionlint, staticcheck and govulncheck at pinned versions; found ${tools.length}`)
  for (const [, tool, version] of tools) {
    assert(
      keys[0].includes(`-${tool}-${version}-`) || keys[0].endsWith(`-${tool}-${version}`),
      `go_static compiles ${tool}@${version} and its build cache key does not name it: the key is written once per go.sum, so no main save would ever hold that build`,
    )
  }
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
    /tests=\$\(go test (?:-race )?-list '\^Test' "\$heavy"/.test(race),
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

  // THE CONTAINER JOB HAS NO LAYER CACHE, AND THAT IS A MEASUREMENT.
  //
  // #223 and #225 gave its source image a type=gha cache, and the job got slower: 137s
  // before, 169s on a PR and 202s on main after. The time is the go build — 74s on
  // every run — and it sits after `COPY . .`, so every commit invalidates it and no
  // layer cache can hold it; the layer the cache did hold took as long to import as to
  // download, and main paid ~52s to export it. A cache that comes back here has to
  // cache the Go compile itself, and has to beat those numbers. Comments are stripped
  // first, because the workflow's own comment names the thing this rejects.
  const container = job('container')
  const containerSteps = container.replace(/^\s*#.*$/gm, '')
  assert(
    !/type=gha/.test(containerSteps),
    'the container job must not use a type=gha layer cache: its 74s go build sits after COPY . . and is invalidated by every commit, so the cache made the job 30-65s slower rather than faster',
  )
  // `--load` IS KEPT ON THE RELEASE BUILD. The default builder's docker driver loads by
  // itself, but a container-driver builder leaves the result out of the daemon's store
  // unless it is asked — which is where every check below reads it — so the flag is
  // what keeps the line right if such a builder is ever set up in this job again.
  assert(
    container.includes('docker buildx build --load -f Dockerfile.release'),
    'the release image build must pass --load, or the runtime checks cannot find the image it just built',
  )
})

// SETUP-GO'S OWN CACHE IS OFF, AND THE MODULE CACHE HAS ONE WRITER.
//
// `cache: true` on setup-go restored ~372 MB, the module cache and a GOCACHE, under one
// key shared by every job and saved on ANY event by whichever job finished first, so it
// sat outside the rule above: a PR that changed go.sum wrote ~400 MB into its own scope,
// and the frozen GOCACHE was extracted over each job's own build cache. It is `false` in
// every job, written out because the action's default is true. The module cache is one
// key restored everywhere, and only go_static writes it, after govulncheck has loaded the
// widest module set any job needs.
test('setup-go caches nothing, and only go_static saves the module cache, after govulncheck', () => {
  const modKey = "go-mod-${{ runner.os }}-${{ hashFiles('go.sum') }}"
  const savers = []
  let setups = 0
  for (const [name, raw] of jobs()) {
    const steps = raw.split(/^      - /m).slice(1)
    for (const step of steps) {
      if (step.startsWith('uses: actions/setup-go@')) {
        setups += 1
        assert(
          /^          cache: false$/m.test(step),
          `${name}'s setup-go must say cache: false. Its default is true, which restores one frozen module+build archive shared by every job and saves it on any event, pull requests included`,
        )
      }
      if (!/^          path: ~\/go\/pkg\/mod$/m.test(step)) continue
      assert(step.includes(`key: ${modKey}`), `${name} must use the one module cache key, ${modKey}`)
      if (step.startsWith('uses: actions/cache/save@')) {
        assert(
          step.includes("if: github.event_name == 'push' && github.ref == 'refs/heads/main'"),
          `${name} saves the module cache without gating it on the default branch`,
        )
        savers.push(name)
      }
    }
    if (raw.includes('path: ~/.cache/go-build')) {
      assert(
        raw.includes('path: ~/go/pkg/mod'),
        `${name} restores a build cache but not the module cache, so it downloads every module on every run`,
      )
    }
  }
  assert(setups >= 9, `every Go job in this file sets up Go; found ${setups} setup-go steps`)
  assert.deepEqual(savers, ['go_static'], 'the module cache must have exactly one writer, go_static: a key cannot be written twice, so a second writer races it and freezes whichever subset lands first')
  const staticJob = job('go_static')
  assert(
    // Its restore comes first, so the last mention of the path is the save.
    staticJob.lastIndexOf('path: ~/go/pkg/mod') > staticJob.indexOf('name: Reachable vulnerability check'),
    'go_static must save the module cache after govulncheck, or the saved set lacks the modules the scans load',
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

// A HANG MUST BE KILLED BY GO TEST, WHICH PRINTS THE GOROUTINE DUMP, AND NOT BY
// THE RUNNER, WHICH PRINTS NOTHING. The race step runs its invocations one after the
// other, so their -timeout budgets add up, and they have to leave the job room for
// setup and a cold compile: two budgets of 8m inside a 10-minute job meant a hang in
// the second half ended as a bare cancellation, with no dump and no uploaded output.
test('the race shard\'s go test budgets fit inside its job timeout', () => {
  const race = job('race-shard')
  const jobMinutes = /^    timeout-minutes: (\d+)$/m.exec(race)
  assert(jobMinutes, 'the race shard must set timeout-minutes')
  const budgets = [...race.matchAll(/go test -race -count=1 -timeout=(\d+)m /g)].map(([, minutes]) => Number(minutes))
  assert(budgets.length >= 2, 'the race step must bound both of its go test invocations with -timeout')
  const total = budgets.reduce((sum, minutes) => sum + minutes, 0)
  assert(
    total + 2 <= Number(jobMinutes[1]),
    `the race step's -timeout budgets add up to ${total}m in a ${jobMinutes[1]}-minute job: with setup and a cold compile the runner's limit comes first, and a hang ends without its goroutine dump`,
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

// THE REQUIRED SMOKE RUNS ON THE PINNED BROWSER. It probed for the runner image's
// google-chrome while a pinned Chromium was installed one step later, so the browser
// under a required check changed with GitHub's image rather than with a commit.
// The install has to come first and hand its binary to the smoke.
test('the production smoke runs on the Chromium the job pins, installed before it', () => {
  const web = job('web')
  const install = web.indexOf('      - name: Install the pinned Playwright and its Chromium\n')
  const smoke = web.indexOf('      - name: production browser smoke\n')
  assert(install >= 0 && smoke >= 0, 'the web job must install the pinned Playwright and run the smoke')
  assert(install < smoke, 'the pinned browser must be installed before the smoke, or the smoke falls back to the runner image\'s Chrome')
  assert(
    step(web, 'production browser smoke').includes('CHROME_PATH: ${{ steps.browser.outputs.chrome }}'),
    'the smoke must be pointed at the pinned browser through CHROME_PATH',
  )
  assert(
    step(web, 'Install the pinned Playwright and its Chromium').includes('test -x "$chrome"'),
    'the install step must prove the browser it hands over exists, or a layout change would silently send the smoke back to the runner\'s Chrome',
  )
})

// THE HOOK LINT RUNS, AND IT CAN FAIL. The rules only exist when the react plugin
// is named in the config: without it oxlint passes having checked nothing, which is
// the one failure a lint step cannot report about itself.
test('the web job lints the React hook rules, with rules-of-hooks as an error', () => {
  const web = job('web')
  assert(step(web, 'lint (react hooks)').includes('npm run lint'), 'the web job must run the hook lint')
  const pkg = JSON.parse(readFileSync(new URL('../web-react/package.json', import.meta.url), 'utf8'))
  assert.match(pkg.scripts.lint ?? '', /^oxlint\b/, 'web-react must have a lint script that runs oxlint')
  assert.match(pkg.devDependencies.oxlint ?? '', /^\d+\.\d+\.\d+$/, 'oxlint must be an exactly pinned devDependency')
  // The config is JSONC; its comments are whole lines, so dropping them is enough.
  const raw = readFileSync(new URL('../web-react/.oxlintrc.json', import.meta.url), 'utf8')
  const config = JSON.parse(raw.split('\n').filter((line) => !/^\s*\/\//.test(line)).join('\n'))
  assert(config.plugins?.includes('react'), 'the react plugin must be enabled, or the hook rules are silently inactive')
  assert.equal(config.rules?.['react-hooks/rules-of-hooks'], 'error', 'rules-of-hooks must fail the lint')
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

// THE CONTRACT JOB DOES NOT RE-RUN THE MATRIX, AND IT DOES NOT LOSE ITS OWN WORK.
//
// When contract_source is one of the planned cases, node-compatibility has already run
// the same four wire tests against the same commit in the same run, and the gate
// requires that job. So the wire suite is skipped there, and only there: matched by
// commit, the identity both jobs verify. The three cross-repository packages are this
// job's own and always run, and every verdict is collected, so neither half can hide
// the other. The identity file is written before any of it, because a failure is the
// only time anyone reads the evidence. The match itself is run, not read: the program
// is lifted out of the workflow and fed the real plan.
test('the contract job skips only a wire run the matrix already made, and always runs its own contracts', () => {
  const contract = step(job('node-contract'), 'real Node sync, durable result receipt and task expiry')
  const identity = contract.indexOf('> "$evidence/environment.json"')
  const firstTest = contract.indexOf('go test')
  assert(identity >= 0 && identity < firstTest, 'environment.json must be written before any test can fail the step')
  assert(contract.includes('wire_covered_by'), 'the evidence must say when, and by which case, the wire run was covered')
  const wire = contract.indexOf("-run '^TestLive_RealNode(")
  const branch = contract.indexOf('if [ -n "$covered_by" ]; then')
  const drift = contract.indexOf('./internal/version/ ./internal/pkg/nodebootstrap/ ./internal/adapters/corecatalogdoc/')
  const fi = contract.indexOf('\n          fi\n')
  assert(branch >= 0 && branch < wire && wire < fi, 'the wire suite must be the only thing the covered-by branch skips')
  assert(drift > fi, 'the cross-repository packages must run whatever the branch decided')
  assert(/corecatalogdoc\/ \|\| failed=1/.test(contract), 'a cross-repository failure must be collected, not end the step before the verdict')
  assert(/--output "\$evidence\/result\.json" \|\| failed=1/.test(contract), 'a wire failure must be collected, not hide the cross-repository contracts')
  assert(contract.trimEnd().endsWith('exit "$failed"'), 'the step must report the failures it collected')

  const matcher = /python3 -c '([^']+)' "\$source_commit"/.exec(contract)
  assert(matcher, 'the covered-by match must be a python3 program over the plan, keyed on $source_commit')
  const root = fileURLToPath(new URL('../', import.meta.url))
  const plan = execFileSync('node', ['deploy/compat/plan.mjs', '--emit', 'cases'], { cwd: root, encoding: 'utf8' })
  const cases = JSON.parse(plan)
  assert(cases.length > 0, 'the plan emitted no case, so the match was not exercised')
  const match = (sha) => execFileSync('python3', ['-c', matcher[1], sha], { input: plan, encoding: 'utf8' }).trim()
  for (const planned of cases) assert.equal(match(planned.sha), planned.id, `a planned commit must be recognised as ${planned.id}`)
  assert.equal(match('0'.repeat(40)), '', 'a commit outside the plan must not be treated as covered, or its wire run is silently dropped')
})

// A JOB WITHOUT A TIMEOUT HOLDS ITS RUNNER FOR 360 MINUTES WHEN SOMETHING HANGS, and
// for a required check that is six hours of a red nobody can read. Every job here
// states its own bound.
test('every job in test.yml sets its own timeout', () => {
  for (const [name, raw] of jobs()) {
    assert(/^    timeout-minutes: \d+$/m.test(raw), `${name} has no timeout-minutes, so a hang holds the runner for GitHub's 360-minute default`)
  }
})

// The real-panel suites run one after the other, so their -timeout budgets add up and
// must leave the job room to start the panels: otherwise a hang in the first suite is
// ended by the runner, without go test's goroutine dump and before the validator can
// name it.
test('the real-panel suites\' go test budgets fit inside their job timeout', () => {
  const isolated = job('third-party-isolated')
  const jobMinutes = Number(/^    timeout-minutes: (\d+)$/m.exec(isolated)[1])
  const budget = Number(/go test -json -count=1 -timeout=(\d+)m "\$pkg"/.exec(isolated)?.[1])
  const suites = (isolated.match(/^\s*run_suite third-party-/gm) ?? []).length
  assert(budget > 0 && suites >= 2, 'the real-panel job must bound each suite with -timeout and run both suites')
  assert(
    budget * suites + 5 <= jobMinutes,
    `${suites} suites of ${budget}m in a ${jobMinutes}-minute job leave no room for pulling and starting the panels`,
  )
})

// THE LINTERS RUN, AND THE ONE THAT DEPENDS ON THE OTHER CAN SEE IT. actionlint
// shellchecks run: blocks only when shellcheck is on PATH and is silent when it is
// not, so the step asserts it first; the standalone scripts are listed from git so a
// new one is linted by construction.
test('go_static lints the workflows with actionlint and the tracked shell scripts with shellcheck', () => {
  const lint = step(job('go_static'), 'Workflow and shell lint')
  assert.match(lint, /go run github\.com\/rhysd\/actionlint\/cmd\/actionlint@v\d+\.\d+\.\d+\n/, 'actionlint must run at a pinned version')
  const probe = lint.indexOf('command -v shellcheck')
  assert(probe >= 0 && probe < lint.indexOf('actionlint@'), 'shellcheck must be asserted before actionlint, which skips it silently when it is missing')
  assert(lint.includes("git ls-files -z '*.sh' | xargs -0 shellcheck"), 'every tracked .sh file must be shellchecked')
  assert(!/shellcheck -S (warning|error)/.test(lint), 'the scripts are clean at every severity, and SC2086 is "info": raising the floor would stop checking quoting')
})
