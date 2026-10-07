import { readdir, readFile } from 'node:fs/promises'
import { extname, join } from 'node:path'

const forbidden = ['accessControlFixtures', 'psp_dev_fixtures', 'psp_dev_access_state', 'fixture_route_not_implemented']

export async function assertNoAccessFixtures(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) await assertNoAccessFixtures(path)
    else if (entry.isFile() && ['.js', '.css', '.html', '.map'].includes(extname(entry.name))) {
      const source = await readFile(path, 'utf8')
      const marker = forbidden.find(value => source.includes(value) || entry.name.includes(value))
      if (marker) throw new Error(`Development access fixtures leaked into production: ${marker} in ${path}`)
    }
  }
}
