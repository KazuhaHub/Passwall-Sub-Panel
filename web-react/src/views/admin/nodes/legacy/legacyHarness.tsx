import { fireEvent, screen, waitFor } from '@testing-library/react'
import { expect } from 'vitest'
import { useAllNodes } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { LegacyEditorsProvider, useLegacyEditors } from './LegacyEditors'

// A stand-in for the list in the tests of the legacy editors: the editors are opened by the page now, so the
// tests open them through the same host API with four buttons. Not part of the app.
function Buttons() {
  const legacy = useLegacyEditors()
  const nodes = useAllNodes(useQueryScope())
  const ready = !!nodes.data
  return (
    <>
      <button disabled={!ready} onClick={() => legacy.openEdit(1)}>open-meta</button>
      <button disabled={!ready} onClick={() => legacy.openInbound(1)}>open-inbound</button>
      <button disabled={!ready} onClick={() => legacy.openEdit(77)}>open-listener</button>
      <button disabled={!ready} onClick={() => legacy.openCreate()}>open-create</button>
    </>
  )
}

export function LegacyProbe() {
  return <LegacyEditorsProvider><Buttons /></LegacyEditorsProvider>
}

async function open(name: string) {
  const button = await screen.findByRole('button', { name })
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(button)
  return screen.findByRole('dialog')
}
export const openMeta = () => open('open-meta')
export const openListener = () => open('open-listener')
export const openInboundEditor = () => open('open-inbound')
export const openCreateWizard = () => open('open-create')
