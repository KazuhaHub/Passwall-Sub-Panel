import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')

// A TRIGGER THAT FIRES BEFORE THE CONSUMERS UNDERSTAND THE TAG OPENS A RELEASE
// NOBODY CAN INSTALL — so this asserts the ABSENCE of the product pattern, for
// the same reason PN's TestTheNodeReleaseWorkflowDoesNotTriggerOnProductTagsYet
// does. The product scheme (release/MAJOR.MINOR.PATCH) is decided and the
// channel logic already answers for it; what is not ready is the reading side. A
// product release's TAG is not its VERSION, and two consumers of a published
// release still read the tag as the version:
//
//   - the public Node installer, which resolves its version from the release's
//     `tag_name`; and
//   - PSP's own cmd/compatwatch, which reads the same field on the Node repo and
//     reconciles it against internal/adapters/noderelease/reviewed.json.
//
// A `release/4.0.0` pushed today therefore starts a run whose identity step
// SUCCEEDS and whose image tag is then refused by check-image — a release that
// fails late, after the tag exists and names nothing. Making the two workflows
// agree on the trigger without those consumers would look like consistency and
// publish nothing.
//
// SO THE TRIGGER OPENS IN THE SAME BATCH AS THE CONSUMERS, and this asserts the
// state in between rather than leaving it to be discovered. When that batch
// lands, this test is REWRITTEN to require the pattern (as PN's is renamed and
// flipped) — it does not start passing on its own.
test('the release workflow does not trigger on the product tag scheme yet', () => {
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
    `the release workflow must still trigger on the legacy scheme, or a legacy release runs nothing (found ${patterns.join(', ')})`,
  )
  assert(
    !patterns.includes('release/*'),
    'the release workflow now triggers on product tags; the public Node installer and PSP cmd/compatwatch both resolve a release version from its tag_name and cannot tell a product tag from its version, so a product release started here would be refused late and name nothing. Read the comment on this test and land the consumer work first — and rewrite this test to require the pattern rather than deleting it.',
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
    workflow.includes('version=$(go run github.com/KazuhaHub/passwall-node/deployment/cmd/release-tag "$tag")'),
    'the workflow must derive the version from the pinned release-tag command, not a second shell rule',
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

// A LEGACY TAG MUST KEEP THE CHANNEL A HYPHEN GAVE IT. The split is a no-op for
// the legacy scheme — the pinned command returns the tag as the version, so every
// version-bearing surface gets the same string — EXCEPT where a rule was replaced,
// and the channel is the one place a wrong answer is not recoverable: a stable
// pointer that moved cannot be moved back by editing a workflow.
//
// So the resolved answer is pinned to reproduce the rule it replaced, for the
// shapes that exist today. This is the batch that must not change legacy
// behaviour; a later batch that changes it has to change this test.
test('a legacy tag keeps the publication channel a hyphen always gave it', () => {
  const setup = job('setup')
  const auto = /case "\$PINNED_TAG" in\s*\n([\s\S]*?)\n\s*esac/.exec(setup)
  assert(auto, 'the channel step must read the tag shape for an automatic channel')
  const arms = auto[1]
  // The three legacy/spec cases, verbatim. `v*-*` is a v-prefixed tag with a
  // hyphen, which has always meant a pre-release; a plain `v*` has always meant
  // stable; and anything unrecognised defaults to pre-release, the direction a
  // deliberate promotion can correct.
  const expected = [
    [/v\*-\*\)\s*prerelease=true/, 'a legacy pre-release tag must stay a pre-release'],
    [/v\*\)\s*prerelease=false/, 'a plain legacy tag must stay stable'],
    [/^\s*\*\)\s*prerelease=true/m, 'an unrecognised tag must default to pre-release'],
  ]
  for (const [pattern, message] of expected) {
    assert(pattern.test(arms), message)
  }
})


// This intentionally guards a bounded, canonical workflow layout; the Go
// version package additionally parses the full YAML and validates SDK gates.
function job(name) {
  const marker = `  ${name}:\n`
  const start = workflow.indexOf(marker)
  assert(start >= 0, `missing ${name} job`)
  const tail = workflow.slice(start + marker.length)
  const next = /^  [a-z][a-z-]*:\n/m.exec(tail)
  return tail.slice(0, next?.index ?? tail.length)
}

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
    // Matrix extension is trusted workflow metadata; replace it only for
    // syntax checking, never evaluate GitHub expressions as executable code.
    scripts.push(body.join('\n').replace(/\$\{\{[^}]+\}\}/g, 'fixture'))
  }
  return scripts
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

