// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Field } from './Field'
import { NumberField } from './NumberField'
import { SecretField } from './SecretField'
import { TagsField } from './TagsField'

afterEach(cleanup)

// The line under a control holds its help and error text (plan 3.3). A tooltip that opens
// below an icon button inside the control would cover exactly that line, so these open above.
async function sideOf(name: string | RegExp) {
  fireEvent.focus(screen.getByRole('button', { name }))
  await vi.waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')).not.toBeNull())
  const content = document.querySelector('[data-slot="tooltip-content"]') as HTMLElement
  return (content.closest('[data-side]') ?? content).getAttribute('data-side')
}

describe('icon buttons inside a field open their tooltip above, not over the text below', () => {
  it('the secret field show / hide button', async () => {
    render(<Field label="Token"><SecretField value="" onChange={() => {}} /></Field>)
    expect(await sideOf('Show')).toBe('top')
  })

  it('the secret field copy button', async () => {
    render(<Field label="Token"><SecretField value="abc" onChange={() => {}} /></Field>)
    expect(await sideOf('Copy')).toBe('top')
  })

  it('the number field "follow again" button', async () => {
    render(<Field label="Limit"><NumberField value={5} onChange={() => {}} allowEmpty /></Field>)
    expect(await sideOf('Follow again')).toBe('top')
  })

  it('a tag chip remove button', async () => {
    render(<Field label="Tags">{p => <TagsField id={p.id} aria-labelledby={p.labelId} value={['HK']} onChange={() => {}} />}</Field>)
    expect(await sideOf('Remove HK')).toBe('top')
  })
})
