// @vitest-environment jsdom
import { screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, mount } from '@/test/adminSaveHarness'
import { SeparatorDialog } from './SeparatorDialog'

const separator = { id: 50, display_name: '---- HK ----', sort_order: 5, enabled: true, mode: 'node_bound' as const, node_ids: [1] }

it('previews which groups would show a node-bound separator', async () => {
  api.post.mockImplementation(async (url: string) => String(url).endsWith('/separator/group-preview')
    ? { data: { groups: [{ id: 1, name: 'VIP' }], visible: [] } }
    : { data: {} })
  mount(<SeparatorDialog separatorId={50} separators={[separator]} nodes={[{ id: 1, name: 'old-name' }]} onClose={vi.fn()} onSaved={vi.fn()} />)
  await waitFor(() => expect(screen.getByTestId('separator-groups').textContent).toBe('admin:nodes.separator_preview.none'))
  const call = api.post.mock.calls.filter(([url]) => String(url).endsWith('/separator/group-preview')).at(-1)!
  expect(call[1]).toEqual({ mode: 'node_bound', node_ids: [1] })
  expect(api.put).not.toHaveBeenCalled()
})
