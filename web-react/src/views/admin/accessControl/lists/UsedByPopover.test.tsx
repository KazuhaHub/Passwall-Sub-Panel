// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, o?: { count?: number }) => `${key}${o?.count !== undefined ? `:${o.count}` : ''}` }),
}))

import UsedByPopover from './UsedByPopover'

afterEach(cleanup)

describe('UsedByPopover', () => {
  it('counts a node route and names its node, without a link to open', () => {
    const onOpenPolicy = vi.fn()
    render(<UsedByPopover onOpenPolicy={onOpenPolicy} onOpenAllowlist={() => {}} usedBy={[
      { kind: 'policy', id: 5, name: 'block ads' },
      { kind: 'route', id: 9, name: 'hk-entry' },
    ]} />)
    const trigger = screen.getByRole('button')
    expect(trigger.textContent).toBe('admin:access_control.lists.usage.policies:1 · admin:access_control.lists.usage.routes:1')
    fireEvent.click(trigger)
    const route = screen.getByTestId('used-by-route')
    expect(route.textContent).toBe('hk-entry')
    expect(route.querySelector('button')).toBeNull()
    fireEvent.click(screen.getByText('block ads'))
    expect(onOpenPolicy).toHaveBeenCalledWith(5)
  })

  it('shows only routes when nothing else uses the list', () => {
    render(<UsedByPopover onOpenPolicy={() => {}} onOpenAllowlist={() => {}} usedBy={[{ kind: 'route', id: 9, name: '' }]} />)
    expect(screen.getByRole('button').textContent).toBe('admin:access_control.lists.usage.routes:1')
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByTestId('used-by-route').textContent).toBe('9')
  })
})
