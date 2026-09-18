// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import NodeDiagnosticsDialog from './NodeDiagnosticsDialog'
import type { Server } from '@/api/servers'

/**
 * The lifecycle and the absences.
 *
 * THE LIFECYCLE IS THE POINT OF THIS COMPONENT. A diagnostic is asynchronous, so
 * queued and offered mean nobody has looked yet — and a component that drew an
 * empty result table for them would be reporting a clean machine on the strength
 * of no evidence.
 *
 * The translation hook is mocked to return the KEY, so these assert which key
 * the component asks for rather than which sentence it renders.
 */

const server: Server = {
  id: 7, name: 'node-1', panel_type: 'psp', capabilities: [], url: 'psp://agt_1',
  has_api_token: false, has_password: false, auth_method: '', insecure_https: false,
}

const checks = [
  { code: 'collector.host', status: 'ok', summary: 'collected' },
  { code: 'credential.permissions', status: 'unavailable', summary: 'no credential given' },
]

function diagnostic(overrides: Record<string, unknown> = {}) {
  return {
    task_id: 'task-1', agent_id: 'agt_1', sections: ['host', 'state'], max_events: 0,
    status: 'succeeded', not_after_ms: 1_789_000_000_000, dispatch_closed: false,
    result: {
      schema_version: 1, collected_at_ms: 1_789_000_000_000, recovered: false, truncated: false,
      checks, state: { sqlite_quick_check: 'ok', outbox_pending: 2, tasks_queued: 0 },
    },
    ...overrides,
  }
}

async function openAndCollect(body: unknown) {
  api.post.mockResolvedValue({ data: body })
  installReads({})
  mount(<NodeDiagnosticsDialog server={server} open onClose={() => {}} />)
  fireEvent.click(screen.getByText('admin:nodeDiagnostics.collect'))
}

describe('NodeDiagnosticsDialog', () => {
  it('shows the lifecycle and no result while the node has not answered', async () => {
    await openAndCollect(diagnostic({ status: 'offered', result: undefined }))
    await waitFor(() => expect(screen.getByText('admin:nodeDiagnostics.status.offered')).toBeTruthy())
    expect(screen.getByText('admin:nodeDiagnostics.waiting')).toBeTruthy()
    // Nobody has looked yet, so there is nothing to report and nothing drawn.
    expect(screen.queryByText('admin:nodeDiagnostics.checks')).toBeNull()
    expect(screen.queryByText('collector.host')).toBeNull()
  })

  it('renders the check verdicts once the node has answered', async () => {
    await openAndCollect(diagnostic())
    await waitFor(() => expect(screen.getByText('collector.host')).toBeTruthy())
    expect(screen.getByText('admin:nodeDiagnostics.checks')).toBeTruthy()
    // The verdict is translated, not printed as the raw wire value.
    expect(screen.getByText('admin:nodeDiagnostics.checkStatus.unavailable')).toBeTruthy()
    expect(screen.getByText('no credential given')).toBeTruthy()
  })

  it('reports a failure instead of drawing an empty result', async () => {
    await openAndCollect(diagnostic({ status: 'failed', result: undefined }))
    await waitFor(() => expect(screen.getByText('admin:nodeDiagnostics.failed')).toBeTruthy())
    expect(screen.queryByText('admin:nodeDiagnostics.checks')).toBeNull()
  })

  // AN UNKNOWABLE OUTCOME IS NOT A FAILURE, and it is not a success either: the
  // work may or may not have happened, and the UI has to say that rather than
  // pick one.
  it('separates an indeterminate outcome from a failure', async () => {
    await openAndCollect(diagnostic({ status: 'indeterminate', result: undefined }))
    await waitFor(() => expect(screen.getByText('admin:nodeDiagnostics.indeterminate')).toBeTruthy())
    expect(screen.queryByText('admin:nodeDiagnostics.failed')).toBeNull()
  })

  // A SECTION NOBODY ASKED FOR IS ABSENT, not empty. Drawing it anyway would
  // claim the panel looked.
  it('does not render a section the node did not collect', async () => {
    await openAndCollect(diagnostic({
      result: {
        schema_version: 1, collected_at_ms: 1, recovered: false, truncated: false, checks,
      },
    }))
    await waitFor(() => expect(screen.getByText('collector.host')).toBeTruthy())
    expect(screen.queryByText(/admin:nodeDiagnostics\.state/)).toBeNull()
    expect(screen.queryByText(/admin:nodeDiagnostics\.runtime/)).toBeNull()
  })

  it('says so when the node truncated the events', async () => {
    await openAndCollect(diagnostic({
      // The STATUS field, which the panel copies from the result: both carry it
      // on the wire, and the chip follows the status the panel reports.
      truncated: true,
      result: {
        schema_version: 1, collected_at_ms: 1, recovered: false, truncated: true, checks,
        events: [{
          code: 'sync.failed', at_ms: 1_789_000_000_000, severity: 'warning', summary: 'a sync round failed',
        }],
      },
    }))
    await waitFor(() => expect(screen.getByText('admin:nodeDiagnostics.truncated')).toBeTruthy())
    expect(screen.getByText('a sync round failed')).toBeTruthy()
  })
})
