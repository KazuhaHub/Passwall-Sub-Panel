# Compatibility result validation

Three tools, because they answer different questions:

| Tool | Question |
| --- | --- |
| `plan.mjs` | **Which cases exist**, and what identity does each run against? |
| `check-go-results.mjs` | Was **this case** measured? |
| `check-case-set.mjs` | Was **every case** measured? |

The second and third exist because those failures look nothing alike. A cancelled
matrix leg, or an artifact upload that failed, leaves every report that *did*
arrive perfectly passing — so a gate that only inspected the reports it found
would call that complete.

## `plan.mjs`

```bash
node deploy/compat/plan.mjs --output plan.json      # the full plan
node deploy/compat/plan.mjs --emit versions         # JSON array of versions
node deploy/compat/plan.mjs --emit cases            # JSON array of case objects
```

**The supported version list is not in any of these files, and must not be.** It
is sliced from `docs/compat/node-v4.json` by **position** at `min_supported` —
never by comparing version strings, because `v0.0.1-beta9` sorts above
`v0.0.1-beta11` and a comparison would silently invert the floor. Restating the
list here would make the plan agree with itself while disagreeing with the
manifest the panel ships, which is how a version stops being tested without
anybody deciding it should.

What `docs/compat/verification-v1.json` adds is **identity**: the commit each tag
resolved to when it was first pinned. A version the manifest supports but the
verification file does not pin **fails the plan**, rather than being tested
against whatever the tag points at today. Both workflows check the resolved
commit against the pinned one and refuse to run on a moved tag.

The planner rejects: an unresolvable or empty floor, a duplicate version, a
version with no pinned SHA, a version that is not a release version, a profile
referencing a test set that does not exist, two cases resolving to one id, an
empty required set, and an upgrade edge missing either end or half its schema. An
exclusion must carry a reason; a bare version is not a decision, and hiding a
known-bad release between two good ends of a range is what explicit exclusions
exist to prevent.

## `check-go-results.mjs`

## Why it exists

`go test` exits 0 when every test it selected passed — **and it also exits 0
when it selected none of them**. `-run` that matches nothing, a skip taken
because the fixture variable was never set, and an exit code lost to a pipe all
produce a clean exit and no coverage. The exit code alone therefore cannot tell
"the wire contract holds" from "we did not look".

## Usage

```bash
GOWORK=off go test -json -count=1 -timeout=5m ./internal/service/nodesync \
  -run '^TestLive_RealNode(AgentContract|MigratedServerContract|TaskEvidenceReceipt|TaskExpiryContract)$' \
  > evidence/go-events.jsonl 2> evidence/go-stderr.txt || test_exit=$?

node deploy/compat/check-go-results.mjs \
  --profile node-wire-v1 \
  --input evidence/go-events.jsonl \
  --go-exit-code "$test_exit" \
  --output evidence/result.json
```

The exit code is **captured**, never propagated: the validator has to run even
when the tests failed, because its report is what names the required item that
did not pass. stderr is kept in its own file so a panic or a build error cannot
leak into the JSON stream being parsed.

Give every backend version its own evidence directory. A shared one lets the
last version's log overwrite an earlier one's, so the surviving report describes
whichever happened to run last.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Complete pass: every required test and required subtest ran and passed, nothing unexpected ran, and the Go process exited 0 |
| 1 | Not a pass. `result.json` says which and why |
| 2 | The validator itself could not evaluate — bad arguments, unknown profile, unwritable output |

`--profiles <path>` overrides the profile file (default `deploy/compat/profiles.json`);
it exists so a test can declare a waiver without editing the shipped profiles.

## Profiles

A profile is a **closed set**. Anything that ran without being declared is
reported as unexpected, and an unexpected top-level test makes the run fail —
which is what a `-run` regex that over-matches produces.

```json
{
  "node-wire-v1": {
    "package": "github.com/KazuhaHub/passwall-sub-panel/internal/service/nodesync",
    "required": {
      "TestLive_RealNodeAgentContract": {},
      "TestLive_RealNodeTaskExpiryContract": {
        "subtests": ["late-completion-terminal-replay", "delayed-body-unknown-journal"]
      }
    },
    "notApplicable": {}
  }
}
```

Required subtests are declared separately because a parent passes whenever the
modes it did run passed. Without naming them, a mode that skipped — or never
appeared — is invisible.

### Waivers

`notApplicable` entries need a `reason`; one without it is ignored and the item
stays required. `subtests` **narrows** the waiver to those modes — naming one
mode does not excuse its parent, or a waiver of a single sub-scenario would
silently release the whole test. A declared waiver is reported as N/A and is
never counted as coverage.

## Tests

```bash
node --test deploy/compat/check-go-results.test.mjs deploy/compat/check-case-set.test.mjs
```

Every row of the failure table in the remediation plan's R02 is a case here,
plus the negative controls: an empty log, a package-only log, a same-named test
in the wrong package, a skip, a skipped subtest under a passing parent, a
failure that later reports success, a truncated stream, a non-JSON line, a test
with no terminal state, and a pass with a non-zero Go exit code.

Both suites are wired into the `container` job of `test.yml`, which already runs
the other `deploy/*_test.mjs` guards.

## `check-case-set.mjs`

Runs in the `compatibility` summary job of both workflows, over the artifacts
the per-case jobs uploaded.

```bash
node deploy/compat/check-case-set.mjs --reports evidence --output case-set.json
```

The expected set is **derived** — profiles × the supported versions in
`docs/compat/node-v4.json`, sliced by position at `min_supported` — never read
back out of the reports directory. "Every report I found is fine" is a statement
that is trivially true of an empty directory, and an empty directory is what a
run with no uploads produces.

Reports are found by walking the tree for `result.json` and naming each case
after its directory, so the layout `actions/download-artifact` produces
(`evidence/<artifact-name>/<case-id>/`) works without the gate knowing about it.

| Exit | Meaning |
| --- | --- |
| 0 | Every expected case reported, and its own verdict was a pass |
| 1 | At least one case did not report, or did not pass |
| 2 | The expected set could not be derived, or the result could not be written |

Evidence for cases nobody expected is recorded in `unexpected` rather than
rejected: a name that drifted between the matrix and the profiles shows up there
first. `pinned-source` — the `node-contract` job's evidence — appears there by
design, since it exercises the revision `go.mod` pins rather than a released
tag, and is therefore not one of the per-version cases the panel promises.
