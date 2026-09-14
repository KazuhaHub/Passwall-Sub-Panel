import { describe, expect, it } from 'vitest'
import type { NodeRelease } from '@/api/nodeReleases'
import type { Server } from '@/api/servers'
import { newerNodeRelease } from './nodeReleaseUpdate'

function release(version: string, overrides: Partial<NodeRelease> = {}): NodeRelease {
  return {
    version,
    channel: version.includes('-') ? 'testing' : 'stable',
    published_at: '2026-09-14T00:00:00Z',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
    notes: '',
    methods: ['linux', 'docker', 'manual'],
    platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
    ...overrides,
  }
}

describe('newerNodeRelease', () => {
  it('finds the highest newer reviewed release without changing catalog order', () => {
    const older = release('v0.0.1')
    const highest = release('v1.0.0')
    const middle = release('v0.1.0')
    const catalog = [older, highest, middle]
    expect(newerNodeRelease({ panel_version: 'v0.0.1' }, catalog)).toBe(highest)
    expect(catalog).toEqual([older, highest, middle])
  })

  it('has no badge for equal, older or absent releases', () => {
    const catalog = [release('v1.0.0'), release('v0.9.0')]
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, catalog)).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v2.0.0' }, catalog)).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [])).toBeUndefined()
  })

  it.each(['v1.0.0', 'v1.0.0 (abc1234)', `v1.0.0 (${'A'.repeat(40)})`])('accepts an official daemon identity: %s', panel_version => {
    const next = release('v1.0.1')
    expect(newerNodeRelease({ panel_version }, [next])).toBe(next)
  })

  it.each([
    undefined, '', 'dev', 'dev (abc1234)', '1.0.0', ' v1.0.0', 'v1.0.0 ',
    'v01.0.0', 'v1.0', 'v1.0.0+local', 'v1.0.0-beta.01', 'v1.0.0 (abc123)',
    `v1.0.0 (${'a'.repeat(41)})`, 'v1.0.0 (xyz1234)', 'v1.0.0(abc1234)',
    'v1.0.0 (abc1234) extra', 'v1.0.0\n',
  ])('does not guess an unknown or malformed daemon identity: %s', panel_version => {
    expect(newerNodeRelease({ panel_version }, [release('v2.0.0')])).toBeUndefined()
  })

  it('strictly follows the saved stable or beta channel, including legacy omission', () => {
    const stable = release('v1.0.0')
    const beta = release('v2.0.0-beta.1')
    const catalog = [beta, stable]
    expect(newerNodeRelease({ panel_version: 'v0.0.1' }, catalog)).toBe(stable)
    expect(newerNodeRelease({ panel_version: 'v0.0.1', update_channel: 'stable' }, catalog)).toBe(stable)
    expect(newerNodeRelease({ panel_version: 'v0.0.1', update_channel: 'beta' }, catalog)).toBe(beta)
    expect(newerNodeRelease({ panel_version: 'v0.0.1', update_channel: 'stable' }, [beta])).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v0.0.1', update_channel: 'beta' }, [stable])).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v0.0.1', update_channel: 'testing' as Server['update_channel'] }, catalog)).toBeUndefined()
  })

  it('uses SemVer rather than natural-number sorting for legacy beta suffixes', () => {
    const beta10 = release('v0.0.1-beta10')
    const beta3 = release('v0.0.1-beta3')
    expect(newerNodeRelease({ panel_version: 'v0.0.1-beta10', update_channel: 'beta' }, [beta3, beta10])).toBe(beta3)
    expect(newerNodeRelease({ panel_version: 'v0.0.1-beta3', update_channel: 'beta' }, [beta10])).toBeUndefined()
  })

  it('compares numeric dotted beta identifiers numerically', () => {
    const beta10 = release('v0.0.1-beta.10')
    const beta9 = release('v0.0.1-beta.9')
    expect(newerNodeRelease({ panel_version: 'v0.0.1-beta.9 (abc1234)', update_channel: 'beta' }, [beta9, beta10])).toBe(beta10)
  })

  it.each([
    { methods: ['manual'] },
    { methods: ['docker', 'manual'] },
    { methods: [] },
    { platforms: [{ os: 'linux', arch: 'amd64' }] },
    { platforms: [{ os: 'linux', arch: 'arm64' }] },
    { platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'windows', arch: 'arm64' }] },
    { platforms: [] },
  ] satisfies Partial<NodeRelease>[])('rejects releases unsupported by the Linux upgrade selector: %j', overrides => {
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [release('v2.0.0', overrides)])).toBeUndefined()
  })

  it('requires channel metadata to agree with actual prerelease status', () => {
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [release('v2.0.0-beta.1', { channel: 'stable' })])).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v1.0.0', update_channel: 'beta' }, [release('v2.0.0', { channel: 'testing' })])).toBeUndefined()
  })

  it.each([
    '2.0.0', 'v02.0.0', 'v2.0', 'v2.0.0+build', 'v2.0.0-beta.01', 'v2.0.0-beta..1',
    'v2.0.0-', 'v2.0.0 (abc1234)', ' v2.0.0', 'v2.0.0 ',
  ])('rejects noncanonical or invalid candidate tags: %s', version => {
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [release(version)])).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v1.0.0', update_channel: 'beta' }, [release(version)])).toBeUndefined()
  })

  it.each([
    'https://github.com/attacker/Passwall-Node/releases/tag/v2.0.0',
    'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v2.0.1',
    'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v2.0.0?download=1',
    'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v2.0.0#notes',
    'http://github.com/KazuhaHub/Passwall-Node/releases/tag/v2.0.0',
  ])('rejects metadata that does not link exactly to the official tag: %s', release_url => {
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [release('v2.0.0', { release_url })])).toBeUndefined()
  })
})
