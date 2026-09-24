// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import NodeDiagnosticsDialog from './NodeDiagnosticsDialog'
import type { Server } from '@/api/servers'


/**
 * SLOW-NETWORK FEEDBACK for the Collect button: the initial
 * requestNodeDiagnostic POST can itself take a few seconds, and until now the
 * button only went disabled for that wait with no spinner — indistinguishable
 * from a button that silently failed to respond to the click.
 */

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const server: Server = {
  id: 7, name: 'node-1', panel_type: 'psp', capabilities: [], url: 'psp://agt_1',
  has_api_token: false, has_password: false, auth_method: '', insecure_https: false,
}

describe('NodeDiagnosticsDialog Collect button', () => {
  it('shows a spinner for the whole request and ignores a second click', async () => {
    installReads({})
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation(() => gate.promise)
    mount(<NodeDiagnosticsDialog server={server} open onClose={() => {}} />)

    const collectButton = screen.getByText('admin:nodeDiagnostics.collect').closest('button')!
    fireEvent.click(collectButton)

    await waitFor(() => expect(collectButton.disabled).toBe(true))
    expect(screen.getByRole('progressbar')).toBeTruthy()

    fireEvent.click(collectButton)
    expect(api.post).toHaveBeenCalledTimes(1)

    gate.resolve({
      data: {
        task_id: 't1', agent_id: 'agt_1', sections: ['host', 'runtime', 'state'], max_events: 0,
        status: 'succeeded', not_after_ms: 1_789_000_000_000, dispatch_closed: true,
        result: { schema_version: 1, collected_at_ms: 1, recovered: false, truncated: false, checks: [] },
      },
    })
    await waitFor(() => expect(collectButton.disabled).toBe(false))
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
})
