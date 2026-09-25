// @vitest-environment jsdom
import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import type { NodeRelease, NodeReleaseCatalog, NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'
import NodeReleaseSelector from './NodeReleaseSelector'
import { releaseTag } from '@/utils/productVersion'

const endpoint = '/admin/servers/node-releases'
const linux: NativeInstallationSelection = { method: 'linux', os: 'linux', arch: 'amd64' }
const platforms: NodeRelease['platforms'] = [
  { os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' },
  { os: 'darwin', arch: 'amd64' }, { os: 'darwin', arch: 'arm64' },
  { os: 'windows', arch: 'amd64' }, { os: 'windows', arch: 'arm64' },
]
// THE ADDRESS IS STATED, WHICH IS WHAT THE API DOES. A catalog entry carries the
// tag its release is published at, and the panel builds the page URL from that tag.
// Deriving it in a fixture would be a second implementation of the rule the
// component is being tested against — and a version no longer determines an
// address, so the derivation is not the answer for the releases published before
// the namespace changed.
const stable: NodeRelease = {
  version: '4.1.0', channel: 'stable', published_at: '2026-09-12T12:36:16Z',
  release_tag: 'v4.1.0',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v4.1.0',
  notes: 'Reviewed protocol compatibility; install exactly this tag.',
  // THE METHODS A RELEASE ADVERTISES ARE ITS ASSETS, and the panel's catalog reader
  // stopped listing `docker` among them: an image is not an asset the release page
  // can be asked about. The fixture said otherwise, which is why a Docker
  // installation could look covered here while being impossible in the product.
  methods: ['linux', 'manual'], platforms,
}
const testing: NodeRelease = {
  ...stable, version: '4.1.1', channel: 'testing',
  release_tag: 'v4.1.1',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v4.1.1',
}

function reads(releases: NodeRelease[]) {
  installReads({ [endpoint]: { releases, checked_at: '2026-09-12T13:00:00Z' } satisfies NodeReleaseCatalog })
}

function Controlled({ enabled = true, selection = linux, disabled = false, initialChannel = 'stable', compact = false }: {
  enabled?: boolean; selection?: NativeInstallationSelection; disabled?: boolean; initialChannel?: NodeReleaseChannel; compact?: boolean
}) {
  const [version, setVersion] = useState('')
  const [, refresh] = useState(0)
  return <>
    <NodeReleaseSelector enabled={enabled} selection={selection} value={version} onChange={setVersion} disabled={disabled} initialChannel={initialChannel} compact={compact} />
    <span data-testid="selected-version">{version}</span>
    <button onClick={() => refresh(value => value + 1)}>refresh selector parent</button>
  </>
}

function selected() { return screen.getByTestId('selected-version').textContent }

async function choose(label: string, option: string) {
  const field = screen.getByRole('combobox', { name: label })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: option }))
}

const chooseVersion = (version: string) => choose('admin:servers.native.agent_version', version)
// Publication details are folded on every surface; open them before reading them.
const openReview = () => fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.release_review' }))
const chooseTesting = () => choose('admin:servers.native.release_channel', 'admin:servers.native.release_testing')

