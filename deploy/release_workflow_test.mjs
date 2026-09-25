import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')

// THE NAMESPACE IS THE ONE THE PRODUCT ALREADY WEARS, AND THE OLD ONE IS CLOSED.
//
// This assertion has reversed twice now, which is why it is kept rather than
// deleted: it first asserted the ABSENCE of `release/*` (while the legacy scheme
// was the only one), then REQUIRED it (once both consumers handled a tag that is
// not its own version), and it requires `v*` and refuses `release/*` now — the
// namespace the first four releases live in, and where nothing new is written.
//
// WHAT MAKES THE CURRENT TRIGGER SAFE IS THAT EVERY READER TAKES EITHER NAMESPACE.
// A release's TAG is not its VERSION — `v4.0.0` names the release `4.0.0` — and
// the consumers that used to read the tag as the version take both identities now:
// the public Node installer reads the version out of the asset name and the address
// out of the release document (Passwall-Node#40), and cmd/compatwatch parses the
// tag before reconciling it (#183). The four releases published under the
// historical namespace are read by that same path.
test('the release workflow triggers on the current tag namespace, and only it', () => {
  const patterns = []
  const lines = workflow.split('\n')
  const start = lines.indexOf('    tags:')
  assert(start >= 0, 'the release workflow must trigger on tag pushes')
  // Read to the end of the block, not to the next non-blank line: comments and
  // blank lines are ordinary YAML here and a parser that stops at one would
  // report a trigger list that is really there.
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() === '' || line.trimStart().startsWith('#')) continue
    if (!/^      - /.test(line)) break
    patterns.push(line.replace(/^\s*- /, '').replace(/^["']|["']$/g, ''))
  }
  assert(
    patterns.includes('v*'),
    `the release workflow does not trigger on v* (found ${patterns.join(', ')}). A tag the workflow does not trigger on is a release that does not happen: no red run, no artifact, and a tag that names nothing.`,
  )
  // AND THE HISTORICAL NAMESPACE IS REFUSED. Four releases live under it and no
  // more can join them, because a published tag cannot be moved and the reading
  // side treats the set as closed — while every address in it carries a slash that
  // a download URL and a Docker tag both read as a separator.
  assert(
    !patterns.includes('release/*'),
    `the release workflow still triggers on release/* (found ${patterns.join(', ')}). That namespace holds the four releases published before the address changed; a tag it does trigger on is a new release published into it.`,
  )
})

// THE TAG AND THE VERSION ARE TWO IDENTITIES, and in the legacy scheme they are
// the same string — which is exactly why using one for the other is invisible
// until the product scheme, where it addresses a different release:
// `release/4.0.0` is a git ref, and `4.0.0` is what the build stamp, the archive
// name and the image tag are made of. PN asserts the same split in
// TestTheReleaseWorkflowKeepsTheTagAndTheVersionApart; this is the PSP half, so
// the two repositories' mechanisms stay the same mechanism.
test('the release workflow keeps the tag and the version apart', () => {
  // One derivation, from the one implementation. A shell `case` or a `${tag#v}`
  // here would be a second copy of a rule whose whole point is that there is one.
  assert(
    workflow.includes('version=$(go run ./cmd/release-tag "$tag")'),
    'the workflow must derive the version from this repository\'s own command, not a second shell rule',
  )
  for (const strip of ['${tag#v}', '${tag##v}']) {
    assert(!workflow.includes(strip), `the workflow strips the v prefix itself (${strip}), which is a second version derivation`)
  }

  // A version belongs in each of these. Every one is a published name for the
  // release's CONTENTS.
  for (const required of [
    'VERSION: ${{ needs.setup.outputs.version }}',
    'org.opencontainers.image.version=${{ needs.setup.outputs.version }}',
    'RELEASE_VERSION: ${{ steps.identity.outputs.version }}',
    'RELEASE_VERSION: ${{ needs.setup.outputs.version }}',
    'type=raw,value=${{ needs.setup.outputs.version }}',
  ]) {
    assert(workflow.includes(required), `a version-bearing surface does not use the version: missing ${required}`)
  }

  // The git ref, the release it publishes, and the shape the channel is read from
  // are the TAG's job. Substituting the version would address a release that is
  // not there — the recheck steps fetch `refs/tags/<x>`, and the failure would
  // read as "the tag changed during the build".
  for (const required of [
    'tag_name: ${{ needs.setup.outputs.tag }}',
    'TAG: ${{ needs.setup.outputs.tag }}',
    'refs/tags/${TAG}',
    'PINNED_TAG: ${{ steps.identity.outputs.tag }}',
  ]) {
    assert(workflow.includes(required), `the published identity is not the tag: missing ${required}`)
  }

  // Both identities have to leave setup, or the jobs below cannot tell which one
  // they were given.
  assert(/^      version: \$\{\{ steps\.identity\.outputs\.version \}\}$/m.test(workflow), 'setup does not export the version')
  assert(/^      tag: \$\{\{ steps\.identity\.outputs\.tag \}\}$/m.test(workflow), 'setup does not export the tag')

  // AND THE CHANNEL IS RESOLVED ONCE. The release body used to decide for itself
  // by testing the version for a hyphen — the legacy shape rule living a second
  // time in a shell script, where a product version (three digits, no hyphen)
  // would take the stable branch and hand the reader a `:latest` command that
  // installs a different build. The one resolved answer is the only input.
  assert(
    !/\$\{VERSION\}"?\s*==\s*\*-/.test(workflow),
    'the workflow decides the channel from a hyphen in the version; read needs.setup.outputs.prerelease instead',
  )
  assert(
    workflow.includes('PRERELEASE: ${{ needs.setup.outputs.prerelease }}'),
    'the release body must read the channel setup resolved',
  )
})

// The `case … esac` block starting at `opener`, with nesting respected.
//
// A NON-GREEDY `[\s\S]*?esac` DOES NOT WORK HERE and this is not a style point: the
// channel arms used to CONTAIN a nested `case`, so the lazy match stopped at the
// inner `esac` and the assertions below read a truncated body — a guard that
// misparses is worse than no guard, because it reports on a region nobody chose.
function caseBlock(text, opener) {
  const start = text.indexOf(opener)
  if (start < 0) return null
  const re = /\b(case|esac)\b/g
  re.lastIndex = start
  let depth = 0
  for (let m = re.exec(text); m; m = re.exec(text)) {
    depth += m[1] === 'case' ? 1 : -1
    if (depth === 0) return text.slice(start, m.index + m[1].length)
  }
  return null
}

// THE DEFAULT IS A PRE-RELEASE, STATED OR NOT. A release published without an
// explicit channel goes out as one, because that is the direction a deliberate
// promotion can correct and the one that cannot move `latest` or /releases/latest
// — pointers consumers follow, which no later edit takes back.
//
// SO THE TAG TEXT NO LONGER DECIDES THE CHANNEL AT ALL. It used to: a plain legacy
// `v*` tag meant STABLE, which made a v-tag the one publishable mistake with an
// unrecoverable half. This test used to PIN that behaviour, on the reasoning that
// the batch introducing the split must not change legacy releases; it is rewritten
// because the policy changed on purpose, and the change is the *removal* of a rule
// rather than a new one.
//
// The image-channel case still reads the tag, and that is about the ADDRESS — is
// this a release ref at all — rather than about the channel.
test('every automatic channel is a pre-release, and stable is stated', () => {
  const setup = job('setup')
  const channel = caseBlock(setup, 'case "${REQUESTED:-auto}" in')
  assert(channel, 'the channel step must resolve the requested channel')
  assert(/stable\)\s*prerelease=false/.test(channel), 'an explicit stable must resolve to stable')
  assert(/auto\)\s*prerelease=true/.test(channel), 'an automatic channel must be a pre-release')
  assert(
    !/PINNED_TAG/.test(channel),
    'the channel reads the tag shape again; that is how a plain v-tag becomes stable by default, and a moved stable pointer is not undone by editing a workflow',
  )
  assert(
    /unknown publication channel/.test(channel),
    'an unrecognised channel input must be refused rather than defaulted',
  )
})



