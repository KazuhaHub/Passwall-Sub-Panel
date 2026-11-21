// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, mountAdmin, toast } from './adminTestHarness'
import LanguagePacksView from './LanguagePacksView'

vi.mock('@/i18n', () => ({
  isBuiltinLanguage: (c: string) => ['zh-CN', 'zh-TW', 'en-US'].includes(c),
  LANGUAGE_PACK_FORMAT: 1,
  loadBuiltinSource: vi.fn(async () => ({ common: { hello: 'Hello' } })),
}))

const pack = { code: 'fr-FR', name: 'Français', author: 'someone', base_version: '' }
const valid = JSON.stringify({ psp_language_pack: 1, code: 'de-DE', name: 'Deutsch', namespaces: { common: { hello: 'Hallo' } } })

function serve(over: Record<string, unknown> = {}) {
  installReads({ '/admin/locales': [pack], '/version': { version: 'dev', commit: '', build_date: '' }, ...over })
}
// Waits for the uploaded pack first: the table redraws when the list and the panel version arrive.
const row = async (name: string) => { await screen.findByText('Français'); return (await screen.findByText(name)).closest('tr')! }

describe('Language packs', () => {
  it('lists the built-in languages with the uploaded ones, and says why a built-in cannot be deleted', async () => {
    serve()
    mountAdmin(<LanguagePacksView />)
    const english = await row('English')
    expect(within(english).getByText('Built in')).toBeTruthy()
    const del = within(english).getByRole('button', { name: 'Delete' })
    // Disabled WITH its reason: focusable, aria-disabled, and the click does nothing.
    expect(del.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(del)
    expect(api.delete).not.toHaveBeenCalled()
    const fr = await row('Français')
    expect(within(fr).getByText('Uploaded')).toBeTruthy()
  })

  it('reports a failed read instead of showing a list that looks empty', async () => {
    serve({ '/admin/locales': new Error('offline') })
    mountAdmin(<LanguagePacksView />)
    expect(await screen.findByText('Language packs are temporarily unavailable')).toBeTruthy()
  })

  it('names the pack in the delete confirmation, focuses Cancel, and deletes only after yes', async () => {
    serve()
    api.delete.mockResolvedValue({ data: {} })
    mountAdmin(<LanguagePacksView />)
    fireEvent.click(within(await row('Français')).getByRole('button', { name: 'More' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Delete language pack Français?')).toBeTruthy()
    expect(within(dialog).getByText(/Français \(fr-FR\)/)).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/locales/fr-FR'))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Language pack deleted', 'success'))
  })

  it('does not delete when the confirmation is declined', async () => {
    serve()
    mountAdmin(<LanguagePacksView />)
    fireEvent.click(within(await row('Français')).getByRole('button', { name: 'More' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('hides upload, export and delete from a role without config.write', async () => {
    serve()
    const { useAuthStore } = await import('@/stores/auth')
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mountAdmin(<LanguagePacksView />)
    await row('Français')
    expect(screen.queryByRole('button', { name: 'Upload' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Export base template' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'More' })).toBeNull()
  })
})

describe('Language pack upload', () => {
  async function openUpload() {
    serve()
    mountAdmin(<LanguagePacksView />)
    await row('Français')
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }))
    return screen.findByRole('dialog')
  }

  it('sends the parsed pack, says so, and reminds that a reload is needed', async () => {
    const dialog = await openUpload()
    api.put.mockResolvedValue({ data: {} })
    fireEvent.change(within(dialog).getByLabelText('Language pack JSON'), { target: { value: valid } })
    expect(within(dialog).getByTestId('pack-summary').textContent).toContain('Deutsch (de-DE)')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/locales/de-DE', JSON.parse(valid), { _skipErrorToast: true }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Language pack saved', 'success'))
    expect(toast).toHaveBeenCalledWith('Reload the page to see this language in the language menu.', 'info')
  })

  it('lists every problem at once and keeps Upload off until they are fixed', async () => {
    const dialog = await openUpload()
    const bad = JSON.stringify({ psp_language_pack: 2, code: 'en-US', namespaces: { common: { a: 1 }, extra: {} } })
    fireEvent.change(within(dialog).getByLabelText('Language pack JSON'), { target: { value: bad } })
    const text = within(dialog).getByRole('alert').textContent ?? ''
    expect(text).toContain('must be 1 (found 2)')
    expect(text).toContain('en-US is a built-in language')
    expect(text).toContain('"name" is required')
    expect(text).toContain('Unknown namespace "extra"')
    expect(text).toContain('common:a must be a string (found number)')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('accepts a null namespace, as the server does', async () => {
    const dialog = await openUpload()
    fireEvent.change(within(dialog).getByLabelText('Language pack JSON'), { target: { value: JSON.stringify({ psp_language_pack: 1, code: 'de-DE', name: 'Deutsch', namespaces: { nav: null, common: { a: 'b' } } }) } })
    expect(within(dialog).queryByRole('alert')).toBeNull()
    expect(within(dialog).getByTestId('pack-summary')).toBeTruthy()
  })

  it('says what is wrong with text that is not JSON', async () => {
    const dialog = await openUpload()
    fireEvent.change(within(dialog).getByLabelText('Language pack JSON'), { target: { value: '{nope' } })
    expect(within(dialog).getByRole('alert').textContent).toContain('not valid JSON')
  })

  it('shows what the server still refuses, in its words, and keeps the text', async () => {
    const dialog = await openUpload()
    api.put.mockRejectedValue(Object.assign(new Error('x'), { response: { status: 400, data: { error: 'validation: pack is odd' } } }))
    const box = within(dialog).getByLabelText('Language pack JSON') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: valid } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }))
    expect(await within(dialog).findByText('validation: pack is odd')).toBeTruthy()
    expect(box.value).toBe(valid)
  })

  it('reads a chosen file into the text, from a real button', async () => {
    const dialog = await openUpload()
    expect(within(dialog).getByRole('button', { name: 'Choose file' })).toBeTruthy()
    const file = new File([valid], 'de.json', { type: 'application/json' })
    fireEvent.change(within(dialog).getByTestId('pack-file'), { target: { files: [file] } })
    await waitFor(() => expect((within(dialog).getByLabelText('Language pack JSON') as HTMLTextAreaElement).value).toBe(valid))
  })
})
