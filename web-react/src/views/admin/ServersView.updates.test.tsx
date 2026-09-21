// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import type { NodeReleaseCatalog } from '@/api/nodeReleases'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import ConfirmHost from '@/components/ConfirmHost'
import { useAuthStore } from '@/stores/auth'
import english from '@/locales/en-US/admin.json'
import ServersView from './ServersView'

const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))
vi.mock('./NativeAgentUpgradeDialog', () => ({
  NativeAgentUpgradeDialog: ({ server }: { server: Server | null }) => server
    ? <div role="dialog" aria-label="native-upgrade-preview">{server.id}</div> : null,
}))

const native: Server = {
  id: 7, name: 'native beta', panel_type: 'psp', update_channel: 'beta',
  panel_version: '4.0.2 (abcdef0)', url: '', capabilities: ['core.upgrade'],
  node_compatibility: 'compatible', node_upgrade_ready: true,
  auth_method: '', has_api_token: false, has_password: false, insecure_https: false,
}
const catalog: NodeReleaseCatalog = {
  checked_at: '2026-09-14T00:00:00Z',
  releases: [{
    version: '4.0.3', channel: 'testing', published_at: '2026-09-13T00:00:00Z',
    release_tag: 'release/4.0.3',
    release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.3',
    notes: '', methods: ['linux'], platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
  }],
}
const sui: Server = {
  ...native, id: 8, name: 'S-UI server', panel_type: 'sui', panel_version: '1.5.0',
  url: 'https://sui.example.test', capabilities: [], auth_method: 'token', has_api_token: true,
  latest_sui_version: 'v1.6.2', update_available: true,
}
const xui: Server = {
  ...sui, id: 9, name: '3X-UI server', panel_type: '3xui', panel_version: '3.4.2',
  latest_sui_version: undefined, latest_xui_version: 'v3.7.0',
  capabilities: ['panel.upgrade', 'core.upgrade'],
}

beforeEach(() => {
  // Hooks may return a teardown function; do not accidentally return the mock.
  releaseReads.mockResolvedValue(catalog)
})
const rowFor = async (name: string) => (await screen.findByText(name)).closest('tr')!
const upgradeWrites = () => api.post.mock.calls.filter(([url]) => url !== '/admin/servers/probe')

