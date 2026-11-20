// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import type { Server } from '@/api/servers'
import '../testHarness'
import { ServerSelect } from './ServerSelect'

afterEach(cleanup)
const s = (id: number, name: string, region = ''): Server => ({ id, name, region, panel_type: 'psp', capabilities: [] } as unknown as Server)

it('filters by typing, on the name or the region, and reports the chosen id', async () => {
  const onChange = vi.fn()
  render(<ServerSelect servers={[s(1, 'Osaka', 'JP'), s(2, 'Tokyo', 'JP'), s(3, 'London', 'UK')]} value={0} onChange={onChange} placeholder="pick" />)
  const input = screen.getByPlaceholderText('pick')
  fireEvent.mouseDown(input)
  fireEvent.change(input, { target: { value: 'uk' } })
  expect((await screen.findAllByRole('option')).map(o => o.textContent)).toEqual(['London'])
  fireEvent.click(screen.getByRole('option', { name: 'London' }))
  expect(onChange).toHaveBeenCalledWith(3)
})
