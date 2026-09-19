import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// ---------------------------------------------------------------------------
// A WAIVER IS ATTACHED TO A REQUIRED ITEM, OR IT DOES NOTHING.
//
// `buildExpected` only marks an item waived if the profile already REQUIRES it
// (`if (!expected.has(name)) continue`). So a `notApplicable` entry whose test
// is absent from `required` is silently inert: nothing is reported as N/A, and
// the test — now undeclared — is reported as UNEXPECTED, which fails the run
// when it is top-level.
//
// That is not a hypothetical. Both fail2ban probes were declared N/A and
// omitted from `required`, so the waiver never attached. While the launcher
// exported nothing every test skipped and the run failed as `skip`, which hid
// it; the moment the environment reached `go test` the skip turned into
// `unexpected-test` and the job went red for a reason that reads like the
// adapter is wrong.
//
// The validator's own comment says an ignored waiver leaves "the item still
// required". That holds only when the name is a TYPO of a required one. When
// the item is simply not required, nothing is required and nothing fails — so
// the guard has to live here, over the shipped profiles, rather than in the
// validator's reasoning about what it never sees.
// ---------------------------------------------------------------------------

const PROFILES = ['profiles.json', 'profiles-third-party.json'].map((name) =>
  fileURLToPath(new URL(name, import.meta.url)),
)

test('every declared waiver names an item its profile also requires', () => {
  for (const path of PROFILES) {
    const all = JSON.parse(readFileSync(path, 'utf8'))
    for (const [name, profile] of Object.entries(all)) {
      const required = profile.required ?? {}
      for (const [test, waiver] of Object.entries(profile.notApplicable ?? {})) {
        // `subtests` narrows the waiver to those modes, so the item it must
        // name is the mode, not the parent.
        const names = (waiver.subtests ?? []).length > 0
          ? waiver.subtests.map((sub) => `${test}/${sub}`)
          : [test]
        for (const item of names) {
          assert.ok(
            Object.hasOwn(required, item),
            `${name}: "${item}" is waived but not required, so the waiver does nothing and the test reports as unexpected instead of N/A`,
          )
        }
      }
    }
  }
})

test('every waiver carries a reason, and the profiles keep at least one required item', () => {
  // A waiver without a reason is ignored by the validator, which makes it the
  // same silent no-op as one that names nothing required.
  for (const path of PROFILES) {
    const all = JSON.parse(readFileSync(path, 'utf8'))
    for (const [name, profile] of Object.entries(all)) {
      assert.ok(Object.keys(profile.required ?? {}).length > 0, `${name}: a profile that requires nothing measures nothing`)
      for (const [test, waiver] of Object.entries(profile.notApplicable ?? {})) {
        assert.ok((waiver.reason ?? '') !== '', `${name}: waivers of ${test} need a reason`)
      }
    }
  }
})
