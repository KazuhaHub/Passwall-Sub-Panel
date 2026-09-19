# Compatibility result validation

`check-go-results.mjs` judges one `go test -json` stream against a named profile
and reports whether the run actually covered what the profile requires.

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
node --test deploy/compat/check-go-results.test.mjs
```

Every row of the failure table in the remediation plan's R02 is a case here,
plus the negative controls: an empty log, a package-only log, a same-named test
in the wrong package, a skip, a skipped subtest under a passing parent, a
failure that later reports success, a truncated stream, a non-JSON line, a test
with no terminal state, and a pass with a non-zero Go exit code.
