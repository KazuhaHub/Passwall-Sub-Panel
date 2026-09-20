// @vitest-environment jsdom
import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import type { NodeRelease, NodeReleaseCatalog, NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'
import NodeReleaseSelector from './NodeReleaseSelector'

const endpoint = '/admin/servers/node-releases'
const linux: NativeInstallationSelection = { method: 'linux', os: 'linux', arch: 'amd64' }
const platforms: NodeRelease['platforms'] = [
  { os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' },
  { os: 'darwin', arch: 'amd64' }, { os: 'darwin', arch: 'arm64' },
  { os: 'windows', arch: 'amd64' }, { os: 'windows', arch: 'arm64' },
]
const stable: NodeRelease = {
  version: 'v1.2.3', channel: 'stable', published_at: '2026-09-12T12:36:16Z',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v1.2.3',
  notes: 'Reviewed protocol compatibility; install exactly this tag.', methods: ['linux', 'docker', 'manual'], platforms,
}
const testing: NodeRelease = {
  ...stable, version: 'v1.2.4-beta.1', channel: 'testing',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v1.2.4-beta.1',
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
const chooseTesting = () => choose('admin:servers.native.release_channel', 'admin:servers.native.release_testing')

describe('Passwall Node release selection', () => {
  it('folds publication notes behind an accessible summary only on the compact installation surface', async () => {
    reads([stable])
    mount(<Controlled compact />)
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

  it('requires an exact version choice and displays the official link, date, and plain-text compatibility notes', async () => {
    reads([{ ...stable, notes: '<script>alert("not HTML")</script>\nReviewed contract.' }])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(selected()).toBe('')
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
    expect(screen.getByText(/<script>alert/).querySelector('script')).toBeNull()
    const link = screen.getByRole('link', { name: 'admin:servers.native.release_details' })
    expect(link.getAttribute('href')).toBe(stable.release_url)
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(screen.getByText('admin:servers.native.release_published')).toBeTruthy()
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
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    reads([{ ...stable, methods: ['manual'] }])
    view.rerender(<Controlled enabled={false} />)
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
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

// An upgrade list that includes the version you are on, and older ones, invites
// a request the service refuses — and offering a downgrade as though it were a
// target is how an operator learns to distrust the list rather than the request.
describe('the upgrade list offers only targets that are actually ahead', () => {
  const beta = (version: string): NodeRelease => ({
    ...testing, version,
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
  })

  it('excludes the node’s own version and everything older', async () => {
    reads([beta('v0.0.1-beta9'), beta('v0.0.1-beta10'), beta('v0.0.1-beta11')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="v0.0.1-beta10" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    // The field is disabled until the catalog resolves, so opening it before
    // then opens nothing — wait for it to become usable first.
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    await screen.findByRole('option', { name: 'v0.0.1-beta11' })
    expect(screen.queryByRole('option', { name: 'v0.0.1-beta10' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'v0.0.1-beta9' })).toBeNull()
  })

  it('ranks the dotless prereleases the way the project publishes them', async () => {
    // SemVer ranks beta11 BELOW beta9 on the trailing character. With that rule
    // this list would be empty and the node would be told it is up to date.
    reads([beta('v0.0.1-beta11')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="v0.0.1-beta9" />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    expect(await screen.findByRole('option', { name: 'v0.0.1-beta11' })).toBeTruthy()
  })

  it('offers nothing when the list has nothing ahead of the node', async () => {
    reads([beta('v0.0.1-beta9')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="v0.0.1-beta9" />)
    await waitFor(() => expect(screen.queryByRole('option', { name: 'v0.0.1-beta9' })).toBeNull())
  })
})

// `newerThan` narrows by version, which is a weaker claim than "a path somebody
// walked": a release can be ahead of the node and still be a target no verified
// edge reaches. When the instance names the reachable ones, that is the list.
describe('an explicit target list wins over being merely newer', () => {
  it('offers only the versions the instance named', async () => {
    const beta = (version: string): NodeRelease => ({
      ...testing, version,
      release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
    })
    reads([beta('v0.0.1-beta10'), beta('v0.0.1-beta11'), beta('v0.0.1-beta12')])
    mount(<NodeReleaseSelector enabled selection={linux} value="" onChange={() => {}}
      initialChannel="testing" newerThan="v0.0.1-beta9" targets={['v0.0.1-beta11']} />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    expect(await screen.findByRole('option', { name: 'v0.0.1-beta11' })).toBeTruthy()
    // Ahead, but nobody walked a path to it.
    expect(screen.queryByRole('option', { name: 'v0.0.1-beta12' })).toBeNull()
    expect(screen.queryByRole('option', { name: 'v0.0.1-beta10' })).toBeNull()
  })

  it('offers nothing when the instance names no reachable target', async () => {
    reads([{ ...testing, version: 'v0.0.1-beta11',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v0.0.1-beta11' }])
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
  const product = (version: string, release_url: string): NodeRelease => ({
    ...stable, version, release_url,
  })
  const official = (version: string) => `https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/${version}`

  it('lists it, selects it and links to its tag page', async () => {
    const release = product('4.0.0', official('4.0.0'))
    reads([release])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion('4.0.0')
    expect(selected()).toBe('4.0.0')
    const link = screen.getByRole('link', { name: 'admin:servers.native.release_details' })
    expect(link.getAttribute('href')).toBe(release.release_url)
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })

  it('refuses a product release whose link is built from its version instead', async () => {
    // tag/4.0.0 is not where a product release lives; tag/release/4.0.0 is.
    reads([product('4.0.0', 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/4.0.0')])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
    expect(selected()).toBe('')
  })

  it('still refuses a product link from anywhere else, and any injected scheme', async () => {
    for (const release_url of [
      'https://github.com/attacker/Passwall-Node/releases/tag/release/4.0.0',
      'javascript:alert(1)',
      'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0?download=1',
      'http://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0',
    ]) {
      reads([product('4.0.0', release_url)])
      const view = mount(<Controlled />)
      await screen.findByText('admin:servers.native.release_no_stable')
      expect(screen.queryByRole('link'), release_url).toBeNull()
      view.unmount()
    }
  })

  it('does not turn a version with a path in it into a link', async () => {
    reads([product('4.0.0/../../latest', official('4.0.0'))])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
  })
})

// THE PANEL'S OWN ANSWER WINS.
//
// The catalog now states the tag it published, and a front end that re-derived
// the mapping would be answering a question the panel already answered — with the
// added risk that the two rules disagree. The stated value is used when present;
// the derivation stays for a panel older than the field, which is why the cases
// above (no `release_tag`) still pass.
describe('a release whose tag the panel states', () => {
  it('uses the stated tag rather than deriving one', async () => {
    // THE STATED TAG HAS TO DIFFER FROM THE DERIVED ONE FOR THIS TO PROVE
    // ANYTHING. For a consistent pair they are the same string by construction —
    // `release/` + version — so a case like that passes whichever value is used,
    // and the test would be describing a preference it never exercised. Here the
    // panel's tag and its version disagree, and only the stated value reaches the
    // URL.
    const release: NodeRelease = {
      ...stable, version: '4.0.0', release_tag: 'release/4.0.1',
      release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.1',
    }
    reads([release])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    await chooseVersion('4.0.0')
    expect(selected()).toBe('4.0.0')
    expect(screen.getByRole('link', { name: 'admin:servers.native.release_details' }).getAttribute('href')).toBe(release.release_url)
  })

  it('refuses a stated tag that disagrees with the URL', async () => {
    reads([{
      ...stable, version: '4.0.0', release_tag: 'release/4.0.1',
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