describe('Server update hints and paired tray icons', () => {
  it('uses matching tray icons for install, Node upgrade and core selection', async () => {
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    fireEvent.click(within(await rowFor(native.name)).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
      .getByTestId('DownloadOutlinedIcon')).toBeTruthy()
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.agent_upgrade.action' }))
      .getByTestId('UploadOutlinedIcon')).toBeTruthy()
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.action.select_core' }))
      .getByTestId('TuneOutlinedIcon')).toBeTruthy()
    expect(screen.queryByTestId('UpgradeIcon')).toBeNull()
    expect(screen.queryByTestId('SystemUpdateAltIcon')).toBeNull()
    expect(screen.queryByText('admin:servers.table.remark')).toBeNull()
    expect(english.servers.install_reinstall.action).toBe('Install / Reinstall')
    expect(english.servers.install_reinstall.title).toBe('Install / Reinstall · {{name}}')
  })

  it('reads one shared catalog, shows only the newer matching-channel Node, and keeps the hint non-interactive', async () => {
    const stable = { ...native, id: 10, name: 'native stable', update_channel: 'stable' as const }
    const current = { ...native, id: 11, name: 'native current', panel_version: '4.0.3' }
    installReads({ '/admin/servers': list([native, stable, current]) })
    mount(<ServersView />)
    const target = await rowFor(native.name)
    expect(await within(target).findByText('admin:servers.update_available')).toBeTruthy()
    const version = within(target).getByText('Passwall Node 4.0.2')
    expect(version).toBeTruthy()
    expect(within(target).queryByText('Passwall Node 4.0.2 (abcdef0)')).toBeNull()
    fireEvent.mouseOver(version)
    expect(await screen.findByText('commit: abcdef0')).toBeTruthy()
    expect(within(target).queryByText('admin:servers.native.compatibility.compatible')).toBeNull()
    expect(within(target).queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    expect(within(await rowFor(stable.name)).queryByText('admin:servers.update_available')).toBeNull()
    expect(within(await rowFor(current.name)).queryByText('admin:servers.update_available')).toBeNull()
    expect(releaseReads).toHaveBeenCalledTimes(1)
    expect(releaseReads.mock.calls[0][0]).toBeInstanceOf(AbortSignal)
    expect(upgradeWrites()).toHaveLength(0)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('distinguishes a catalog failure from an available update and makes no upgrade request', async () => {
    releaseReads.mockRejectedValue(new Error('offline catalog'))
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    expect(await screen.findByText('admin:servers.native.update_check_failed')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('keeps update metadata visible without an upgrade action for an operator', async () => {
    useAuthStore.setState({ role: 'operator' })
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    expect(await screen.findByText('admin:servers.update_available')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    expect(screen.queryByRole('dialog', { name: 'native-upgrade-preview' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('shows limited Node compatibility and blocks remote upgrade without hiding release metadata', async () => {
    const limited = { ...native, node_compatibility: 'limited' as const, node_upgrade_ready: false }
    installReads({ '/admin/servers': list([limited]) })
    mount(<ServersView />)
    const row = await rowFor(limited.name)
    expect(within(row).getByText('admin:servers.native.compatibility.limited')).toBeTruthy()
    // AWAITED, UNLIKE THE BADGE ABOVE. The compatibility badge is server row
    // data and is there when the row renders; the update hint comes from the
    // node release catalog, a second read that resolves afterwards. Asserting it
    // synchronously passed whenever the catalog happened to win the race and
    // failed under load — which is how this test flaked, not the code.
    expect(await within(row).findByText('admin:servers.update_available')).toBeTruthy()
    expect(within(row).queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(screen.getByRole('menuitem', { name: 'admin:servers.agent_upgrade.action' }).getAttribute('aria-disabled')).toBe('true')
    expect(screen.queryByRole('dialog', { name: 'native-upgrade-preview' })).toBeNull()
  })

  it('keeps S-UI and 3X-UI update hints informational and non-interactive', async () => {
    installReads({ '/admin/servers': list([sui, xui]) })
    mount(<ServersView />)
    const suiRow = await rowFor(sui.name)
    expect(within(suiRow).getByText('admin:servers.update_available')).toBeTruthy()
    expect(within(suiRow).queryByRole('link')).toBeNull()
    expect(within(await rowFor(xui.name)).getByText('admin:servers.update_available')).toBeTruthy()
    fireEvent.click(within(suiRow).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.action.upgrade_xray' })).toBeNull()
    expect(releaseReads).not.toHaveBeenCalled()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('merges fresh upstream update fields from a successful probe, including false to clear an old hint', async () => {
    installReads({ '/admin/servers': list([sui]) })
    api.post.mockResolvedValue({ data: { ok: true, panel_version: '1.6.2', latest_sui_version: 'v1.6.2', update_available: false } })
    mount(<ServersView />)
    const target = await rowFor(sui.name)
    await waitFor(() => expect(within(target).getByText('S-UI 1.6.2')).toBeTruthy())
    expect(within(target).queryByRole('link', { name: 'admin:servers.sui_update.available' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('shows a S-UI update on the first page visit when shared metadata finishes after the connection probes', async () => {
    const cold = { ...sui, latest_sui_version: undefined, update_available: undefined }
    const current = { ...cold, id: 10, name: 'current S-UI', panel_version: '1.6.2' }
    installReads({ '/admin/servers': list([cold, current]) })
    const ordinaryRead = api.get.getMockImplementation()!
    let resolveMetadata!: (result: { data: { version: string } }) => void
    api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/servers/sui-release'
      ? new Promise(resolve => { resolveMetadata = resolve }) : ordinaryRead(url, ...args))
    api.post.mockImplementation(async (_url: string, body: { id: number }) => ({
      data: { ok: true, panel_version: body.id === cold.id ? '1.5.0' : '1.6.2' },
    }))
    mount(<ServersView />)
    const target = await rowFor(cold.name)
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2))
    expect(within(target).queryByRole('link')).toBeNull()
    await act(async () => resolveMetadata({ data: { version: 'v1.6.2' } }))
    expect(within(target).getByText('admin:servers.update_available')).toBeTruthy()
    expect(within(target).queryByRole('link')).toBeNull()
    expect(within(await rowFor(current.name)).queryByText('admin:servers.update_available')).toBeNull()
    expect(api.get.mock.calls.filter(([url]) => url === '/admin/servers/sui-release')).toHaveLength(1)
    expect(upgradeWrites()).toHaveLength(0)
  })
})

// The 3X-UI upgrade endpoint takes no version argument, and the API says so with
// target_pinnable=false. The dialog has to repeat it, because a confirm that
// shows a target version and nothing else reads as a promise the API explicitly
// denies — and an admin who believes the promise stops watching the outcome.
describe('the 3X-UI upgrade confirm states what the target is not', () => {
  it('warns that the target cannot be pinned when the API reports it', async () => {
    installReads({
      '/admin/servers': list([xui]),
      '/admin/servers/9/upgrade-preview': {
        update_available: true,
        current_version: '3.4.2',
        target_version: 'v3.7.0',
        compat_status: 'supported',
        can_force: false,
        target_pinnable: false,
        upgrade_mode: 'latest_only',
      },
    })
    mount(<><ConfirmHost /><ServersView /></>)

    const row = await rowFor(xui.name)
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' }))

    // The dialog body is one joined string whose lines are i18n KEYS in this
    // harness, so a substring regex is what matches a single line.
    expect(await screen.findByText(/admin:servers\.confirm\.upgrade_target_unpinnable/)).toBeTruthy()
  })

  it('does not add the warning when the API says the target is pinnable', async () => {
    installReads({
      '/admin/servers': list([xui]),
      '/admin/servers/9/upgrade-preview': {
        update_available: true,
        current_version: '3.4.2',
        target_version: 'v3.7.0',
        compat_status: 'supported',
        target_pinnable: true,
      },
    })
    mount(<><ConfirmHost /><ServersView /></>)

    const row = await rowFor(xui.name)
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' }))

    // The target line is present, so the dialog did open; the caveat is absent
    // because nothing said the target was unpinnable.
    // The dialog body is one joined string whose lines are i18n KEYS in this
    // harness, so a substring regex is what matches a single line.
    expect(await screen.findByText(/admin:servers\.confirm\.upgrade_target(?![_])/)).toBeTruthy()
    expect(screen.queryByText(/admin:servers\.confirm\.upgrade_target_unpinnable/)).toBeNull()
  })
})

// The instance decides whether it may upgrade a component; the dialog asks.
// Showing a confirm and letting the request fail would teach the operator that
// the button is unreliable, and firing on a state the server already called
// unavailable makes the client the first place the decision was made.
describe('the upgrade action asks the instance before it fires', () => {
  const optionRead = (state: string, reason_codes = ['capability_missing']) => ({
    '/admin/servers/9/upgrade-options': { component: 'panel', state, target_pinnable: false, reason_codes },
  })

  it.each(['unsupported', 'blocked'])('does not fire when the instance answers %s', async state => {
    installReads({ '/admin/servers': list([xui]), ...optionRead(state) })
    mount(<><ConfirmHost /><ServersView /></>)

    const row = await rowFor(xui.name)
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' }))

    // The harness mocks pushSnack, so the message is read from the spy rather
    // than the DOM — and it is the i18n KEY, because t() resolves keys to
    // themselves here.
    await waitFor(() => expect(snack.mock.calls.some(([message]) => /upgrade_unavailable/.test(message))).toBe(true))
    // No confirm and no request: the refusal came from the instance, so there is
    // nothing for the operator to agree to.
    expect(screen.queryByText('admin:servers.confirm.upgrade_panel_title')).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('still asks for confirmation when the instance says the upgrade is manual only', async () => {
    // manual_only is not a refusal — the upgrade is possible, it just cannot be
    // held to a version — so the operator still gets the dialog.
    installReads({
      '/admin/servers': list([xui]),
      ...optionRead('manual_only', ['target_not_pinnable']),
      '/admin/servers/9/upgrade-preview': {
        update_available: true, current_version: '3.4.2', target_version: 'v3.7.0',
        compat_status: 'supported', target_pinnable: false,
      },
    })
    mount(<><ConfirmHost /><ServersView /></>)

    const row = await rowFor(xui.name)
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' }))

    expect(await screen.findByText('admin:servers.confirm.upgrade_panel_title')).toBeTruthy()
    expect(snack.mock.calls.some(([message]) => /upgrade_unavailable/.test(message))).toBe(false)
  })

  it('falls through to the existing flow when the instance cannot answer', async () => {
    // A failed read is NOT a refusal: the write path still protects the fire, so
    // a control-plane blip must not remove an action the operator was using.
    installReads({
      '/admin/servers': list([xui]),
      '/admin/servers/9/upgrade-preview': { update_available: true, current_version: '3.4.2', target_version: 'v3.7.0' },
    })
    mount(<><ConfirmHost /><ServersView /></>)

    const row = await rowFor(xui.name)
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' }))

    expect(await screen.findByText('admin:servers.confirm.upgrade_panel_title')).toBeTruthy()
  })
})

// The panel can report why an upgrade is not offered. That answer is only useful
// if it is visible: without it the diagnosis an operator has is to guess, and the
// guess is usually "the panel is broken" rather than "the range is the last good
// one".
describe('the fleet-level compatibility state', () => {
  const working = { xui: { min_version: '3.4.2', max_tested: '3.8.5' }, sui: { max_tested: '1.6.3' } }

  it('warns when the range is the last good one', async () => {
    installReads({
      '/admin/servers': list([xui]),
      '/admin/servers/compat-status': { ...working,
        xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-19T00:00:00Z', last_error: 'github unreachable' } },
    })
    mount(<ServersView />)
    expect(await screen.findByText(/compat_notice\.range-stale/)).toBeTruthy()
  })

  it('says nothing when the state is ordinary', async () => {
    // A banner for every non-ideal state is how banners stop being read.
    installReads({ '/admin/servers': list([xui]), '/admin/servers/compat-status': working })
    mount(<ServersView />)
    await screen.findByText('admin:servers.title')
    expect(screen.queryByText(/compat_notice\./)).toBeNull()
  })

  it('says nothing when the panel cannot answer', async () => {
    installReads({ '/admin/servers': list([xui]) })
    mount(<ServersView />)
    await screen.findByText('admin:servers.title')
    expect(screen.queryByText(/compat_notice\./)).toBeNull()
  })
})

// The manual-maintenance copy existed in both locales and was rendered nowhere —
// written and never wired, which looks identical to never having written it. Its
// absence matters because "there is a newer release" and "there is something for
// you to do" are different sentences.
describe('the S-UI manual-upgrade hint', () => {
  it('tells an S-UI operator the release is theirs to install', async () => {
    installReads({ '/admin/servers': list([sui]) })
    mount(<ServersView />)
    const row = await rowFor(sui.name)
    expect(await within(row).findByText('admin:servers.sui_update.manual_hint')).toBeTruthy()
  })

  it('does not say it when there is no update to install', async () => {
    // The hint is a step, not a permanent caveat about the backend.
    installReads({ '/admin/servers': list([{ ...sui, update_available: false, latest_sui_version: undefined }]) })
    mount(<ServersView />)
    const row = await rowFor(sui.name)
    await within(row).findByText('S-UI 1.5.0')
    expect(within(row).queryByText('admin:servers.sui_update.manual_hint')).toBeNull()
  })

  it('does not attach it to a 3X-UI row', async () => {
    // 3X-UI has a managed path from the row menu; telling its operator to
    // maintain it by hand would be the opposite of true.
    installReads({ '/admin/servers': list([xui]) })
    mount(<ServersView />)
    const row = await rowFor(xui.name)
    expect(await within(row).findByText(/admin:servers.update_available/)).toBeTruthy()
    expect(within(row).queryByText('admin:servers.sui_update.manual_hint')).toBeNull()
  })
})