describe('Passwall Node release selection', () => {
  it.each([
    { surface: 'compact installation', compact: true },
    { surface: 'upgrade', compact: false },
  ])('folds publication notes behind an accessible summary on the $surface surface', async ({ compact }) => {
    reads([stable])
    mount(<Controlled compact={compact} />)
    await chooseVersion(stable.version)
    expect(screen.queryByText(stable.notes)).toBeNull()
    expect(screen.queryByRole('link', { name: 'admin:servers.native.release_details' })).toBeNull()
    const summary = screen.getByRole('button', { name: 'admin:servers.native.release_review' })
    expect(summary.tagName).toBe('BUTTON')
    expect(summary.getAttribute('aria-expanded')).toBe('false')
    expect(summary.getAttribute('tabindex')).not.toBe('-1')
    fireEvent.click(summary)
    expect(summary.getAttribute('aria-expanded')).toBe('true')
    expect(await screen.findByText(stable.notes)).toBeTruthy()
    expect(screen.getByRole('link', { name: 'admin:servers.native.release_details' }).getAttribute('href')).toBe(stable.release_url)
    expect(selected()).toBe(stable.version)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('opens the saved testing preference without preselecting a version or making any secret/write request', async () => {
    reads([testing, stable])
    const view = mount(<Controlled initialChannel="testing" />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
    expect(selected()).toBe('')
    await choose('admin:servers.native.release_channel', 'admin:servers.native.release_stable')
    await chooseVersion(stable.version)
    fireEvent.click(screen.getByRole('button', { name: 'refresh selector parent' }))
    expect(selected()).toBe(stable.version)
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_stable')
    view.rerender(<Controlled enabled={false} initialChannel="testing" />)
    view.rerender(<Controlled initialChannel="testing" />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(selected()).toBe('')
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
    expect(api.get.mock.calls.every(([url]) => url === endpoint)).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('starts stable and does not silently fall back when only testing releases exist', async () => {
    reads([testing])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).getAttribute('aria-disabled')).toBe('true')
    await chooseTesting()
    expect(selected()).toBe('')
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
    expect(api.get.mock.calls.every(([url]) => url === endpoint)).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('defaults Docker to the selected floating channel tag and still allows an exact rollback pin', async () => {
    reads([testing, stable])
    mount(<Controlled selection={{ ...linux, method: 'docker' }} initialChannel="testing" />)
    await waitFor(() => expect(selected()).toBe('beta'))
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).textContent)
      .toContain('admin:servers.native.release_follow_testing')
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
    await choose('admin:servers.native.release_channel', 'admin:servers.native.release_stable')
    await waitFor(() => expect(selected()).toBe('latest'))
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).textContent)
      .toContain('admin:servers.native.release_follow_stable')
    expect(api.post).not.toHaveBeenCalled()
  })

  it('requires an exact version choice and displays the official link, date, and notes without raw HTML', async () => {
    reads([{ ...stable, notes: '<script>alert("not HTML")</script>\n\nReviewed contract.' }])
    const view = mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(selected()).toBe('')
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
    openReview()
    expect(await screen.findByText('Reviewed contract.')).toBeTruthy()
    // Raw HTML in a release body is dropped, neither executed nor shown as markup.
    expect(view.container.querySelector('script')).toBeNull()
    expect(screen.queryByText(/alert\(/)).toBeNull()
    const link = screen.getByRole('link', { name: 'admin:servers.native.release_details' })
    expect(link.getAttribute('href')).toBe(stable.release_url)
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(screen.getByText('admin:servers.native.release_published')).toBeTruthy()
  })

  // THE NOTES ARE A GITHUB RELEASE BODY, which is Markdown: GitHub's generated
  // changelog is a heading, one bulleted pull request per line, and a bold
  // "Full Changelog" line, with bare URLs that GitHub autolinks. Shown as text,
  // the operator reads "## What's Changed" and asterisks.
  it('renders the release body as Markdown with safe links and no remote images', async () => {
    reads([{ ...stable, notes: [
      "## What's Changed",
      '* The example compose grants FOWNER by @KKazuhaK in https://github.com/KazuhaHub/Passwall-Node/pull/58',
      '* Root must not hand the runtime directory away by @KKazuhaK in https://github.com/KazuhaHub/Passwall-Node/pull/59',
      '',
      '**Full Changelog**: https://github.com/KazuhaHub/Passwall-Node/compare/v4.0.1.4...v4.0.1.5',
      '',
      '[not a link](javascript:alert(1))',
      '',
      '![tracking pixel](https://tracker.example/pixel.png)',
    ].join('\n') }])
    const view = mount(<Controlled />)
    await chooseVersion(stable.version)
    openReview()

    const heading = await screen.findByRole('heading', { name: "What's Changed" })
    expect(heading.textContent).not.toContain('#')
    const items = screen.getAllByRole('listitem')
    expect(items.map(item => item.textContent)).toEqual([
      'The example compose grants FOWNER by @KKazuhaK in https://github.com/KazuhaHub/Passwall-Node/pull/58',
      'Root must not hand the runtime directory away by @KKazuhaK in https://github.com/KazuhaHub/Passwall-Node/pull/59',
    ])
    expect(screen.getByText('Full Changelog').tagName).toBe('STRONG')
    expect(view.container.textContent).not.toContain('**')

    const pull = screen.getByRole('link', { name: 'https://github.com/KazuhaHub/Passwall-Node/pull/58' })
    expect(pull.getAttribute('href')).toBe('https://github.com/KazuhaHub/Passwall-Node/pull/58')
    expect(pull.getAttribute('target')).toBe('_blank')
    expect(pull.getAttribute('rel')).toBe('noopener noreferrer')
    expect(screen.getByRole('link', { name: 'https://github.com/KazuhaHub/Passwall-Node/compare/v4.0.1.4...v4.0.1.5' })).toBeTruthy()

    // Only http(s) targets become links, and a remote image is never fetched:
    // opening the dialog must not report the operator's address to a third party.
    expect(screen.queryByRole('link', { name: 'not a link' })).toBeNull()
    expect(screen.getByText('not a link')).toBeTruthy()
    expect(view.container.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(view.container.querySelector('img')).toBeNull()
    expect(screen.getByText('tracking pixel')).toBeTruthy()
  })

  it('clears the exact selection when changing channels and does not auto-select the other channel', async () => {
    reads([testing, stable])
    mount(<Controlled />)
    await chooseVersion(stable.version)
    await chooseTesting()
    expect(selected()).toBe('')
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
  })

  it('clears a selected version on platform changes even when that version supports both platforms', async () => {
    reads([stable])
    const view = mount(<Controlled selection={{ ...linux, method: 'manual' }} />)
    await chooseVersion(stable.version)
    view.rerender(<Controlled selection={{ method: 'manual', os: 'windows', arch: 'arm64' }} />)
    await waitFor(() => expect(selected()).toBe(''))
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
  })

  it('filters manual versions by exact OS and architecture', async () => {
    reads([{ ...stable, platforms: [{ os: 'linux', arch: 'amd64' }] }])
    mount(<Controlled selection={{ method: 'manual', os: 'windows', arch: 'arm64' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
  })

  it('requires the selected recipe and both Linux architectures for auto-detecting installations', async () => {
    reads([{ ...stable, platforms: [{ os: 'linux', arch: 'amd64' }] }])
    const view = mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    // A DOCKER INSTALLATION DETECTS THE HOST ARCHITECTURE TOO, so a release
    // published for only one of the two Linux architectures is not offerable to it
    // either — the recipe is what differs, not the platform rule.
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    // AND THE RECIPE RULE IS ABOUT THE RECIPES. A release that does not advertise
    // `manual` is not offerable to a manual installation; a Docker installation
    // reads an image, so it needs no entry among the release's assets at all.
    reads([{ ...stable, methods: ['linux'] }])
    view.rerender(<Controlled enabled={false} />)
    view.rerender(<Controlled selection={{ ...linux, method: 'manual' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    view.rerender(<Controlled enabled={false} />)
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await waitFor(() => expect(selected()).toBe('latest'))
  })

  it('distinguishes a lookup failure from an empty channel and lets the administrator retry', async () => {
    api.get.mockRejectedValue(new Error('Rate limit'))
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_failed')
    expect(screen.queryByText('admin:servers.native.release_no_stable')).toBeNull()
    reads([stable])
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.release_retry' }))
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
    expect(screen.queryByText('admin:servers.native.release_failed')).toBeNull()
  })

  it('aborts closed requests and ignores late responses after the selector is enabled again', async () => {
    const requests: { signal: AbortSignal; resolve: (value: { data: NodeReleaseCatalog }) => void }[] = []
    api.get.mockImplementation((_url: string, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ signal: options.signal, resolve })
    }))
    const view = mount(<Controlled />)
    expect(screen.getByRole('status')).toBeTruthy()
    const oldRequests = [...requests]
    view.rerender(<Controlled enabled={false} />)
    expect(oldRequests.every(request => request.signal.aborted)).toBe(true)
    view.rerender(<Controlled />)
    const current = requests.filter(request => !request.signal.aborted).at(-1)!
    await act(async () => {
      current.resolve({ data: { releases: [testing], checked_at: '' } })
      oldRequests.forEach(request => request.resolve({ data: { releases: [stable], checked_at: '' } }))
    })
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    view.unmount()
    expect(current.signal.aborted).toBe(true)
  })

  it('does not fetch any metadata or secrets while disabled by lifecycle', async () => {
    mount(<Controlled enabled={false} />)
    expect(api.get).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  it('does not expose versions with untrusted external release links', async () => {
    reads([{ ...stable, release_url: 'javascript:alert(1)' }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
    expect(selected()).toBe('')
  })

  it('prevents changing channel and version while an installation operation is disabled', async () => {
    reads([stable])
    mount(<Controlled disabled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(screen.getAllByRole('combobox').every(field => field.getAttribute('aria-disabled') === 'true')).toBe(true)
    expect(selected()).toBe('')
  })
})

// An empty list means different things to the two callers, and the difference is
// not cosmetic: an INSTALL list is empty when the channel has nothing for this
// platform, an UPGRADE list when nothing in the channel applies to the node in
// front of you. Saying "no stable releases" to someone upgrading sends them
// looking for a release that is not missing.
describe('the empty state answers the question that was asked', () => {
  it('tells an upgrade caller that nothing applies to this node', async () => {
    reads([])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}} context="upgrade" />)
    expect(await screen.findByText('admin:servers.native.release_no_target_for_node')).toBeTruthy()
    expect(screen.queryByText('admin:servers.native.release_no_stable')).toBeNull()
  })

  it('keeps the channel wording for an install caller', async () => {
    reads([])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}} />)
    expect(await screen.findByText('admin:servers.native.release_no_stable')).toBeTruthy()
    expect(screen.queryByText('admin:servers.native.release_no_target_for_node')).toBeNull()
  })
})

// An upgrade list that includes the version you are on, and older ones, invites
// a request the service refuses — and offering a downgrade as though it were a
// target is how an operator learns to distrust the list rather than the request.
describe('the upgrade list offers only targets that are actually ahead', () => {
  // release_tag HAS TO MOVE WITH THE VERSION. It did not: every beta inherited
  // `testing`'s v4.1.1, so officialReleaseURL — which builds the expected address
  // from the panel-stated tag and compares it to release_url — rejected every
  // release except 4.1.1. The ordering assertions below then passed because the
  // OTHER filter had already removed their subjects, which is the failure mode
  // this whole file exists to catch in the product.
  const beta = (version: string): NodeRelease => ({
    ...testing, version,
    release_tag: releaseTag(version),
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag(version)}`,
  })

  it('lists an older release and marks it rather than hiding it', async () => {
    // NEWEST FIRST, because that is what the catalog returns — it reads GitHub's
    // release list, which is ordered by publication. "Recommended" is the first
    // option that is not older, so this order is load-bearing rather than
    // decorative, and a fixture in the other order would assert the wrong thing.
    reads([beta('4.1.1'), beta('4.1.0'), beta('4.0.6')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="4.1.0" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    // The field is disabled until the catalog resolves, so opening it before
    // then opens nothing — wait for it to become usable first.
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)

    // THE PANEL PERMITS A DOWNGRADE, so the list may not pretend otherwise. The
    // write path has no ordering rule at all and records why: the signature, the
    // checksum and the node's own state-schema check are what guard the choice.
    // Hiding these made the browser the only place a rule lived.
    const older = await screen.findByRole('option', { name: '4.0.6' })
    expect(older.textContent).toContain('admin:servers.native.release_older_than_current')

    // The newest is still the one marked recommended.
    const newest = screen.getByRole('option', { name: '4.1.1' })
    expect(newest.textContent).toContain('admin:servers.native.release_recommended')
    expect(newest.textContent).not.toContain('admin:servers.native.release_older_than_current')

    // The node's own version is neither older nor recommended: it is simply not
    // ahead. Excluding it is the SERVER's job — agentTargets omits it — so the
    // selector does not duplicate that rule.
    expect(screen.getByRole('option', { name: '4.1.0' }).textContent)
      .not.toContain('admin:servers.native.release_older_than_current')
  })

  it('never auto-selects a release older than the node', async () => {
    // Offering a downgrade is fine. Pre-selecting one, and labelling it
    // "recommended", is how an operator ends up installing it by pressing return.
    const onChange = vi.fn()
    reads([beta('4.0.6')])
    mount(<NodeReleaseSelector enabled autoSelectLatest selection={linux} value="" onChange={onChange}
      initialChannel="testing" newerThan="4.1.0" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    const only = await screen.findByRole('option', { name: '4.0.6' })
    expect(only.textContent).toContain('admin:servers.native.release_older_than_current')
    expect(only.textContent).not.toContain('admin:servers.native.release_recommended')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('ranks a release above the node it is ahead of', async () => {
    // The comparison is the project's own, on parsed versions: a string ordering
    // is what puts 4.0.10 below 4.0.9, and a node ahead of its own upgrade would
    // be told it is up to date.
    reads([beta('4.1.1')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="4.0.6" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    const ahead = await screen.findByRole('option', { name: '4.1.1' })
    expect(ahead.textContent).not.toContain('admin:servers.native.release_older_than_current')
  })
})

// `newerThan` narrows by version, which is a weaker claim than "a path somebody
// walked": a release can be ahead of the node and still be a target no verified
// edge reaches. When the instance names the reachable ones, that is the list.
describe('an explicit target list wins over being merely newer', () => {
  it('offers only the versions the instance named', async () => {
    const beta = (version: string): NodeRelease => ({
      ...testing, version,
      release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag(version)}`,
    })
    reads([beta('4.1.0'), beta('4.1.1'), beta('4.2.0')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="4.0.6" targets={['4.1.1']} />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    expect(await screen.findByRole('option', { name: '4.1.1' })).toBeTruthy()
    // Ahead, but nobody walked a path to it.
    expect(screen.queryByRole('option', { name: '4.2.0' })).toBeNull()
    expect(screen.queryByRole('option', { name: '4.1.0' })).toBeNull()
  })

  it('offers nothing when the instance names no reachable target', async () => {
    reads([{ ...testing, version: '4.1.1',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/4.1.1' }])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" targets={[]} />)
    // An empty list is the honest answer here: nothing the instance will accept.
    expect(await screen.findByText('admin:servers.native.release_no_testing')).toBeTruthy()
  })
})

// THE PRODUCT SCHEME. Everything here is a VERSION — the catalog's version field
// — while the release PAGE is addressed by the TAG, which the backend builds
// from the tag since the two identities were split.
//
// The guard below used to require a v-prefixed version and to rebuild the
// expected URL from the version, so every product release failed it. That is not
// a broken link: the URL check is a FILTER, so the release did not appear in the
// list at all, and an operator with nothing to choose from concludes there is
// nothing to install.
describe('a product-scheme release, whose page is addressed by its tag', () => {
  const product = (version: string, tag: string): NodeRelease => ({
    ...stable, version, release_tag: tag,
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${tag}`,
  })

  it('lists it, selects it and links to its tag page', async () => {
    const release = product('4.0.0', 'v4.0.0')
    reads([release])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion('4.0.0')
    expect(selected()).toBe('4.0.0')
    openReview()
    const link = screen.getByRole('link', { name: 'admin:servers.native.release_details' })
    expect(link.getAttribute('href')).toBe(release.release_url)
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })

  // A RELEASE PUBLISHED BEFORE THE NAMESPACE CHANGED IS OFFERED FROM WHERE IT IS.
  // The panel states `release/4.0.1.2` for the four it published there, and a front
  // end that rebuilt the address from the version would offer `v4.0.1.2` — a tag
  // nobody published — or, as here, drop the release from the list entirely.
  it('lists a release published under the historical namespace', async () => {
    const release = product('4.0.1.2', 'release/4.0.1.2')
    reads([release])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion('4.0.1.2')
    expect(selected()).toBe('4.0.1.2')
    openReview()
    expect(screen.getByRole('link', { name: 'admin:servers.native.release_details' }).getAttribute('href')).toBe(release.release_url)
  })

  it('refuses a product release whose link is built from its version instead', async () => {
    // tag/4.0.0 is not where a release lives in any namespace.
    reads([{ ...product('4.0.0', 'v4.0.0'), release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/4.0.0' }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
    expect(selected()).toBe('')
  })

  it('still refuses a product link from anywhere else, and any injected scheme', async () => {
    for (const release_url of [
      'https://github.com/attacker/Passwall-Node/releases/tag/v4.0.0',
      'javascript:alert(1)',
      'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v4.0.0?download=1',
      'http://github.com/KazuhaHub/Passwall-Node/releases/tag/v4.0.0',
    ]) {
      reads([{ ...product('4.0.0', 'v4.0.0'), release_url }])
      const view = mount(<Controlled />)
      await screen.findByText('admin:servers.native.release_no_stable')
      expect(screen.queryByRole('link'), release_url).toBeNull()
      view.unmount()
    }
  })

  it('does not turn a version with a path in it into a link', async () => {
    reads([product('4.0.0/../../latest', 'v4.0.0')])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
  })
})

// THE PANEL'S OWN ANSWER WINS.
//
// The catalog states the tag it published, and a front end that re-derived the
// mapping would be answering a question the panel already answered — with the
// added risk that the two rules disagree. The stated value is used when present;
// the derivation stays for a panel older than the field, which is why the cases
// above (no `release_tag`) still pass.
describe('a release whose tag the panel states', () => {
  it('uses the stated tag rather than deriving one', async () => {
    // THE STATED TAG HAS TO DIFFER FROM THE DERIVED ONE FOR THIS TO PROVE
    // ANYTHING, and it differs for every release published before the namespace
    // changed: the panel states `release/4.0.0` and deriving the version gives
    // `v4.0.0`. Only the stated value reaches the URL.
    const release: NodeRelease = {
      ...stable, version: '4.0.0', release_tag: 'release/4.0.0',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0',
    }
    reads([release])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion('4.0.0')
    expect(selected()).toBe('4.0.0')
    openReview()
    expect(screen.getByRole('link', { name: 'admin:servers.native.release_details' }).getAttribute('href')).toBe(release.release_url)
  })

  it('refuses a stated tag that names another release', async () => {
    // TWO RELEASES DESCRIBED ONCE. The tag is well formed and the version is well
    // formed; they are simply not the same release, and the entry is refused rather
    // than resolved — the version is what the operator selects and what the upgrade
    // comparison ranks, while the tag is where the link goes.
    reads([{
      ...stable, version: '4.0.0', release_tag: 'release/4.0.1',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.1',
    }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('refuses a stated tag that disagrees with the URL', async () => {
    reads([{
      ...stable, version: '4.0.1', release_tag: 'release/4.0.1',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0',
    }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('still refuses a stated tag that is not a release tag', async () => {
    reads([{
      ...stable, version: '4.0.0', release_tag: '4.0.0',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/4.0.0',
    }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
  })
})

// A BETA-PREFERENCE NODE CAN STILL REACH A RELEASED TARGET, and it can here
// WITHOUT this selector changing.
//
// The migration plan says a testing user may be offered released targets as well
// as testing ones, which the NUDGE honours — it no longer filters by the saved
// channel. This surface needs no such change: the channel is the operator's
// explicit choice, and both options are reachable from it. Widening the
// "testing" toggle to list released releases too would make its label say one
// thing and its list another, which is the opposite of the problem being solved.
describe('a node saved on the testing channel', () => {
  it('opens on testing and can still pick a released target by switching', async () => {
    reads([testing, stable])
    mount(<Controlled initialChannel="testing" />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
    // The released target is reachable from the same surface.
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.release_stable' }))
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
  })
})

// THE OPTIONS FETCH CAN FAIL, AND THEN THERE IS NO TARGET LIST AT ALL.
//
// `targets` is undefined whenever the dialog's /upgrade-options call failed, and
// the whole catalog is listed — including the version the node is already on,
// which the server would otherwise have omitted. Recommending that one, and
// auto-selecting it, leaves Confirm disabled with nothing on screen to say why:
// the write path refuses the exact no-op.
describe('the recommendation is strictly ahead, not merely not-older', () => {
  const beta = (version: string): NodeRelease => ({
    ...testing, version,
    release_tag: releaseTag(version),
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag(version)}`,
  })

  it('never recommends the version the node is already running', async () => {
    const onChange = vi.fn()
    reads([beta('4.1.0'), beta('4.0.6')])
    mount(<NodeReleaseSelector enabled autoSelectLatest selection={linux} value="" onChange={onChange}
      initialChannel="testing" newerThan="4.1.0" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)

    // Both are listed — the panel permits a downgrade and does not hide one.
    const own = await screen.findByRole('option', { name: '4.1.0' })
    expect(screen.getByRole('option', { name: '4.0.6' })).toBeTruthy()
    // But neither is recommended, and nothing was chosen on the operator's behalf.
    expect(own.textContent).not.toContain('admin:servers.native.release_recommended')
    expect(onChange).not.toHaveBeenCalled()
  })
})
