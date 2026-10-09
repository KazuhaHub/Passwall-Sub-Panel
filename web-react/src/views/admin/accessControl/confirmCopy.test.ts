import { expect, it } from 'vitest'
import { destinationPolicies } from '@/test/accessControlFixtures'
import type { TFunction } from 'i18next'
import { deleteListCopy, discardSettingsCopy, listInUseCopy, needsFirstPublishConfirm } from './confirmCopy'

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
