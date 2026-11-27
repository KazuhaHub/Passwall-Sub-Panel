// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, expect, it } from 'vitest'
import NotFoundView from './NotFoundView'

afterEach(cleanup)

it('says the page does not exist and links back to the start page', () => {
  render(<MemoryRouter><NotFoundView home="/admin/dashboard" /></MemoryRouter>)
  expect(screen.getByText('Page not found')).toBeTruthy()
  expect(screen.getByText('Back to start page').closest('a')!.getAttribute('href')).toBe('/admin/dashboard')
})
