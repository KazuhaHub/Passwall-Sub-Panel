// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { Disclosure } from './Disclosure'

afterEach(cleanup)

it('keeps its own state when uncontrolled, and does not mount the body while closed', () => {
  render(<Disclosure summary="More">secret</Disclosure>)
  const button = screen.getByRole('button', { name: 'More' })
  expect(button.getAttribute('aria-expanded')).toBe('false')
  expect(screen.queryByText('secret')).toBeNull()
  fireEvent.click(button)
  expect(button.getAttribute('aria-expanded')).toBe('true')
  expect(screen.getByText('secret')).toBeTruthy()
})

it('follows `open` and only reports the toggle when controlled', () => {
  const onToggle = vi.fn()
  render(<Disclosure open={false} onToggle={onToggle} summary="Raw">data</Disclosure>)
  fireEvent.click(screen.getByRole('button', { name: 'Raw' }))
  expect(onToggle).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('data')).toBeNull()
})

it('starts open with defaultOpen', () => {
  render(<Disclosure defaultOpen summary="Open">body</Disclosure>)
  expect(screen.getByText('body')).toBeTruthy()
})
