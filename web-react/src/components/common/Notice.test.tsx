// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { Notice } from './Notice'

afterEach(cleanup)

it('announces a failure as an alert and everything else as a status, unless told otherwise', () => {
  render(<><Notice tone="danger">bad</Notice><Notice tone="success">fine</Notice><Notice tone="info" role="alert">told</Notice></>)
  expect(screen.getByText('bad').closest('[data-slot="notice"]')!.getAttribute('role')).toBe('alert')
  expect(screen.getByText('fine').closest('[data-slot="notice"]')!.getAttribute('role')).toBe('status')
  expect(screen.getByText('told').closest('[data-slot="notice"]')!.getAttribute('role')).toBe('alert')
})

it('carries an optional action and a test id', () => {
  render(<Notice tone="warning" testId="n" action={<button>Retry</button>}>wait</Notice>)
  expect(screen.getByTestId('n')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
})