// This intentionally guards a bounded, canonical workflow layout; the Go
// version package additionally parses the full YAML and validates SDK gates.
function job(name) {
  // ANCHORED TO THE LINE START. A plain `indexOf('  tag:\n')` also matches the
  // `    tag:` inside the dispatch form, so a job whose name is also an input key
  // resolved to the input block — and the assertions then read a fragment of the
  // form as if it were the job.
  const marker = new RegExp(`^  ${name}:\\n`, 'm')
  const found = marker.exec(workflow)
  assert(found, `missing ${name} job`)
  const tail = workflow.slice(found.index + found[0].length)
  const next = /^  [a-z][a-z-]*:\n/m.exec(tail)
  return tail.slice(0, next?.index ?? tail.length)
}

// EVERY job in this file, by name, as its raw block. `job(name)` reads one; this
// reads the set, which is what an invariant over the graph needs.
function jobs() {
  const start = workflow.indexOf('\njobs:\n')
  assert(start >= 0, 'the release workflow defines jobs')
  const body = workflow.slice(start + '\njobs:\n'.length)
  const markers = [...body.matchAll(/^  ([A-Za-z_][A-Za-z0-9_-]*):\n/gm)]
  const found = new Map()
  markers.forEach((marker, index) => {
    const end = index + 1 < markers.length ? markers[index + 1].index : body.length
    found.set(marker[1], body.slice(marker.index, end))
  })
  assert(found.size > 0, 'the release workflow defines jobs')
  return found
}

// The lines a block RUNS, without the comments that explain them. Searching a whole
// job finds the sentence that explains a command as readily as the command itself.
function withoutComments(text) {
  return text
    .split('\n')
    .filter((line) => !line.trimStart().startsWith('#'))
    .join('\n')
}

function literalScripts(text = workflow) {
  const lines = text.split('\n')
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
    // Matrix extension is trusted workflow metadata; replace it only for
    // syntax checking, never evaluate GitHub expressions as executable code.
    scripts.push(body.join('\n').replace(/\$\{\{[^}]+\}\}/g, 'fixture'))
  }
  return scripts
}

// The lines a script EXECUTES: no comments, and no heredoc bodies. A heredoc is
// data on its way into a file, and the release notes are one — they tell the
// reader to run `sha256sum -c SHA256SUMS.txt`, and that sentence was the only
// line the checksum guard below ever matched, for as long as no step verified
// anything at all. A here-string (<<<) is not a heredoc and is left alone.
function executedLines(script) {
  const executed = []
  let terminator = null
  for (const line of script.split('\n')) {
    if (terminator !== null) {
      if (line.replace(/^\t+/, '') === terminator) terminator = null
      continue
    }
    if (line.trimStart().startsWith('#')) continue
    executed.push(line)
    const heredoc = /(?<!<)<<(?!<)-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)\1/.exec(line)
    if (heredoc) terminator = heredoc[2]
  }
  return executed
}

// Floor for actions/setup-go and actions/setup-node. Below this the inputs this
// guard relies on (cache: false, package-manager-cache: false) are not
// available, so an older major is a real failure rather than a version bump.
const MIN_SETUP_ACTION_MAJOR = 6

function assertPublisherCachesDisabled(raw) {
  assert(!raw.includes('actions/cache@'), 'publisher must not use a shared cache action')
  assert(!/^\s*cache-(?:from|to):/m.test(raw), 'publisher must not import/export a shared container build cache')
  // The major is matched, not pinned: pinning it meant every Dependabot bump of
  // setup-go/setup-node silently shrank what this guard inspected -- and once
  // BOTH were bumped, matched nothing and failed outright. What the guard is
  // actually for is that each setup disables its shared cache, so it accepts
  // any major at or above the floor and still refuses the ones below it.
  const setups = [...raw.matchAll(/^        uses: actions\/setup-(go|node)@v(\d+)$/gm)]
  assert(setups.length > 0, 'require publisher setup actions')
  for (const setup of setups) {
    assert(
      Number(setup[2]) >= MIN_SETUP_ACTION_MAJOR,
      `publisher setup-${setup[1]} must be at least v${MIN_SETUP_ACTION_MAJOR}, found v${setup[2]}`,
    )
  }
  for (const setup of setups) {
    // Inspect only this action's input block, never a following setup's flags.
    const block = raw.slice(setup.index + setup[0].length).split(/\n      - |\n  [a-z][a-z-]*:\n/, 1)[0]
    if (setup[1] === 'go') {
      assert(/^          cache: false$/m.test(block), 'every publisher Go setup must explicitly disable its default shared cache')
    } else {
      assert(/^          package-manager-cache: false$/m.test(block), 'every publisher Node setup must disable automatic package-manager caching')
      assert(!/^\s*(?:cache|cache-dependency-path):/m.test(block), 'publisher Node setup must not explicitly restore/save npm caches')
    }
  }
}

test('all literal release shell scripts parse without executing or publishing', () => {
  const scripts = literalScripts()
  assert(scripts.length >= 7)
  for (const script of scripts) execFileSync('bash', ['-n'], { input: script })
})

// THE VALIDATOR IS THIS REPOSITORY'S OWN. It used to be Passwall Node's command,
// run with `go run`, which kept the Node module in PSP's go.mod — so the
// dependency could not be dropped while this derivation was outsourced. The RULE
// is unchanged and the shared vectors are still the contract between the two
// implementations; only the implementation's address moved.
test('release tag input uses env plus this repository\'s own canonical validator', () => {
  const setup = job('setup')
  assert(setup.includes('REQUESTED_TAG: ${{ inputs.tag }}'))
  assert(setup.includes('go run ./cmd/release-tag "$tag"'))
  assert(setup.includes('test "$GITHUB_REF" = "refs/tags/${tag}"'))
  assert(setup.includes('git rev-parse --verify "refs/tags/${tag}^{commit}"'))
  assert(setup.includes('test "$release_sha" = "$GITHUB_SHA"'))
  assert(setup.includes('gh api --paginate "repos/${GITHUB_REPOSITORY}/releases"'))
  assert(setup.includes('test -z "$existing"'))
  for (const script of literalScripts()) {
    assert(!script.includes('${{ inputs.'), 'untrusted input must never be interpolated into shell source')
  }
})

// THE RELEASE IS ONE DISPATCH. Before this, the tag had to exist first and the run
// had to be dispatched FROM that tag, so fixing the release path itself cost a PR
// and a merge before a release could run — which happened twice in one session. A
// hand-pushed tag is still accepted, so this adds a path rather than replacing one.
test('the dispatch form can cut the next release without a local tag push', () => {
  const form = workflow.slice(
    workflow.indexOf('  workflow_dispatch:'),
    workflow.indexOf('\njobs:'),
  )
  for (const name of ['tag:', 'line:', 'notes:']) {
    assert(form.includes(`\n      ${name}`), `the dispatch form has no ${name} input`)
  }
  // BLANK CUTS THE NEXT ONE, so the tag input CANNOT be required: a required tag
  // input is the old path, where the tag is made elsewhere and the form only
  // records it.
  const tag = form.slice(form.indexOf('      tag:'), form.indexOf('      line:'))
  assert(/required:\s*false/.test(tag), 'the tag input must be optional: blank means cut the next one')
  assert(!/required:\s*true/.test(tag), 'a required tag input puts the tag back outside the form')
})

