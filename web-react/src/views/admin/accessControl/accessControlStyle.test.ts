import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// THE CLOSED STYLE RULES OF THE ACCESS-CONTROL PAGE (plan §7.0 #2, #3, §7.6).
// Colours come from the theme's M3 tokens and the closed tone table; state is
// drawn by ToneBadge, never <Chip color=…>. A hex literal or an rgba() in these
// files is a colour the table does not have; a coloured Chip is a state the
// theme's MuiChip override will repaint as grey. Scanned: every non-test source
// under views/admin/accessControl and the components this page added.

const here = fileURLToPath(new URL('.', import.meta.url))
const srcRoot = fileURLToPath(new URL('../../../', import.meta.url))

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [path] : []
  })
}

const files = [
  ...sources(here),
  ...['ToneBadge', 'StatusLine', 'KpiTile', 'PolicyField', 'NodePolicyStatusRow'].map(n => join(srcRoot, 'components', `${n}.tsx`)),
  join(srcRoot, 'views/admin/ServerAccessDialog.tsx'),
  join(srcRoot, 'views/admin/risk/drawer/AccessTab.tsx'),
]

// Comments may name a colour; strip them before looking.
const code = (src: string) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1')

describe('accessControl style', () => {
  it('scans a real set of files', () => {
    expect(files.length).toBeGreaterThan(25)
  })
  it.each([
    ['a hex colour', /#[0-9a-fA-F]{3,8}\b/],
    ['an rgb()/rgba() colour', /\brgba?\(/],
    ['a coloured Chip', /<Chip[^>]*\bcolor=/],
  ])('has %s nowhere', (_name, re) => {
    // ToneBadge, StatusLine and the tone tables build colours with alpha() and
    // theme tokens (no literal); this is what holds that.
    const offenders = files.filter(f => re.test(code(readFileSync(f, 'utf8'))))
    expect(offenders).toEqual([])
  })
})
