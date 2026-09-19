import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')

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
  assert(docker.includes("value=latest,enable=${{ startsWith(needs.setup.outputs.tag, 'v') && !contains(needs.setup.outputs.tag, '-') }}"))
  assert(docker.includes("value=beta,enable=${{ startsWith(needs.setup.outputs.tag, 'v') }}"))
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
