// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, lastParams, list, mountPage, toast } from './logs/testHarness'
import { useAuthStore } from '@/stores/auth'
import LogsView from './LogsView'

const mount = (entry = '/admin/logs') => mountPage(<LogsView />, entry, '/admin/logs')
const here = (m: ReturnType<typeof mount>) => m.router.state.location.pathname + m.router.state.location.search

const subLogs = [
  { id: 1, user_id: 7, user_upn: 'alice', ip: '203.0.113.7', ua: 'ClashMeta/1.0', client_type: 'mihomo', accessed_at: '2026-09-20T00:00:00Z', device_label: 'iOS 17.5 · iPhone15,2', device_id4: 'ab12' },
  { id: 2, user_id: 7, user_upn: 'alice', ip: '203.0.113.8', ua: 'v2rayN/7', client_type: 'uri-list', accessed_at: '2026-09-20T00:01:00Z', device_id4: 'cd34' },
  { id: 3, user_id: 7, user_upn: 'alice', ip: '203.0.113.9', ua: 'curl/8', client_type: 'uri-list', accessed_at: '2026-09-20T00:02:00Z' },
]
const empty = list([])
const reads = (over: Record<string, unknown> = {}) => installReads({
  '/admin/sub-logs': list(subLogs), '/admin/audit': empty, '/admin/auth-events': empty, '/admin/email-logs': empty,
  '/admin/cert-events': { events: [], total: 0 }, '/admin/settings/ui': { sub_log_retention_days: 30, mail_sent_retention_days: 14 }, ...over,
})

