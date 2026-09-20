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
    const older = release('4.2.1')
    const highest = release('v1.0.0')
    const middle = release('v0.1.0')
    const catalog = [older, highest, middle]
    expect(newerNodeRelease({ panel_version: '4.2.1' }, catalog)).toBe(highest)
    expect(catalog).toEqual([older, highest, middle])
  })

  it('has no badge for equal, older or absent releases', () => {
    const catalog = [release('v1.0.0'), release('v0.9.0')]
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, catalog)).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v2.0.0' }, catalog)).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v1.0.0' }, [])).toBeUndefined()
  })

  // A CHANNEL PROMOTION REFRESHES STATE. IT DOES NOT CREATE AN UPDATE.
  //
  // Promoting v2.0.0 from testing to stable republishes the same bytes under the
  // same version, so a node already running v2.0.0 has nothing to install. This is
  // an acceptance item in the migration plan — "a same-version channel promotion
  // only refreshes state and must not manufacture an update-available notice" —
  // and it is the kind of notice an operator learns to ignore once it is wrong.
  it('manufactures nothing when the same version was promoted between channels', () => {
    const promoted = release('v2.0.0', { channel: 'stable' })
    expect(newerNodeRelease({ panel_version: 'v2.0.0' }, [promoted])).toBeUndefined()
    // Including for the node whose own channel the promotion concerns.
    expect(newerNodeRelease({ panel_version: 'v2.0.0', update_channel: 'beta' }, [promoted])).toBeUndefined()
    // And the promotion does not make an OLDER release look newer either.
    expect(newerNodeRelease({ panel_version: 'v2.0.0', update_channel: 'beta' }, [promoted, release('v1.9.0')])).toBeUndefined()
  })

  it.each(['v1.0.0', 'v1.0.0 (abc1234)', `v1.0.0 (${'A'.repeat(40)})`])('accepts an official daemon identity: %s', panel_version => {
    const next = release('v1.0.1')
    expect(newerNodeRelease({ panel_version }, [next])).toBe(next)
  })

  it.each([
    undefined, '', 'dev', 'dev (abc1234)', ' v1.0.0', 'v1.0.0 ',
    // `1.0.0` USED TO BE IN THIS LIST, and that was the defect rather than the
    // rule: the product scheme stamps exactly that, so a migrated node reported
    // an identity this function refused and the upgrade badge could never
    // appear. The product-scheme block below asserts it is accepted; what stays
    // here is the near misses, which are still not identities.
    '1.0', '04.0.0', '1.0.0.1.2', 'release/1.0.0',
    'v01.0.0', 'v1.0', 'v1.0.0+local', 'v1.0.0-beta.01', 'v1.0.0 (abc123)',
    `v1.0.0 (${'a'.repeat(41)})`, 'v1.0.0 (xyz1234)', 'v1.0.0(abc1234)',
    'v1.0.0 (abc1234) extra', 'v1.0.0\n',
  ])('does not guess an unknown or malformed daemon identity: %s', panel_version => {
    expect(newerNodeRelease({ panel_version }, [release('v2.0.0')])).toBeUndefined()
  })

  // THE SAVED CHANNEL IS A FLOOR, NOT A FILTER, and this test used to assert the
  // opposite: a beta node with only a stable release ahead of it was told there
  // was nothing newer. The migration plan states the rule — "stable users see
  // stable targets only; testing users MAY see released and testing targets" —
  // and the case it exists for is a beta line that has fallen behind: a node on
  // 4.8.0 with 5.0.0-beta.1 and 4.9.0 published was offered only the beta, because
  // the newer STABLE release was filtered out by the channel it was saved on.
  //
  // WHAT IT MUST NOT DO IS DOWNGRADE. A stable target older than what the node
  // runs is not offered, which the strictly-newer comparison enforces — so
  // "may see released targets" cannot become "was moved back onto the stable
  // line".
  it('treats the saved stable or beta channel as a floor, not a filter', () => {
    const stable = release('v1.0.0')
    const beta = release('v2.0.0-beta.1')
    const catalog = [beta, stable]
    expect(newerNodeRelease({ panel_version: '4.2.1' }, catalog)).toBe(stable)
    expect(newerNodeRelease({ panel_version: '4.2.1', update_channel: 'stable' }, catalog)).toBe(stable)
    expect(newerNodeRelease({ panel_version: '4.2.1', update_channel: 'beta' }, catalog)).toBe(beta)
    // A stable node is not offered a testing target.
    expect(newerNodeRelease({ panel_version: '4.2.1', update_channel: 'stable' }, [beta])).toBeUndefined()
    // A beta node IS offered a released one, and the higher version wins whichever
    // channel published it.
    expect(newerNodeRelease({ panel_version: '4.2.1', update_channel: 'beta' }, [stable])).toBe(stable)
    expect(newerNodeRelease({ panel_version: 'v1.0.0', update_channel: 'beta' }, [release('v1.1.0-beta.1'), release('v1.2.0')])?.version).toBe('v1.2.0')
    // And nothing older is offered, so the wider set cannot downgrade.
    expect(newerNodeRelease({ panel_version: 'v1.2.0', update_channel: 'beta' }, [stable])).toBeUndefined()
    expect(newerNodeRelease({ panel_version: 'v1.0.0', update_channel: 'beta' }, [release('v0.9.0'), release('v0.8.0')])).toBeUndefined()
    // An unrecognised saved channel is still refused rather than widened.
    expect(newerNodeRelease({ panel_version: '4.2.1', update_channel: 'testing' as Server['update_channel'] }, catalog)).toBeUndefined()
  })

  // THIS TEST USED TO ASSERT THE OPPOSITE, and the change is the point of it.
  //
  // It was named "uses SemVer rather than natural-number sorting for legacy beta
  // suffixes" and asserted that a node on beta10 was offered beta3, and that a
  // node on beta3 was NOT offered beta10 — which is SemVer's ordering of
  // "beta10" against "beta3", character by character. That is the defect, not
  // the contract: this project publishes beta1..beta11 and its release order is
  // numeric, which is why the panel's admission check, Passwall Node's own
  // comparator and the release catalog were all changed to compare the digit run
  // numerically. A test pinning the old behaviour would have kept this the one
  // place the rule disagreed with the other three.
  it('orders the legacy beta suffixes the way the project publishes them', () => {
    const beta10 = release('4.1.0')
    const beta3 = release('4.0.2')
    expect(newerNodeRelease({ panel_version: '4.0.2', update_channel: 'beta' }, [beta3, beta10])).toBe(beta10)
    expect(newerNodeRelease({ panel_version: '4.1.0', update_channel: 'beta' }, [beta3])).toBeUndefined()
  })

  it('compares numeric dotted beta identifiers numerically', () => {
    const beta10 = release('4.0.7')
    const beta9 = release('4.0.5')
    expect(newerNodeRelease({ panel_version: '4.0.5 (abc1234)', update_channel: 'beta' }, [beta9, beta10])).toBe(beta10)
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

// The dotless prerelease form is what this project publishes, and plain SemVer
// gets it backwards: "beta11" compares as less than "beta9" on the trailing
// character, so a node on beta9 was told there was no newer release. The same
// defect was fixed in the panel's admission check and in Passwall Node's own
// comparator; this is the third place the rule is applied, and it must be the
// same rule rather than a fourth opinion.
describe('newerNodeRelease and the published prerelease tags', () => {
  it('treats beta11 as newer than beta9', () => {
    const beta11 = release('4.1.1')
    expect(newerNodeRelease({ panel_version: '4.0.6', update_channel: 'beta' }, [beta11])).toBe(beta11)
  })

  it('treats beta10 as newer than beta9 and older than beta11', () => {
    const beta10 = release('4.1.0')
    const beta11 = release('4.1.1')
    expect(newerNodeRelease({ panel_version: '4.0.6', update_channel: 'beta' }, [beta10, beta11])).toBe(beta11)
    expect(newerNodeRelease({ panel_version: '4.1.0', update_channel: 'beta' }, [beta11])).toBe(beta11)
  })

  it('does not offer a beta the node is already on or past', () => {
    const beta11 = release('4.1.1')
    expect(newerNodeRelease({ panel_version: '4.1.1', update_channel: 'beta' }, [beta11])).toBeUndefined()
    const beta9 = release('4.0.6')
    expect(newerNodeRelease({ panel_version: '4.1.0', update_channel: 'beta' }, [beta9])).toBeUndefined()
  })

  it('picks the highest of several published betas, not the lexically first', () => {
    // A catalog ordered newest-published-first puts beta11 ahead of beta9; a
    // comparator that disagrees with that order would pick beta9 again whenever
    // the catalog arrived newest-first.
    const catalog = [release('4.1.1'), release('4.1.0'), release('4.0.6')]
    expect(newerNodeRelease({ panel_version: '4.0.6', update_channel: 'beta' }, catalog)?.version).toBe('4.1.1')
    expect(newerNodeRelease({ panel_version: '4.0.0', update_channel: 'beta' }, [...catalog].reverse())?.version).toBe('4.1.1')
  })
})

// THE PRODUCT SCHEME, end to end through this function.
//
// Every string this function reads is a VERSION, not a tag: the daemon is
// stamped with the release version, and the catalog's `version` field is the
// version too. A product version has no v prefix, so a rule that required one
// found no release for any product release — the badge simply never appeared,
// which is the failure mode that looks like "no upgrade available".
//
// The URLs are the other half: the release PAGE is addressed by the TAG, and the
// backend now builds it from the tag, so a product release's URL contains
// `tag/release/4.0.0` while its `version` is `4.0.0`.
describe('newerNodeRelease, product scheme', () => {
  function product(version: string, overrides: Partial<NodeRelease> = {}): NodeRelease {
    return release(version, {
      release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/${version}`,
      ...overrides,
    })
  }

  it.each(['4.0.0', '4.0.0 (dc5270c)', `4.0.0 (${'A'.repeat(40)})`])('accepts a product daemon identity: %s', panel_version => {
    const next = product('4.0.1')
    expect(newerNodeRelease({ panel_version }, [next])).toBe(next)
  })

  it('does not offer a product release the node is already on or past', () => {
    const catalog = [product('4.0.0'), product('3.9.0')]
    expect(newerNodeRelease({ panel_version: '4.0.0' }, catalog)).toBeUndefined()
    expect(newerNodeRelease({ panel_version: '4.0.1' }, catalog)).toBeUndefined()
  })

  it('picks the highest newer product release', () => {
    const catalog = [product('4.0.1'), product('4.1.0'), product('4.0.2')]
    expect(newerNodeRelease({ panel_version: '4.0.0' }, catalog)?.version).toBe('4.1.0')
  })

  // THE CHANNEL IS METADATA, NOT SOMETHING THE VERSION TEXT CARRIES. A legacy
  // version spells its channel in a prerelease suffix; a product version has
  // none, because a candidate is distinguished by the channel it was published
  // to. Requiring the text to agree made every product release invisible to a
  // beta-channel node, which is the only node that is supposed to see one.
  it('offers a testing product release to a beta channel and not to a stable one', () => {
    const candidate = product('4.0.1', { channel: 'testing' })
    expect(newerNodeRelease({ panel_version: '4.0.0', update_channel: 'beta' }, [candidate])).toBe(candidate)
    expect(newerNodeRelease({ panel_version: '4.0.0', update_channel: 'stable' }, [candidate])).toBeUndefined()
  })

  it('does not guess a malformed product identity', () => {
    for (const panel_version of ['4.0', '04.0.0', '4.0.0.1.2', '4.0.0+local', 'release/4.0.0', ' 4.0.0', '4.0.0 ']) {
      expect(newerNodeRelease({ panel_version }, [product('4.1.0')])).toBeUndefined()
    }
  })

  // A LEGACY NODE STILL SEES PRODUCT RELEASES, and the two schemes are compared
  // at the release line: a v0.x build is behind a 4.0.0 release. The channel
  // still decides — a beta node is offered testing releases only, which is what
  // the existing legacy cases pin too.
  it('offers a testing product release to a legacy node, and nothing to a product node from the legacy line', () => {
    const modern = product('4.0.0', { channel: 'testing' })
    expect(newerNodeRelease({ panel_version: '4.1.1', update_channel: 'beta' }, [modern])?.version).toBe('4.0.0')
    const legacy = release('4.1.1')
    expect(newerNodeRelease({ panel_version: '4.0.0', update_channel: 'beta' }, [legacy])).toBeUndefined()
  })
})
