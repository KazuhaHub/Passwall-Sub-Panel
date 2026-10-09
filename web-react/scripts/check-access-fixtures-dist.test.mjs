import assert from 'node:assert/strict'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'
import { test } from 'node:test'
import { assertNoAccessFixtures } from './check-access-fixtures-dist.mjs'

test('checks nested chunks and source maps, not only the main entry', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'psp-dist-fixture-test-'))
  try {
    const assets = join(directory, 'assets')
    await mkdir(assets)
    await writeFile(join(directory, 'index.html'), '<div id="root"></div>')
    await writeFile(join(assets, 'main.js'), 'console.log("product");')
    await assertNoAccessFixtures(directory)
    for (const [name, marker] of [['hidden.js', 'psp_dev_fixtures'], ['hidden.js.map', 'accessControlFixtures']]) {
      const path = join(assets, name)
      await writeFile(path, marker)
      await assert.rejects(assertNoAccessFixtures(directory), /Development access fixtures leaked/)
      await rm(path)
    }
  } finally {
    const base = resolve(tmpdir())
    assert.ok(resolve(directory).startsWith(`${base}${sep}psp-dist-fixture-test-`))
    await rm(directory, { recursive: true, force: true })
  }
})