test('the tag job cuts the tag after the suite passes, only when the form asked for one', () => {
  const cut = job('tag')
  // THE CONDITION IS A STEP, NOT AN `if:` ON THE JOB, AND THAT IS THE ASSERTION.
  //
  // It used to be `if: github.event_name == 'workflow_dispatch' && inputs.tag == ''`
  // on the job, which skipped it on the pushed-tag path — and a skipped job skips
  // everything downstream of it, so that path published nothing while reporting a
  // failed compatibility gate. The exemption written on `setup` (`always() && …`)
  // did not restore them. The invariant is asserted over the whole graph by the
  // case below; here it is pinned where it was broken.
  assert(
    !/^    if:/m.test(cut),
    'the tag job must carry no job-level condition: a skipped job skips its whole downstream chain, and the release jobs under it depend on this one having run',
  )
  assert(
    cut.includes('if: steps.decision.outputs.cut == \'true\''),
    'the cut is gated on a step output, so the job itself always runs',
  )
  // WHAT IT DECIDES IS UNCHANGED, and it is read from the same two inputs the job
  // condition used: the event, and whether the form named a tag.
  assert(cut.includes('EVENT_NAME: ${{ github.event_name }}'), 'the decision must read the event')
  assert(cut.includes('REQUESTED_TAG: ${{ inputs.tag }}'), 'the decision must read the form')
  assert(
    cut.includes('"$EVENT_NAME" = "workflow_dispatch" ] && [ -z "$REQUESTED_TAG" ]'),
    'only a dispatch that named no tag cuts one: a pushed tag is the tag, and a named tag already addresses the release',
  )
  assert(cut.includes('contents: write'), 'cutting a tag is the one job that needs to write contents')
  // THE NUMBER IS NOT COMPUTED HERE. "An incremental fix takes the fourth segment"
  // and "a number, once bound to a revision, is never reused" are one rule with
  // three consumers, and it lives in internal/version. It used to be reached
  // through the Node module; X07 moved it into this repository, which is why the
  // command below is a path into this checkout rather than a module reference.
  assert(
    cut.includes('go run ./cmd/allocate-release-tag'),
    'the number must come from this repository\'s own allocator, not from arithmetic in YAML',
  )
  assert(
    cut.includes('-line "$line"'),
    'the line must be named by the caller: a line\'s first release is named, not allocated',
  )
  // THE CREATE IS THE CONFIRMATION. `git push` refuses to move a ref, so a lost
  // race is found by the push failing and the next pass re-reading — which is why
  // this is a loop rather than one shot.
  assert(cut.includes('git push --quiet origin "refs/tags/$tag"'), 'the tag must be created by an atomic ref push')
  assert(/while \[ "\$attempt" -lt \d+ \]/.test(cut), 'the allocation must retry when the push loses the race')
  // ANNOTATED AT CREATION, because the notes are the artefact a reader opens and
  // the only place a note survives a rerun.
  assert(cut.includes('git tag -a --cleanup=verbatim "$tag"'), 'the tag must be annotated and carry the notes')
  // AND IT REFUSES A COMMIT THE SUITE HAS NOT PASSED — asking about the gate
  // workflow. Enumerating the commit's check runs would include this very release
  // run, still in progress, and refuse the commit for its own existence.
  assert(
    cut.includes('--workflow test.yml --commit "$SHA"'),
    'the tag job must ask about the test workflow rather than about every run on the commit',
  )
  assert(!cut.includes('check-runs'), 'enumerating check runs refuses the commit for the release run itself existing')
  // AND THAT CHECK IS NOT BEHIND THE DECISION. It used to sit inside a job that only
  // ran for the blank dispatch form, so a tag pushed by hand was published from a
  // commit whose suite nobody had asked about — the one path where the commit was
  // chosen by a person rather than by the allocator. The step now runs on every
  // path; only the allocation below it is conditional.
  const suiteCheck = /- name: Refuse a commit the test suite has not passed\n([\s\S]*?)\n      - name:/.exec(cut)
  assert(suiteCheck, 'the suite check must be its own step')
  assert(
    !/^        if:/m.test(suiteCheck[1]),
    'the suite check must run on every path, including a pushed tag',
  )
  // AND IT ASKS ABOUT MAIN, TWICE. A dispatch can start from any branch, and a
  // branch head usually has a green pull_request run of test.yml, which tested the
  // merge ref and not this commit. Unfiltered, the lookup accepted that run and a
  // permanent tag could be cut on unmerged code. Read from the executed lines, so
  // the comment explaining the rule cannot satisfy it.
  const suiteScript = withoutComments(suiteCheck[1])
  assert(
    suiteScript.includes('git merge-base --is-ancestor "$SHA" origin/main'),
    'the suite check must refuse a commit that main does not contain',
  )
  assert(
    /git fetch [^\n]*refs\/heads\/main:refs\/remotes\/origin\/main/.test(suiteScript),
    'the ancestry check must read a main this step fetched, not whatever the checkout happened to leave',
  )
  assert(
    /--workflow test\.yml --commit "\$SHA" --branch main\b/.test(suiteScript),
    'the suite check must take only a test.yml run on main as evidence, not a pull_request run of the same head',
  )
})

// A SKIPPED JOB SKIPS EVERYTHING DOWNSTREAM OF IT, TRANSITIVELY, AND `always()` ON A
// JOB IN BETWEEN DOES NOT RESTORE THE JOBS UNDER IT. The exempted job runs and reports
// success; its dependents are skipped anyway, because GitHub decides each dependent's
// skip from the graph rather than from what the intermediate job actually reported.
//
// THIS IS NOT A THEORY. The first release cut by pushing a tag (v4.0.1.8) published
// nothing: the tag job was skipped on that path, `setup` was exempted (`always() && …`)
// and succeeded, and `web`, `build`, `release` and `docker` were skipped with it. The
// run reached the compatibility gate — which carries `always()` — and failed there,
// with `LEG=skipped` standing in for "no artifact was ever built".
//
// SO TWO PROPERTIES ARE PINNED, AND TOGETHER THEY CLOSE THE CLASS.
//
// One: nothing the release is built from carries a job-level condition at all. Those
// jobs may still be skipped when a job above them FAILS, and that is the outcome we
// want — a failed release stops, loudly, naming its cause. What must not happen is a
// job being skipped while the jobs below it are expected to run.
//
// Two: any job-level condition left in the file has to carry `always()`, so it can
// report on a chain that did not succeed instead of being skipped by it. The
// compatibility gate is that shape: it is terminal, and its verdict on a failed leg is
// the reason it exists.
test('no job is skipped by its own condition while the release depends on it', () => {
  const all = jobs()
  for (const name of ['setup', 'node-compatibility', 'web', 'build', 'release', 'docker']) {
    const raw = all.get(name)
    assert(raw, `the release graph must still contain the ${name} job`)
    assert(
      !/^    if:/m.test(raw),
      `${name} is part of what a release is built from and must carry no job-level if:. A condition there skips the job — and every job under it — without failing anything, which is how v4.0.1.8 published nothing.`,
    )
  }
  for (const [name, raw] of all) {
    const condition = /^    if: (.*)$/m.exec(raw)
    if (!condition) continue
    assert(
      condition[1].includes('always()'),
      `${name} carries a job-level if: without always(), so it can be skipped by its own condition and take its dependents with it. Gate a step instead, as the tag job does.`,
    )
  }
})

// EVERY RELEASE JOB STATES ITS OWN TIMEOUT. All releases share one concurrency group
// that never cancels, and GitHub keeps one pending run per group, so a job hung on
// the 360-minute default blocks every release for six hours, and a third release
// started meanwhile silently replaces the second: a pushed tag left without a release.
test('every job in release.yml sets its own timeout', () => {
  for (const [name, raw] of jobs()) {
    assert(/^    timeout-minutes: \d+$/m.test(raw), `${name} has no timeout-minutes, so a hang holds the release group for GitHub's 360-minute default`)
  }
})