describe('LogsView', () => {
  it('reports a failed read instead of rendering an empty log', async () => {
    // "No log entries" is a claim about the audit trail: a failure must never read as a clean history.
    installReads({ '/admin/sub-logs': new Error('offline') })
    mount()
    expect(await screen.findByText('Log data is temporarily unavailable')).toBeTruthy()
    expect(screen.queryByText('No log entries yet')).toBeNull()
  })

  it('reports a failed certificate read instead of an empty certificate log, and shows an empty one as empty', async () => {
    installReads({ '/admin/cert-events': new Error('offline') })
    const m = mount('/admin/logs?tab=certs')
    expect(await screen.findByText('Log data is temporarily unavailable')).toBeTruthy()
    m.unmount()
    reads()
    mount('/admin/logs?tab=certs')
    expect(await screen.findByText('No log entries yet')).toBeTruthy()
  })

  it('says how many records match, and asks the server for the page and keyword in the address', async () => {
    reads({ '/admin/sub-logs': list(subLogs, 120) })
    mount('/admin/logs?page=2&q=alice')
    expect(await screen.findByText('120 results')).toBeTruthy()
    expect(lastParams('/admin/sub-logs')).toMatchObject({ page: 2, page_size: 25, search: 'alice' })
  })

  it('shows the declared device to admins only', async () => {
    reads()
    mount()
    await screen.findByText('iOS 17.5 · iPhone15,2')
    expect(screen.queryByRole('columnheader', { name: 'Device' })).not.toBeNull()
    // Only the 4-character prefix of the digest reaches the page; a fetch that declared nothing says so.
    expect(screen.getByText('#cd34')).toBeTruthy()
    const row3 = screen.getByText('curl/8').closest('tr') as HTMLElement
    expect(within(row3).getByText('—')).toBeTruthy()
  })

  it('hides the device column from an operator, whose API response carries none', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    mount()
    await screen.findByText('curl/8')
    expect(screen.queryByRole('columnheader', { name: 'Device' })).toBeNull()
    expect(screen.queryByText('iOS 17.5 · iPhone15,2')).toBeNull()
  })

  it('opens the record in a sheet whose address is a link, and closes it with Back-able state', async () => {
    reads({ '/admin/audit': list([{ id: 9, actor: 'root', action: 'PUT /api/admin/nodes/3', target: 'node 3', ip: '198.51.100.2', at: '2026-09-20T00:00:00Z', before_json: '{"a":1}', after_json: '{"a":2}' }]) })
    const m = mount('/admin/logs?tab=audit')
    fireEvent.click(await screen.findByText('node 3'))
    const sheet = await screen.findByRole('dialog')
    expect(here(m)).toBe('/admin/logs?tab=audit&row=9')
    expect(within(sheet).getByText('PUT /api/admin/nodes/3')).toBeTruthy()
    // JSON is pretty-printed in a block that can be copied whole.
    expect(within(sheet).getByText(/"a": 2/)).toBeTruthy()
    expect(within(sheet).getAllByRole('button', { name: /^Copy/ }).length).toBe(2)
    fireEvent.click(within(sheet).getByRole('button', { name: 'Close details' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(here(m)).toBe('/admin/logs?tab=audit')
  })

  it('a tab switch leaves the page, keyword and record behind', async () => {
    reads()
    const m = mount('/admin/logs?page=3&q=alice&row=2')
    await screen.findAllByText('alice')
    // The sheet is modal, so the rest of the page is hidden from the tree while it is open.
    fireEvent.click(screen.getByRole('tab', { name: 'Audit logs', hidden: true }))
    await waitFor(() => expect(here(m)).toBe('/admin/logs?tab=audit'))
  })
})

// The risk center's drawer links here with the account's id (exact) and its UPN (display only): a
// keyword would be a fuzzy search over user, IP, UA and client and match other accounts' names too.
describe('LogsView exact user filter', () => {
  it('filters the subscription log by user_id behind a removable chip', async () => {
    reads()
    const m = mount('/admin/logs?tab=sub&user_id=7&upn=alice')
    expect(await screen.findByText('User: alice')).toBeTruthy()
    await waitFor(() => expect(lastParams('/admin/sub-logs')).toMatchObject({ user_id: 7 }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove the user filter' }))
    await waitFor(() => expect(here(m)).toBe('/admin/logs?tab=sub'))
    expect(screen.queryByText('User: alice')).toBeNull()
    await waitFor(() => expect(lastParams('/admin/sub-logs')?.user_id).toBeUndefined())
  })

  it('filters the sign-in log the same way, and its facets are in the address', async () => {
    reads()
    mount('/admin/logs?tab=auth&user_id=7&upn=alice&outcome=failure')
    expect(await screen.findByText('User: alice')).toBeTruthy()
    await waitFor(() => expect(lastParams('/admin/auth-events')).toMatchObject({ user_id: 7, outcome: 'failure' }))
  })

  it('ignores a malformed id', async () => {
    reads()
    mount('/admin/logs?tab=sub&user_id=7x&upn=alice')
    await waitFor(() => expect(lastParams('/admin/sub-logs')).toBeDefined())
    expect(lastParams('/admin/sub-logs')?.user_id).toBeUndefined()
    expect(screen.queryByText('User: alice')).toBeNull()
  })
})

// Purge and clear delete records for good: admin only, each names what it deletes, focus starts on
// Cancel, and a second pick while the request is in flight sends nothing more.
describe('LogsView clean-up', () => {
  const openMenu = () => {
    const trigger = screen.getByRole('button', { name: 'Clean-up options' })
    fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(trigger, { button: 0 })
    fireEvent.click(trigger)
  }
  const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

  it('purges after a confirmation that names the log, once', async () => {
    reads()
    mount()
    await screen.findByText('curl/8')
    const gate = deferred<{ data: { deleted: number } }>()
    api.post.mockImplementation(async () => gate.promise)
    openMenu()
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Purge expired' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Purge expired subscription logs')).toBeTruthy()
    // Focus starts on Cancel, so a stray Enter cannot confirm a deletion.
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Purge expired' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/sub-logs/purge'))
    // A second attempt while the first is in flight is ignored.
    openMenu()
    expect((await screen.findByRole('menuitem', { name: 'Purge expired' })).getAttribute('aria-disabled')).toBe('true')
    expect(api.post).toHaveBeenCalledTimes(1)
    gate.resolve({ data: { deleted: 4 } })
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Purged 4 expired logs', 'success'))
  })

  it('clears the audit log only after the confirmation', async () => {
    reads()
    mount('/admin/logs?tab=audit')
    await screen.findByText('No log entries yet')
    openMenu()
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Clear all' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Clear all' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/audit'))
  })

  it('offers neither to an operator', async () => {
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    reads()
    mount()
    await screen.findByText('curl/8')
    expect(screen.queryByRole('button', { name: 'Clean-up options' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Retention' })).toBeNull()
  })

  it('saves the retention by writing the whole settings object back with the one field changed', async () => {
    reads({ '/admin/settings/ui': { sub_log_retention_days: 30, other: 'kept' } })
    api.put.mockImplementation(async (_url: string, body: unknown) => ({ data: body }))
    mount()
    await screen.findByText('curl/8')
    fireEvent.click(screen.getByRole('button', { name: 'Retention' }))
    const input = await screen.findByRole('textbox')
    await waitFor(() => expect((input as HTMLInputElement).value).toBe('30'))
    fireEvent.change(input, { target: { value: '90' } })
    fireEvent.blur(input)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', { sub_log_retention_days: 90, other: 'kept' }))
  })
})
