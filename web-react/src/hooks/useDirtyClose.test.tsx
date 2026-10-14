/** @vitest-environment jsdom */
import { useState } from 'react'
import { Dialog, ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import { createAppTheme } from '@/theme'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const confirm = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm }))
import { useDirtyClose } from './useDirtyClose'
beforeEach(() => confirm.mockReset())
afterEach(cleanup)

it.each(['close button', 'escape', 'backdrop'])('requires confirmation for %s and preserves edits on cancel', async origin => {
  confirm.mockResolvedValueOnce(false)
  function Harness() {
    const [open, setOpen] = useState(true)
    const canClose = useDirtyClose(true, { title: 'Unsaved', message: 'Discard changes?' })
    const close = () => { void canClose().then(ok => { if (ok) setOpen(false) }) }
    return <Dialog open={open} onClose={close}><button onClick={close}>Close editor</button></Dialog>
  }
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><Harness /></ThemeProvider>)
  // Each case must travel through the actual dialog's distinct event path.
  if (origin.includes('close button')) fireEvent.click(screen.getByText('Close editor'))
  else if (origin.includes('escape')) fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape', keyCode: 27 })
  else {
    const backdrop = screen.getByRole('dialog').parentElement!
    fireEvent.mouseDown(backdrop)
    fireEvent.click(backdrop)
  }
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
  expect(screen.getByRole('dialog')).toBeTruthy()
})

it('does not prompt for a clean dialog and reads the latest copy after edits', async () => {
  confirm.mockClear()
  const { result, rerender } = renderHook(({ dirty, message }) => useDirtyClose(dirty, { title: 'Unsaved', message }), { initialProps: { dirty: false, message: 'old' } })
  await expect(result.current()).resolves.toBe(true)
  expect(confirm).not.toHaveBeenCalled()
  rerender({ dirty: true, message: 'new' })
  confirm.mockResolvedValueOnce(true)
  await expect(result.current()).resolves.toBe(true)
  expect(confirm).toHaveBeenCalledWith({ title: 'Unsaved', message: 'new' })
})

it('shares one confirmation across repeated close attempts', async () => {
  confirm.mockClear()
  let answer!: (ok: boolean) => void
  confirm.mockReturnValueOnce(new Promise<boolean>(resolve => { answer = resolve }))
  const { result } = renderHook(() => useDirtyClose(true, { title: 'Unsaved', message: 'Discard?' }))
  const one = result.current()
  const two = result.current()
  expect(confirm).toHaveBeenCalledTimes(1)
  answer(false)
  await expect(one).resolves.toBe(false)
  await expect(two).resolves.toBe(false)
})