// A TAG CARRIES AN IDENTITY, AND A FRESH RUNNER HAS NONE.
//
// `git tag -a` refuses with `fatal: empty ident name` when no committer is
// configured, and this job is the only place in the release that creates a ref. It
// was written, guarded for its shape, and then failed the FIRST time it actually
// ran — the two releases before it were tagged before the job existed, so nothing
// had exercised it. Shape guards cannot see a missing identity, which is why this
// one asserts the identity rather than the sequence.
//
// The identity is a person's, and it is the one every existing tag carries: a
// published tag cannot be rewritten, so a tool's name in it would be permanent.
test('the tag job gives the tag an identity before creating it', () => {
  const cut = job('tag')
  // THE COMMAND, NOT THE PHRASE. Searching for `git tag -a` found the comment that
  // explains why the identity is needed — which sits above the configuration it is
  // meant to precede — so this assertion first reported that the fix was in the
  // wrong place. Counting indentation instead was worse: the create is inside a
  // retry loop and the configuration is not, so they do not share a depth. The flag
  // is what belongs to the command and to nothing a comment would say.
  const created = cut.indexOf('git tag -a --cleanup=verbatim')
  const configured = cut.indexOf('git config user.name')
  assert(created >= 0, 'the tag job no longer creates an annotated tag')
  assert(configured >= 0, 'a fresh runner has no committer identity, and git tag -a refuses without one')
  assert.match(cut, /git config user\.email/, 'the identity needs an email as well as a name')
  assert(configured < created, 'the identity must be configured before the tag is created, not after')
})

test('the release reads the tag this run cut', () => {
  assert(job('tag').includes('tag: ${{ steps.cut.outputs.tag }}'), 'the tag job must export the tag it cut')
  const setup = job('setup')
  assert(setup.includes('CUT_TAG: ${{ needs.tag.outputs.tag }}'), 'setup must read the tag the run cut')
  assert(/^\s*needs: tag$/m.test(setup), 'setup must depend on the tag job')
  // A SKIPPED DEPENDENCY SKIPS ITS DEPENDENTS, so the condition has to say that a
  // skipped tag job is not a failed one — otherwise a pushed tag releases nothing.
  assert(
    setup.includes("needs.tag.result == 'skipped'"),
    'a skipped tag job must not skip the release',
  )
  // THE REF CHECK CANNOT APPLY TO A TAG THIS RUN CUT: the run was dispatched from a
  // branch, so GITHUB_REF is not the tag. It stays for the hand-pushed path, where
  // the ref that triggered the run is the tag.
  assert(
    setup.includes('if [ -z "${CUT_TAG}" ]; then'),
    'the tag-ref check must be limited to the paths where the triggering ref IS the tag',
  )
})

test('every downstream job checks out trusted immutable workflow SHA proven equal to release SHA', () => {
  assert(job('setup').includes('test "$release_sha" = "$GITHUB_SHA"'))
  for (const name of ['node-compatibility', 'web', 'build', 'release', 'docker']) {
    assert(job(name).includes('ref: ${{ github.sha }}'), `${name} checkout is not pinned to the trusted workflow commit`)
    assert(!job(name).includes('ref: ${{ needs.setup.outputs.sha }}'), 'resolver output must not become a privileged checkout input')
  }
  const build = job('build')
  assert(build.includes('COMMIT: ${{ needs.setup.outputs.sha }}'))
  assert(build.includes('BUILD_DATE: ${{ needs.setup.outputs.build_date }}'))
  assert(!build.includes('date -u'))
  assert(build.includes('sh deploy/check-build.sh'))
  assert(job('release').includes('target_commitish: ${{ needs.setup.outputs.sha }}'))
  assert(job('docker').includes('org.opencontainers.image.revision=${{ needs.setup.outputs.sha }}'))
})

test('published releases and exact images cannot silently overwrite while rolling channels remain intentional', () => {
  const release = job('release')
  const docker = job('docker')
  assert(release.includes('overwrite_files: false'))
  assert(release.includes('fail_on_unmatched_files: true'))
  assert(release.includes('release tag changed during the build'))
  assert(docker.includes('release tag changed during the build'))
  assert(job('setup').includes('node deploy/check-image.mjs'))
  assert(docker.includes('node deploy/check-image.mjs'))
  assert(docker.includes("value=latest,enable=${{ needs.setup.outputs.image_channel == 'true' && needs.setup.outputs.prerelease == 'false' }}"))
  assert(docker.includes("value=beta,enable=${{ needs.setup.outputs.image_channel == 'true' }}"))
  assert(workflow.includes('group: release-passwall-sub-panel\n  cancel-in-progress: false'))
})

test('archives and OCI metadata carry PSP AGPL license and reviewed version notes', () => {
  assert(job('release').includes('cp LICENSE README.md "${pkgdir}/"'))
  assert(job('release').includes('note="docs/releases/${VERSION}.md"'))
  assert(job('release').includes('cat "$note" >> release_notes.md'))
  assert(job('docker').includes('org.opencontainers.image.licenses=AGPL-3.0'))
  assert(!job('docker').includes('Apache-2.0'))
  assert(readFileSync(new URL('../LICENSE', import.meta.url), 'utf8').includes('GNU AFFERO GENERAL PUBLIC LICENSE'))
})

test('write tokens are scoped to publisher jobs only', () => {
  const global = workflow.slice(0, workflow.indexOf('\njobs:'))
  assert(global.includes('permissions:\n  contents: read'))
  assert(!global.includes('packages: write'))
  for (const name of ['setup', 'node-compatibility', 'web', 'build']) assert(!job(name).includes('contents: write'))
  assert(job('release').includes('permissions:\n      contents: write'))
  assert(job('docker').includes('permissions:\n      contents: read\n      packages: write'))
})

test('publisher explicitly disables every Go and Node shared dependency cache', () => {
  assertPublisherCachesDisabled(workflow)
})

// THE GATE MUST COVER EVERY RETAINED NODE RELEASE, and how it does that has
// changed. The matrix used to be eleven literal lines in this file, which meant
// the supported set lived here, in test.yml and in the manifest, and a release
// added to one but not the others was covered exactly until somebody noticed.
// It is derived now.
//
// So this asserts the DERIVATION rather than restating the list. Pinning the
// literals again would pass against a hand-maintained copy that had already
// drifted — which is the failure this test exists to refuse, and it is not
// hypothetical: beta10 and beta11 shipped while every check stayed green,
// because nothing compared the offered set against the published one.
test('release compatibility gate derives its Node set from the single source of truth', () => {
  const compatibility = job('node-compatibility')
  const setup = job('setup')

  // Derived, not enumerated: the matrix reads the plan setup built.
  assert(
    compatibility.includes('node_case: ${{ fromJSON(needs.setup.outputs.node_cases) }}'),
    'the compatibility matrix must be derived from the plan, not listed'
  )
  // And no version literal may reappear in this job at all. Matched as "any
  // literal" rather than as the bare-list shape the old matrix happened to use,
  // because a reintroduced list could equally arrive as `- node_version: v…` or
  // an inline array, and every one of those shadows the derivation and drifts
  // from it just as quietly. The derived job contains no version string, so
  // there is nothing here to allowlist.
  assert(
    !/v\d+\.\d+\.\d+/.test(compatibility),
    'the compatibility job must not name a version literal; derive it instead'
  )

  // The plan is built by the planner, which is the only thing that reads the
  // manifest. Locating the floor BY POSITION is a property of the planner and is
  // asserted in its own suite; what matters here is that this workflow does not
  // derive the set itself.
  assert(setup.includes('deploy/compat/plan.mjs'), 'the supported set must come from the planner')
  assert(setup.includes('--emit cases'), 'the planner must emit the cases the matrix consumes')

  // A tag is a label, not an identity. Each leg carries the commit the plan
  // pinned and refuses to run on a different one, so a moved tag cannot swap the
  // source while still reporting the same version.
  assert(compatibility.includes('${{ matrix.node_case.sha }}'), 'each leg must carry the pinned commit')
  assert(compatibility.includes('rev-parse HEAD'), 'each leg must verify the resolved commit')

  // The gate still exercises the live contract tests and still blocks release.
  assert(compatibility.includes('TestLive_RealNode(AgentContract|MigratedServerContract|TaskEvidenceReceipt|TaskExpiryContract)'))
  assert(job('release').includes('needs: [setup, build, compatibility]'))
  assert(job('docker').includes('needs: [setup, build, compatibility]'))
})

