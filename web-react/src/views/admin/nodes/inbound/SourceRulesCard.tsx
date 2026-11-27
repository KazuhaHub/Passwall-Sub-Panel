import { useEffect, useRef, useState } from 'react'
import SourceRulesSection, { type SourceDraftHandle } from '../../accessControl/SourceRulesSection'

// The shared section reports its unsaved state only through a handle that is read on demand, not as an event, and
// it is not this package's to change. Reading the handle a few times a second is cheap (a ref) and lets the page
// know about the draft: leaving the page with it unsaved must ask, like any other tab's edits.
const POLL_MS = 400

/**
 * The node's source-country rules (a Passwall Node server only). A different resource from the inbound form with
 * its own Save, so it is NOT part of the tab's save bar; the tab only learns whether it holds unsaved edits.
 */
export function SourceRulesCard({ nodeId, onDirtyChange }: { nodeId: number; onDirtyChange: (dirty: boolean) => void }) {
  const handle = useRef<SourceDraftHandle | null>(null)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    const timer = setInterval(() => setDirty(handle.current?.dirty() ?? false), POLL_MS)
    return () => clearInterval(timer)
  }, [])
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])
  return <SourceRulesSection nodeId={nodeId} dirtyRef={handle} />
}
