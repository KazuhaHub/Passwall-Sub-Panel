import { expect, it, vi } from 'vitest'
import { destinationNode, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import type { TFunction } from 'i18next'
import { cancelExemptionCopy, deleteListCopy, deletePolicyCopy, discardSettingsCopy, firstPublishCopy, listInUseCopy, needsFirstPublishConfirm, pauseExecutionCopy, shortenRetentionCopy, switchListKindCopy } from './confirmCopy'

it('centralizes destructive policy deletion and ordinary list-type switching', () => {
  const t = vi.fn((key: string) => key)
  expect(deletePolicyCopy(t as unknown as TFunction, 'Finance')).toEqual({ title: 'admin:access_control.policies.delete_title', message: 'admin:access_control.policies.delete_message', confirmText: 'common:actions.delete', destructive: true })
  expect(t).toHaveBeenCalledWith('admin:access_control.policies.delete_title', { name: 'Finance' })
  expect(switchListKindCopy(t as unknown as TFunction)).toEqual({ title: 'admin:access_control.list_editor.switch_title', message: 'admin:access_control.list_editor.switch_message', confirmText: 'admin:access_control.list_editor.switch_action' })
})

it.each([true, false])('centralizes execution confirmation with paused=%s', paused => {
  const t = ((key: string) => key) as TFunction
  const action = paused ? 'pause' : 'resume'
  expect(pauseExecutionCopy(t, paused)).toEqual({ title: `admin:access_control.confirm.${action}_title`, message: `admin:access_control.confirm.${action}_message`, confirmText: `admin:access_control.${action}`, destructive: paused })
})

it.each([undefined, 60001])('centralizes exemption cancellation with ETA %s', etaMs => {
  const t = vi.fn((key: string) => key)
  expect(cancelExemptionCopy(t as unknown as TFunction, 'alice@example.test', etaMs)).toEqual({ title: 'admin:access_control.exemptions.cancel_title', message: 'admin:access_control.exemptions.cancel_message', confirmText: 'admin:access_control.exemptions.cancel' })
  expect(t).toHaveBeenCalledWith('admin:access_control.exemptions.cancel_title', { upn: 'alice@example.test' })
  expect(t).toHaveBeenCalledWith('admin:access_control.exemptions.cancel_message', { eta: `admin:access_control.confirm.${etaMs === undefined ? 'eta_unknown' : 'eta_minutes'}` })
  if (etaMs !== undefined) expect(t).toHaveBeenCalledWith('admin:access_control.confirm.eta_minutes', { minutes: 2 })
})

it('keeps shortened retention destructive and carries the actual days', () => {
  const t = vi.fn((key: string) => key)
  expect(shortenRetentionCopy(t as unknown as TFunction, 7)).toEqual({ title: 'admin:access_control.confirm.shorten_title', message: 'admin:access_control.confirm.shorten_message', confirmText: 'admin:access_control.confirm.shorten_action', destructive: true })
  expect(t).toHaveBeenCalledWith('admin:access_control.confirm.shorten_message', { days: 7 })
})

it.each(['unknown', 'zero', 'mixed'] as const)('keeps first-publication node information %s', kind => {
  const t = vi.fn((key: string) => key)
  const status = kind === 'unknown' ? undefined : destinationStatus({ apply_eta_ms: 60001, nodes: kind === 'zero' ? [] : [
    destinationNode(), destinationNode({ state: 'offline' }), destinationNode({ state: 'unsupported_version' }),
    destinationNode({ kind: '3x-ui' }), destinationNode({ supports: { policy: false, hits: false, usage: false } }),
  ] })
  expect(firstPublishCopy(t as unknown as TFunction, status, kind === 'unknown' ? undefined : 60)).toEqual({ title: 'admin:access_control.confirm.first_title', message: 'admin:access_control.confirm.first_message', confirmText: 'admin:access_control.confirm.first_action' })
  if (kind === 'unknown') {
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.each_node')
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.wait_unknown')
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.eta_unknown')
    expect(t).not.toHaveBeenCalledWith('admin:access_control.confirm.node_count', expect.anything())
  } else {
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.node_count', { count: kind === 'zero' ? 0 : 1 })
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.wait_seconds', { seconds: 60 })
    expect(t).toHaveBeenCalledWith('admin:access_control.confirm.eta_minutes', { minutes: 2 })
  }
})

it('names the safe discard-dialog action as continuing to edit', () => {
  const t = ((key: string) => key) as TFunction
  expect(discardSettingsCopy(t)).toMatchObject({ cancelText: 'admin:access_control.confirm.continue_editing', confirmText: 'admin:access_control.confirm.discard_action' })
})
it('keeps deletion destructive and the rejected-reference explanation informational', () => {
  const t = ((key: string, values?: { name?: string }) => key + (values?.name ? ` ${values.name}` : '')) as TFunction
  expect(deleteListCopy(t, 'Finance')).toEqual({ title: 'admin:access_control.lists.delete_title Finance', message: 'admin:access_control.lists.delete_message', confirmText: 'common:actions.delete', destructive: true })
  expect(listInUseCopy(t)).toEqual({ title: 'admin:access_control.lists.in_use_title', message: 'admin:access_control.lists.in_use_message', confirmText: 'common:actions.close' })
})

it.each(['switch', 'editor', 'template', 'allowlist'] as const)('uses published facts at the %s entry', origin => {
  const pending = destinationPolicies({ published_has_access_control: false })
  const change = origin === 'allowlist' ? { mode: 'allowlist' as const } : { enabled: true }
  expect(needsFirstPublishConfirm(pending, [], change)).toBe(true)
  expect(needsFirstPublishConfirm({ ...pending, published_has_access_control: true }, [], change)).toBe(false)
})
it('does not ask for a disabled policy or open group and includes published allowlist-only history', () => {
  const empty = destinationPolicies({ published_has_access_control: false, block: [] })
  expect(needsFirstPublishConfirm(empty, [], { enabled: false })).toBe(false)
  expect(needsFirstPublishConfirm(empty, [], { mode: 'open' })).toBe(false)
  expect(needsFirstPublishConfirm({ ...empty, published_has_access_control: true }, [{ mode: 'allowlist' }], { enabled: true })).toBe(false)
})