// EVERY PUBLISHING JOB REACHES THE COMPATIBILITY EVIDENCE ONLY THROUGH THE
// GATE. Chaining `needs` directly onto node-compatibility would let a job
// publish while the gate — which is the thing that knows whether the whole case
// set reported — is still failing or was never produced.
test('every publishing job is gated behind the compatibility summary', () => {
  const gate = job('compatibility')
  // always(), so a cancelled or failed leg still produces a verdict. A skipped
  // gate reports as an absent check, which reads as "not run" rather than
  // "failed", and would let a branch merge on evidence that was never gathered.
  assert(gate.includes('if: always()'))
  assert(gate.includes('node-compatibility'), 'the gate must summarise the compatibility leg')
  for (const name of ['release', 'docker']) {
    assert(
      job(name).includes('needs: [setup, build, compatibility]'),
      `${name} must depend on the summary gate`
    )
    assert(
      !/needs: \[[^\]]*node-compatibility/.test(job(name)),
      `${name} must reach node-compatibility only through the gate`
    )
  }
})

// The suite above is only evidence if something runs it. This asserts the file
// is wired into the job that already runs the other deploy guards, so a renamed
// or dropped checker cannot quietly stop being executed.
test('the compatibility gate actually runs the case-set checker', () => {
  const gate = job('compatibility')
  assert(gate.includes('deploy/compat/check-case-set.mjs'))
  assert(gate.includes('actions/download-artifact'))
  // AND IT DOWNLOADS ONLY WHAT IT READS. An unscoped download fetches every artifact
  // in the run and fails on any it cannot read; that took test.yml's gate down on
  // main (#226), and this run's docker job publishes the same kind of record.
  const downloads = withoutComments(gate)
    .split(/\n(?= {6}- )/)
    .filter((step) => step.includes('uses: actions/download-artifact@'))
  assert(downloads.length > 0, 'the gate must download its evidence')
  for (const step of downloads) {
    assert(/^ {10}(?:name|pattern): \S/m.test(step), `the gate downloads every artifact in the run:\n${step}`)
  }
  for (const pattern of ["pattern: 'node-wire-v1@*'", "pattern: 'compat-digest-*'"]) {
    assert(downloads.some((step) => step.includes(pattern)), `the gate no longer downloads ${pattern}`)
  }
})

// R10 STEP 1, MADE ENFORCEABLE. "Walk the release needs graph and list every job
// that really writes a release asset, an image or a channel" is a one-off reading
// of the file; the property worth keeping is that a job ADDED LATER cannot write
// without the gate. So the publishing set is derived from what each job is
// permitted to write, not listed here — a list would only confirm that someone
// remembered to update it.
test('every job that can write is behind the compatibility gate', () => {
  const names = [...workflow.matchAll(/^  ([a-z][a-z0-9_-]*):\n/gm)].map((m) => m[1])
  assert(names.length > 3, `expected the workflow to declare jobs, found ${names.length}`)
  // ONE EXEMPTION, AND IT IS ABOUT ORDER RATHER THAN TRUST. The tag job writes a
  // REF, not a release and not an artifact, and it runs BEFORE the gate: the tag
  // it cuts is the identity every later job reads, so a dependency on
  // `compatibility` is a cycle rather than a safeguard. Its own gate is the test
  // suite on the commit, which it checks directly and refuses without. A number
  // that a later-failing release then leaves unused is the gap the allocation rule
  // explicitly permits. The exemption is itself guarded by the test below, which
  // holds the tag job to a ref push and nothing else.
  const writers = names.filter(
    (name) => name !== 'tag' && /^ {6}(contents|packages|id-token):\s*write\s*$/m.test(job(name)),
  )
  assert(writers.length > 0, 'no job can write; either the workflow changed or this matcher broke')
  for (const name of writers) {
    assert(
      /needs: \[[^\]]*\bcompatibility\b[^\]]*\]/.test(job(name)),
      `${name} can write but does not depend on the compatibility summary`
    )
  }
})

// THE EXEMPTION ABOVE IS NARROW, AND THIS IS WHAT KEEPS IT SO. The tag job may
// write because it creates the ref the release is published under; if that scope
// ever grows to a package or an OIDC token, it has stopped being a tag cutter and
// the exemption no longer covers it.
test('the tag job writes a ref and nothing else', () => {
  const cut = job('tag')
  assert(/^ {6}contents: write$/m.test(cut), 'the tag job needs contents: write to create the tag')
  for (const scope of ['packages', 'id-token']) {
    assert(
      !new RegExp(`^ {6}${scope}: write$`, 'm').test(cut),
      `the tag job must not hold a ${scope} write; that is a publisher, not a tag cutter`,
    )
  }
  // COMMENTS NAME THE COMMAND TOO, so a bare substring search matches the prose
  // explaining the race and reports it as a second push.
  const writes = cut
    .split('\n')
    .filter((line) => !line.trimStart().startsWith('#') && /git push/.test(line))
  assert(writes.length > 0, 'the tag job must push the tag')
  for (const line of writes) {
    assert(
      line.includes('refs/tags/$tag'),
      `the tag job pushes something other than the release tag: ${line.trim()}`,
    )
  }
})

// A WRITE TOKEN RUNS ONLY CODE THAT CANNOT BE SWAPPED UNDER IT. The writers are
// derived from their permissions, so a publisher added later is held to this too —
// and derived from ALL of them: any scope granted `write`, `write-all`, and for a job
// that declares no permissions of its own, whatever the top-level block grants it
// (or the repository default, which can be write, when there is no block at all).
// In each, an action from outside actions/* is named by a full commit with its
// release in a trailing comment — the form Dependabot keeps current. A tag would
// run whatever it points at on the day of the release.
function permissionsAt(text, indent) {
  const found = new RegExp(`^ {${indent}}permissions:(.*)\\n((?: {${indent + 2},}.*\\n|[ \\t]*\\n)*)`, 'm').exec(text)
  return found ? `${found[1]}\n${found[2]}`.replace(/#.*$/gm, '') : null
}

function grantsWrite(permissions) {
  return /^\s*write-all\s*$/m.test(permissions) || /[a-z-]+:\s*write\b/.test(permissions)
}

test('every third-party action in a job that can write is pinned by commit', () => {
  const inherited = permissionsAt(workflow.slice(0, workflow.indexOf('\njobs:\n') + 1), 0) ?? 'write-all'
  let seen = 0
  for (const [name, block] of jobs()) {
    if (!grantsWrite(permissionsAt(block, 4) ?? inherited)) continue
    for (const [line, ref] of block.matchAll(/^ +(?:- )?uses: (\S+).*$/gm)) {
      seen++
      if (ref.startsWith('actions/') || ref.startsWith('./')) continue
      assert(
        /@[0-9a-f]{40} # v\d+\.\d+\.\d+$/.test(line),
        `${name} runs ${ref} with a write token; pin it to a commit with a # vX.Y.Z comment`,
      )
    }
  }
  assert(seen > 0, 'no job that can write uses an action; either the workflow changed or this matcher broke')
  // The derivation itself, on the shapes a later job could take.
  const at = (text) => grantsWrite(permissionsAt(text, 4) ?? inherited)
  assert.equal(at('  j:\n    permissions: write-all\n    steps:\n'), true)
  assert.equal(at('  j:\n    permissions:\n      contents: read\n      # a note\n\n      attestations: write\n'), true)
  assert.equal(at('  j:\n    permissions:\n      contents: read\n    steps:\n      - run: echo contents: write\n'), false)
  assert.equal(permissionsAt('  j:\n    steps:\n      - run: true\n', 4), null, 'a job with no block of its own inherits')
  assert.equal(grantsWrite(permissionsAt('permissions:\n  pull-requests: write\njobs:\n', 0)), true)
})

// R10 STEP 3: "cross-platform compilation does not substitute for runtime
// acceptance". The compile matrix is what makes a release buildable on every
// platform; it is not evidence that any of them RUNS. A publishing job that
// depends on it must still depend on the gate, or a green cross-compile would
// stand in for acceptance.
test('a cross-compile job is never a publishing job\'s only dependency', () => {
  for (const name of ['release', 'docker']) {
    const needs = /needs: \[([^\]]*)\]/.exec(job(name))
    assert(needs, `${name} declares no needs`)
    const deps = needs[1].split(',').map((d) => d.trim())
    assert(deps.includes('compatibility'), `${name} must depend on the gate`)
  }
})

