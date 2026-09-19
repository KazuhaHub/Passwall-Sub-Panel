import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

// ---------------------------------------------------------------------------
// A NUL BYTE IN A SOURCE FILE MAKES IT INVISIBLE TO grep.
//
// `grep` and `file` classify a file containing NUL as binary. Under `grep -r`
// the file is then SKIPPED, and — this is the part that costs real time — the
// command exits 1 with no output, which is the same result as "the string is
// not in the repository". There is no warning to notice. An agent or a reviewer
// searching for a symbol concludes it does not exist.
//
// This happened: `deploy/compat/check-go-results.mjs` builds its composite keys
// as `${pkg}\x00${name}` where the `\x00` is a literal NUL written into the
// source rather than the two-character escape. The code is correct — a NUL is a
// fine separator and JavaScript accepts it — and that is exactly why nothing
// caught it. A search for `unexpected` across `deploy/compat/` returned nothing
// from the one file that implements it.
//
// The escape sequence is the whole fix: `\u0000` compiles to the same string
// and keeps the file text.
// ---------------------------------------------------------------------------

const repoRoot = fileURLToPath(new URL('..', import.meta.url))

// Binary assets are not the subject: a PNG legitimately contains NUL. This is an
// allowlist of the extensions a reader would expect to be searchable as text.
const SEARCHABLE = /\.(?:go|mjs|cjs|js|jsx|ts|tsx|json|md|sh|bash|zsh|yml|yaml|css|scss|html|sql|mod|sum|txt|example)$/

test('no tracked text file carries a NUL byte', () => {
  const tracked = execFileSync('git', ['ls-files', '-z'], { cwd: repoRoot, encoding: 'utf8' })
    .split('\0')
    .filter((file) => file !== '' && SEARCHABLE.test(file))

  const offenders = tracked.filter((file) => readFileSync(join(repoRoot, file)).includes(0))

  assert.deepEqual(
    offenders,
    [],
    `these files contain a NUL byte, so grep and file treat them as binary and skip them silently: ${offenders.join(', ')}`,
  )
})