test('release tag input uses env plus the pinned published canonical validator', () => {
  const setup = job('setup')
  assert(setup.includes('REQUESTED_TAG: ${{ inputs.tag }}'))
  assert(setup.includes('go run github.com/KazuhaHub/passwall-node/deployment/cmd/release-tag "$tag"'))
  assert(setup.includes('test "$GITHUB_REF" = "refs/tags/${tag}"'))
  assert(setup.includes('git rev-parse --verify "refs/tags/${tag}^{commit}"'))
  assert(setup.includes('test "$release_sha" = "$GITHUB_SHA"'))
  assert(setup.includes('gh api --paginate "repos/${GITHUB_REPOSITORY}/releases"'))
  assert(setup.includes('test -z "$existing"'))
  for (const script of literalScripts()) {
    assert(!script.includes('${{ inputs.'), 'untrusted input must never be interpolated into shell source')
  }
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
  const writers = names.filter((name) => /^ {6}(contents|packages|id-token):\s*write\s*$/m.test(job(name)))
  assert(writers.length > 0, 'no job can write; either the workflow changed or this matcher broke')
  for (const name of writers) {
    assert(
      /needs: \[[^\]]*\bcompatibility\b[^\]]*\]/.test(job(name)),
      `${name} can write but does not depend on the compatibility summary`
    )
  }
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

// R10 STEP 5: signature verification and the candidate's test trust chain are
// SEPARATE, and a private candidate must never be made trusted by relaxing the
// publisher. The manual's words are "do not modify production code to trust an
// arbitrary release source for a private candidate".
//
// PSP publishes checksums rather than signatures, so there is no signature step
// to separate here — but the same rule has a checkable form: the publisher must
// still verify what it produced, and nothing in the path may be relaxed to make a
// candidate pass. Each pattern below is a way that rule gets broken quietly.
test('the publisher verifies its own artifacts and relaxes nothing', () => {
  // The checksum verification has to be a real step, not a swallowed one. Asserting
  // only that the command appears is not enough — it still appears with `|| true`
  // appended, and that is precisely the shape a quiet relaxation takes.
  const verifyLines = workflow.split('\n').filter((line) => /sha256sum -c\s+SHA256SUMS\.txt/.test(line))
  assert(verifyLines.length > 0, 'the publisher must verify the checksums it published')
  for (const line of verifyLines) {
    const after = line.slice(line.indexOf('SHA256SUMS.txt') + 'SHA256SUMS.txt'.length)
    assert(!/\|\||&&|;/.test(after), `the checksum verification is followed by more shell, which can swallow its failure: ${line.trim()}`)
  }
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
  // The scheme this project publishes today.
  assert.deepEqual(resolveChannel('v1.0.0'), { prerelease: false, images: true })
  assert.deepEqual(resolveChannel('v0.0.1-beta11'), { prerelease: true, images: true })
  assert.deepEqual(resolveChannel('v1.0.0-rc1'), { prerelease: true, images: true })

  // The scheme it is moving to. A tag with no hyphen is NOT stable by default:
  // publishing a candidate as stable moves a pointer consumers follow, and no
  // later edit takes it back.
  assert.deepEqual(resolveChannel('release/4.0.0'), { prerelease: true, images: true })

  // Not a release tag at all: neither channel may move.
  assert.deepEqual(resolveChannel('1.0.0'), { prerelease: true, images: false })
  assert.deepEqual(resolveChannel('nightly'), { prerelease: true, images: false })

  // An explicit channel overrides the shape, which is what the input is for.
  assert.deepEqual(resolveChannel('release/4.0.0', 'stable'), { prerelease: false, images: true })
  assert.deepEqual(resolveChannel('v1.0.0', 'testing'), { prerelease: true, images: true })
})

test('publisher cache guard rejects implicit defaults and explicit cache restoration', () => {
  for (const [label, mutated] of [
    ['implicit Go cache', workflow.replace('          cache: false\n', '')],
    ['enabled Go cache', workflow.replace('          cache: false', '          cache: true')],
    ['implicit Node cache', workflow.replace('          package-manager-cache: false\n', '')],
    ['enabled Node cache', workflow.replace('          package-manager-cache: false', '          package-manager-cache: true')],
    ['explicit npm cache', workflow.replace('          package-manager-cache: false', '          package-manager-cache: false\n          cache: npm')],
    ['npm cache path', workflow.replace('          package-manager-cache: false', '          package-manager-cache: false\n          cache-dependency-path: web-react/package-lock.json')],
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

  // The recoverable direction for a tag in neither scheme: a deliberate promotion
  // can correct it, and pointing stable at something unreviewed cannot.
  assert(/\*\)\s+prerelease=true/.test(setup), 'an unrecognised tag must default to pre-release')
})