// R10 STEP 3, THE HALF A NEEDS GRAPH CANNOT SHOW. "Declared runtime platforms need
// native or clearly labelled runtime evidence." check-build.sh reads build
// metadata and never runs anything, so three failures shipped green past it: a
// version stamp the linker silently dropped (every binary reports "dev"), a web
// bundle that landed outside internal/web/dist (a panel with no UI, since an empty
// dist still compiles), and an arm64 binary no job had ever started. The build job
// now answers all three, and this holds it to the executed lines, not the comments.
test('each linux leg runs what it built, natively, and no leg links a panel without its UI', () => {
  const build = job('build')
  const executed = withoutComments(build)

  const bundle = executed.indexOf('test -f internal/web/dist/index.html')
  const compile = executed.indexOf('go build ')
  assert(bundle >= 0, 'every leg must refuse a missing internal/web/dist/index.html')
  assert(compile >= 0, 'the build job no longer compiles the panel')
  assert(bundle < compile, 'the UI check must come before go build, while it can still stop the leg')

  // NATIVE, BY CONSTRUCTION. A linux leg on a foreign runner could only emulate,
  // and R10 does not let emulation stand in for native; the step fails such a leg
  // rather than skipping it, and this refuses the matrix that would reach it.
  assert(/^    runs-on: \$\{\{ matrix\.runner \}\}$/m.test(build), 'each build leg must run on the runner its matrix row names')
  const legs = [...build.matchAll(/^ +- \{ goos: (\w+), +goarch: (\w+),.*\brunner: ([\w.-]+) *\}$/gm)]
  assert.equal(legs.length, 6, 'every one of the six release targets must name its runner')
  const linux = legs.filter(([, goos]) => goos === 'linux')
  assert.deepEqual(linux.map(([, , goarch]) => goarch).sort(), ['amd64', 'arm64'])
  for (const [, , goarch, runner] of linux) {
    const armRunner = /-arm$/.test(runner)
    assert.equal(armRunner, goarch === 'arm64', `linux/${goarch} is built on ${runner}, which cannot run it natively`)
  }
  assert(executed.includes('test "$(go env GOHOSTARCH)" = "$GOARCH"'), 'a linux leg on a foreign runner must fail, not skip its run')

  // AND WHAT RUNS MUST NAME THE RELEASE. Exit status alone passes a binary that
  // says "dev", which is exactly what a dropped -X produces.
  assert(executed.includes('if [ "$GOOS" = linux ]; then'), 'both linux legs must run their binary')
  assert(executed.includes('reported=$(out/psp --version 2>&1)'), 'the linux legs must run the binary they built')
  assert(
    executed.includes('grep -qF "${VERSION} (${short_sha})" <<<"$reported"'),
    'the binary must report the release version and commit, not merely exit 0',
  )
})

// R10 STEP 4: the evidence index is what still exists once the run's logs have
// expired, so its absence is not a missing convenience — it is a claim nobody can
// check later. Asserted by shape because this guard reads the workflow as text:
// the step must name the indexer and the index must be uploaded.
test('the release gate records an evidence index', () => {
  const gate = job('compatibility')
  assert(gate.includes('deploy/compat/evidence-index.mjs'), 'the gate must assemble an evidence index')
  assert(gate.includes('deploy/compat/plan.mjs'), 'the index must be built against the planned case manifest, not the reports')
  assert(gate.includes('compatibility-evidence-index'), 'the index must be uploaded, or it expires with the runner')
})

// AND UPLOADED IS NOT KEPT. An artifact lives 90 days at most, which is exactly the
// horizon the index exists to outlive, and the handover records it as a release
// attachment — which it was once, by hand. So the release carries it, under the
// same checksums as the archives, and the image digest, which R10 step 4 also asks
// for and which nothing recorded, is written down where the push happens.
test('the release attaches the evidence index under its checksums, and the image digest is recorded', () => {
  const release = job('release')
  assert(
    /uses: actions\/download-artifact@v\d+\n\s+with:\n\s+name: compatibility-evidence-index\n/.test(release),
    'the release job must download the index the gate wrote',
  )
  const packaging = withoutComments(release)
  const attach = packaging.indexOf('cp evidence/evidence-index.json "dist/compat-evidence-index-${VERSION}.json"')
  const sums = packaging.indexOf('(cd dist && sha256sum -- * > SHA256SUMS.txt)')
  assert(attach >= 0, 'the index must be packaged into dist, which is what the release publishes')
  assert(sums >= 0, 'the release no longer writes SHA256SUMS.txt')
  assert(attach < sums, 'the index must be in dist before the sums are taken, or it is published unchecked')
  assert(packaging.includes(".source_sha == $sha"), 'the attached index must be checked to name this commit')
  assert(/files: \|\n\s+dist\/\*\n/.test(release), 'the release publishes dist/*, which is what carries the index')

  const docker = job('docker')
  assert(/^ {6}- name: Build and push multi-arch image\n {8}id: push$/m.test(docker), 'the push step must be addressable for its digest')
  assert(docker.includes('digest: ${{ steps.push.outputs.digest }}'), 'the docker job must export the digest it pushed')
  const record = withoutComments(docker.slice(docker.indexOf('- name: Record the pushed image digest')))
  assert(record.includes('DIGEST: ${{ steps.push.outputs.digest }}'), 'the digest must be recorded from the push itself')
  assert(record.includes('>> "$GITHUB_STEP_SUMMARY"'), 'the digest must be written where the run shows it')
  assert(record.includes('^sha256:[0-9a-f]{64}$'), 'an empty or malformed digest must fail rather than be recorded')
})

// R10 STEP 5: signature verification and the candidate's test trust chain are
// SEPARATE, and a private candidate must never be made trusted by relaxing the
// publisher. The manual's words are "do not modify production code to trust an
// arbitrary release source for a private candidate".
//
// PSP publishes checksums and a provenance attestation, and verifies no signature
// on the way in, so there is no signature step to separate here — but the same
// rule has a checkable form: the publisher must
// still verify what it produced, and nothing in the path may be relaxed to make a
// candidate pass. Each pattern below is a way that rule gets broken quietly.
test('the publisher verifies its own artifacts and relaxes nothing', () => {
  // EXECUTED, NOT MENTIONED. This used to count every line of the file matching
  // the command, and the one it found was in the release-notes heredoc: an
  // instruction to the reader. The guard was green while nothing downloaded or
  // checked a published file, and the release was public before its files
  // existed. Heredoc bodies are data, so they are not counted now.
  assert.deepEqual(
    executedLines('set -eu\ncat <<EOF > notes.md\nsha256sum -c SHA256SUMS.txt\nEOF\ngrep -q x <<<"$y"\n'),
    ['set -eu', 'cat <<EOF > notes.md', 'grep -q x <<<"$y"', ''],
    'a heredoc body must not count as a command, and a here-string must not open one',
  )
  const release = job('release')
  const executed = literalScripts(release).flatMap(executedLines)
  // The checksum verification has to be a real step, not a swallowed one. Asserting
  // only that the command appears is not enough — it still appears with `|| true`
  // appended, and that is precisely the shape a quiet relaxation takes.
  const verifyLines = executed.filter((line) => /sha256sum -c\s+SHA256SUMS\.txt/.test(line))
  assert(verifyLines.length > 0, 'the publisher must verify the checksums it published, in a line that runs')
  for (const line of verifyLines) {
    const after = line.slice(line.indexOf('SHA256SUMS.txt') + 'SHA256SUMS.txt'.length)
    assert(!/\|\||&&|;/.test(after), `the checksum verification is followed by more shell, which can swallow its failure: ${line.trim()}`)
  }
  // WHAT IT VERIFIES IS WHAT WAS PUBLISHED, AND BEFORE ANYONE CAN SEE IT. The files
  // are read back from the release, not from dist/, which the upload read too; the
  // release is a draft while that happens; and only after it passes does the draft
  // flag change — nothing else about the release does.
  assert(executed.some((line) => /gh release download "\$TAG"[^\n]*--dir verify/.test(line)), 'the published files must be downloaded back')
  assert(verifyLines.some((line) => line.includes('cd verify')), 'the checksums must be checked against the downloaded files')
  assert(
    executed.some((line) => line.includes("'.artifacts[$name]") && line.includes('evidence/evidence-index.json')),
    'each archived binary must be compared with the digest the gate indexed',
  )
  const steps = withoutComments(release)
  const upload = /- name: Publish GitHub Release\n([\s\S]*?)(?=\n {6}- name:|$)/.exec(steps)
  assert(upload, 'the release job no longer has its upload step')
  assert(upload[1].includes('uses: softprops/action-gh-release@'), 'the upload step is not the release action')
  assert(/^ {10}draft: true$/m.test(upload[1]), 'the release must be uploaded as a draft')
  const drafted = upload.index
  const verified = steps.indexOf("- name: Verify the draft's assets")
  const undrafted = steps.indexOf('gh release edit "$TAG" --repo "$GITHUB_REPOSITORY" --draft=false')
  assert(verified > drafted, 'the files must be verified after the draft is uploaded')
  assert(undrafted > verified, 'the release may become public only after its files are verified')
  const edits = executed.filter((line) => /\bgh release edit\b/.test(line))
  assert.deepEqual(
    edits.map((line) => line.trim()),
    ['gh release edit "$TAG" --repo "$GITHUB_REPOSITORY" --draft=false'],
    'making the release public must change the draft flag only: the channel, Latest and the notes were already decided',
  )
  for (const pattern of [
    /\|\|\s*true[^\n]*sha256/i,
    /sha256sum[^\n]*--insecure/,
    /--no-check-certificate/,
    /NODE_TLS_REJECT_UNAUTHORIZED/,
    /npm[^\n]*--strict-ssl[= ]false/,
    /docker[^\n]*--tls-verify[= ]false/,
    /cosign[^\n]*--insecure-ignore-tlog/
  ]) {
    assert(!pattern.test(workflow), `the publisher relaxes verification: ${pattern}`)
  }
})

// CHECKSUMS PROVE INTEGRITY, NOT ORIGIN. SHA256SUMS.txt is published on the same
// page as the files it covers, so whoever could replace a file could replace the
// sums. A provenance attestation binds each digest to this workflow, commit and
// run under a Sigstore certificate, and it is checked with `gh attestation verify`.
// Held here: both publishers attest what they publish, the scopes that needs are
// ADDED to theirs (the write-scope asserts above still read the same prefix), the
// release attests while it is still a draft, and the image by digest, not by tag.
test('the release files and the pushed image carry build provenance', () => {
  const release = job('release')
  const docker = job('docker')
  assert(
    release.includes('permissions:\n      contents: write\n      id-token: write\n      attestations: write\n'),
    'the release job needs id-token and attestations, after its own contents: write',
  )
  assert(
    docker.includes('permissions:\n      contents: read\n      packages: write\n      id-token: write\n      attestations: write\n'),
    'the docker job needs id-token and attestations, after its own scopes',
  )

  const files = withoutComments(release)
  // The files read back from the draft, which are the published bytes; dist/ is
  // not, on a re-run over this run's own draft.
  const attestFiles = /uses: actions\/attest-build-provenance@\S+\n {8}with:\n {10}subject-path: verify\/\*\n/.exec(files)
  assert(attestFiles, 'the release must attest every file it publishes, as read back from the release')
  assert(
    files.indexOf("- name: Verify the draft's assets") < attestFiles.index &&
      attestFiles.index < files.indexOf('--draft=false'),
    'the files are attested after they are verified and while the release is still a draft',
  )

  const image = withoutComments(docker)
  const attestImage = image.indexOf('uses: actions/attest-build-provenance@')
  assert(attestImage > image.indexOf('id: push'), 'the image is attested after it is pushed')
  const inputs = image.slice(attestImage)
  assert(inputs.includes('subject-name: ghcr.io/${{ needs.setup.outputs.owner_lc }}/passwall-sub-panel\n'), 'the image is named without a tag')
  assert(inputs.includes('subject-digest: ${{ steps.push.outputs.digest }}\n'), 'the image is attested by the digest the push reported')
  assert(inputs.includes('push-to-registry: true\n'), 'the attestation must sit in the registry beside the image')
})

// R10 STEP 7: "rolling channels follow the repository's existing semantics, and
// a candidate that did not pass cannot become an automatic upgrade target."
//
// The string assertions above prove the expressions are still written. They do
// not prove the expressions are RIGHT, and this is the one place where being
// wrong is silent and public: `:latest` mirrors GitHub's /releases/latest, which
// PSP's own in-app upgrade nudge reads, so a prerelease reaching `latest` offers
// every installed panel a beta it never asked for.
//
// NO MODEL OF THE RULE LIVES HERE. The resolution is a SCRIPT, and a script can
// be RUN, so the guard runs the bytes the workflow runs. The model that used to
// sit at this point — `channelEnabled(expression, tag)`, simulating the
// workflow's `startsWith(tag, 'v')` conditions — was deleted rather than kept
// when the expressions changed shape: it went on answering, correctly, for
// conditions the workflow no longer contained.
function resolveChannel(tag, requested = 'auto') {
  const jobText = job('setup')
  const stepStart = jobText.indexOf('Resolve the publication channel')
  assert(stepStart >= 0, 'the channel resolution step must exist in setup')
  const after = jobText.slice(stepStart)
  const runStart = after.indexOf('run: |\n')
  assert(runStart >= 0, 'the channel step must run a script')
  const body = after.slice(runStart + 'run: |\n'.length)
  // The block ends at the next step or at the end of the job.
  const end = body.search(/\n {6}- name:/)
  const script = end >= 0 ? body.slice(0, end) : body
  const dedented = script.split('\n').map(line => line.replace(/^ {10}/, '')).join('\n')

  const dir = mkdtempSync(join(tmpdir(), 'psp-channel-'))
  const outputs = join(dir, 'outputs')
  writeFileSync(outputs, '')
  execFileSync('bash', ['-c', dedented], {
    env: { ...process.env, PINNED_TAG: tag, REQUESTED: requested, GITHUB_OUTPUT: outputs },
  })
  const text = readFileSync(outputs, 'utf8')
  const read = name => {
    const found = new RegExp(`${name}=(true|false)`).exec(text)
    assert(found, `the resolution did not write ${name} for tag ${tag} (channel ${requested})`)
    return found[1] === 'true'
  }
  return { prerelease: read('prerelease'), images: read('images') }
}

test('the image channels both read the one resolved answer', () => {
  const docker = job('docker')
  const latest = /value=latest,enable=\$\{\{ (.*?) \}\}/.exec(docker)
  const beta = /value=beta,enable=\$\{\{ (.*?) \}\}/.exec(docker)
  assert(latest && beta, 'the image channels must both declare an enable condition')
  for (const [name, expression] of [['latest', latest[1]], ['beta', beta[1]]]) {
    assert(
      expression.includes("needs.setup.outputs.image_channel == 'true'"),
      `${name} must read whether images are published at all: ${expression}`,
    )
  }
  assert(latest[1].includes("needs.setup.outputs.prerelease == 'false'"),
    'latest must move only for a stable release')
  assert(!beta[1].includes('prerelease'),
    'beta tracks the newest of any kind, so it must not read the channel')
})

test('latest never points at a prerelease and beta always tracks the newest of any kind', () => {
  // THE DEFAULT IS A PRE-RELEASE FOR EVERY TAG, INCLUDING A PLAIN LEGACY ONE. It
  // used to be stable for `v1.0.0`, which made a v-tag the one publishable mistake
  // with an unrecoverable half: it moves a pointer consumers follow. Publishing
  // stable is now something a maintainer states.
  assert.deepEqual(resolveChannel('v1.0.0'), { prerelease: true, images: true })
  assert.deepEqual(resolveChannel('v0.0.1-beta11'), { prerelease: true, images: true })
  assert.deepEqual(resolveChannel('v1.0.0-rc1'), { prerelease: true, images: true })

  // The scheme it is moving to. A tag with no hyphen is NOT stable by default:
  // publishing a candidate as stable moves a pointer consumers follow, and no
  // later edit takes it back.
  assert.deepEqual(resolveChannel('release/4.0.0'), { prerelease: true, images: true })

  // Not a release tag at all: neither channel may move.
  assert.deepEqual(resolveChannel('1.0.0'), { prerelease: true, images: false })
  assert.deepEqual(resolveChannel('nightly'), { prerelease: true, images: false })

  // An explicit channel overrides the default, which is what the input is for.
  assert.deepEqual(resolveChannel('release/4.0.0', 'stable'), { prerelease: false, images: true })
  assert.deepEqual(resolveChannel('v1.0.0', 'stable'), { prerelease: false, images: true })
  assert.deepEqual(resolveChannel('v1.0.0', 'testing'), { prerelease: true, images: true })
})

// MUTATE ONE NAMED JOB, NOT "THE FIRST MATCH IN THE FILE". `String.replace` with a
// string changes only the first occurrence, so these mutations silently started
// editing whichever job happened to come first — and when a new job was added
// ahead of the publishers, the guard stopped being exercised at all and the test
// failed for a reason that had nothing to do with caches.
function inJob(name, from, to) {
  const body = job(name)
  const mutated = body.replace(from, to)
  assert(mutated !== body, `${name}: nothing matched ${JSON.stringify(from)} to mutate`)
  return workflow.replace(body, mutated)
}

test('publisher cache guard rejects implicit defaults and explicit cache restoration', () => {
  for (const [label, mutated] of [
    ['implicit Go cache', inJob('setup', '          cache: false\n', '')],
    ['enabled Go cache', inJob('build', '          cache: false', '          cache: true')],
    ['implicit Node cache', inJob('web', '          package-manager-cache: false\n', '')],
    ['enabled Node cache', inJob('web', '          package-manager-cache: false', '          package-manager-cache: true')],
    ['explicit npm cache', inJob('web', '          package-manager-cache: false', '          package-manager-cache: false\n          cache: npm')],
    ['npm cache path', inJob('web', '          package-manager-cache: false', '          package-manager-cache: false\n          cache-dependency-path: web-react/package-lock.json')],
    ['shared cache action', workflow + '\n      - uses: actions/cache@v4\n'],
    ['shared image cache', workflow + '\n          cache-from: type=gha\n'],
  ]) {
    assert.throws(() => assertPublisherCachesDisabled(mutated), { name: 'AssertionError' }, label)
  }
})

// THE PUBLICATION CHANNEL IS NOT READ OUT OF THE TAG TEXT.
//
// `prerelease: contains(tag, '-')` was the whole rule, and it is a LEGACY rule: a
// v-prefixed tag with a hyphen has always meant a pre-release. A product-scheme
// tag (release/MAJOR.MINOR.PATCH) has no hyphen at all, so the same test would
// publish every testing candidate as STABLE and would tag no image at all —
// neither of which is recoverable by a later edit, because `prerelease: false`
// and the `latest` image tag are what a consumer reads as "released".
test('the publication channel is resolved once, never inferred from a hyphen', () => {
  const setup = job('setup')
  const release = job('release')
  const docker = job('docker')

  assert(
    !/prerelease:\s*\$\{\{\s*contains\(/.test(workflow),
    'a job derives the channel from the tag text again; a product-scheme tag has no hyphen',
  )
  assert(
    !/type=raw,value=(?:latest|beta),enable=\$\{\{\s*contains\(/.test(workflow),
    'an image channel tag is derived from the tag text again',
  )

  // ONE ANSWER, IN SETUP, READ BY BOTH. Two places deciding a channel separately
  // is how they disagree.
  assert(setup.includes('Resolve the publication channel'), 'setup must resolve the channel')
  assert(/prerelease: \$\{\{ steps\.channel\.outputs\.prerelease \}\}/.test(setup), 'setup must expose the channel')
  assert(/image_channel: \$\{\{ steps\.channel\.outputs\.images \}\}/.test(setup), 'setup must expose whether images are tagged')

  assert(/prerelease: \$\{\{ needs\.setup\.outputs\.prerelease \}\}/.test(release), 'the release must publish the resolved channel')
  assert(/type=raw,value=latest,enable=\$\{\{ needs\.setup\.outputs\.image_channel/.test(docker), 'latest must read the resolved answer')
  assert(/type=raw,value=beta,enable=\$\{\{ needs\.setup\.outputs\.image_channel/.test(docker), 'beta must read the resolved answer')

  // The recoverable direction is now the default for every tag, not a fallback for
  // tags in neither scheme: `stable` is reachable only through the stated input.
  assert(/auto\)\s*prerelease=true/.test(setup), 'an automatic channel must default to pre-release')
})

// THE NODE CASE IS ADDRESSED BY ITS TAG, NOT ITS VERSION.
//
// A version is not a git ref. The release lives at refs/tags/release/4.0.0 and is
// named 4.0.0, so a checkout asked to resolve the version fails outright: the job
// dies before it records anything, and the release it gates never runs — which is
// exactly how the 4.0.1 release died, with the failure two jobs away from the
// line that caused it.
//
// The planner emits both fields. This pins which one addresses the source, and
// which one names the build.
test('the compat matrix addresses the node case by its tag', () => {
  assert.match(
    workflow,
    /ref:\s*\$\{\{\s*matrix\.node_case\.tag\s*\}\}/,
    'the compat matrix does not check the node case out by its tag, so it asks git for a ref that does not exist',
  )
  assert.doesNotMatch(
    workflow,
    /ref:\s*\$\{\{\s*matrix\.node_case\.version\s*\}\}/,
    'the compat matrix checks the node case out by its VERSION, which is not a git ref',
  )
})
