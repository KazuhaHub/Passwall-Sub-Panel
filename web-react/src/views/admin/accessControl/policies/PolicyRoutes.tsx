import { useMemo, useRef } from 'react'
import { Navigate, useLocation, useNavigate, useParams, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { isDestUnavailable, type DestPolicy } from '@/api/accessControl'
import { ObjectPage, type ObjectBack } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useDestPolicies } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { ACCESS_PATH, parseNewPolicySeed, parseObjectId, policyPath } from '../accessRoutes'
import PolicyEditor from './PolicyEditor'
import { emptyForm, formOf, type PolicyForm } from './policyForm'

const P = 'admin:access_control.policies.'

/** The back link every policy page shares: to the access-control page as it was left. */
function useBack(): ObjectBack {
  const { t } = useTranslation(['admin'])
  return { to: useBackTarget(ACCESS_PATH), label: t('admin:access_control.title') }
}

/** The policies, read once for a page: the frame's state while they load or fail. */
function usePolicyLookup() {
  const { t } = useTranslation(['admin'])
  const policies = useDestPolicies(useQueryScope())
  const all = useMemo(() => [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])], [policies.data])
  const failed = policies.isError && !policies.data
  const state = policies.isPending ? { kind: 'loading' as const }
    : failed
      ? (isDestUnavailable(policies.error)
        ? { kind: 'not_found' as const, title: t('admin:access_control.unwired') }
        : { kind: 'error' as const, reason: policies.error, retry: () => policies.refetch(), title: t(`${P}read_failed`) })
      : null
  return { all, state, fetching: policies.isFetching }
}

/**
 * `/admin/access-control/policies/:id`. The id is read from the policy list (there is no single-policy read), so a
 * missing one is "no such policy" only once the list is settled: right after a create the list is still being
 * refetched and must not be mistaken for a deletion. The policy is captured when first seen and the editor is keyed
 * by it, so a refetch while the form is open never rewrites the form.
 */
export function PolicyDetailRoute() {
  const canView = useCan('access.view')
  const { id } = useParams()
  const n = parseObjectId(id)
  if (!canView) return <Navigate to="/admin/dashboard" replace />
  // A malformed address asks for nothing: no request for #0 or "7x".
  if (n === null) return <PolicyNotFound />
  return <PolicyLoader key={n} id={n} />
}

function PolicyNotFound() {
  const { t } = useTranslation(['admin'])
  return <ObjectPage back={useBack()} state={{ kind: 'not_found', title: t(`${P}editor.not_found`) }} />
}

function PolicyLoader({ id }: { id: number }) {
  const { t } = useTranslation(['admin'])
  const back = useBack()
  const { all, state, fetching } = usePolicyLookup()
  const seen = useRef<DestPolicy | null>(null)
  const found = all.find(p => p.id === id)
  if (!seen.current && found) seen.current = found
  if (seen.current) return <PolicyEditor key={id} policy={seen.current} back={back} onCreated={() => undefined} />
  if (state) return <ObjectPage back={back} state={state} />
  if (fetching) return <ObjectPage back={back} state={{ kind: 'loading' }} />
  return <ObjectPage back={back} state={{ kind: 'not_found', title: t(`${P}editor.not_found`) }} />
}

/**
 * `/admin/access-control/policies/new`, optionally `?kind=allow|block|report` (a blank policy of a step) or
 * `?copy=<id>` (a copy of one, switched off). The address holds only that choice, so a reload gives the same form.
 */
export function PolicyCreateRoute() {
  const canView = useCan('access.view')
  const [params] = useSearchParams()
  const { seed, invalid } = parseNewPolicySeed(params)
  if (!canView) return <Navigate to="/admin/dashboard" replace />
  if (invalid) return <PolicyNotFound />
  return seed.copy ? <CopyLoader key={`copy-${seed.copy}`} id={seed.copy} /> : <Creator key={seed.kind ?? 'block'} initial={blankOf(seed.kind ?? 'block')} />
}

/** A step's blank form: report-only is a block policy that starts in its report state. */
function blankOf(kind: 'allow' | 'block' | 'report'): PolicyForm {
  return kind === 'report' ? { ...emptyForm('block'), state: 'report' } : emptyForm(kind)
}

function CopyLoader({ id }: { id: number }) {
  const { t } = useTranslation(['admin'])
  const back = useBack()
  const { all, state, fetching } = usePolicyLookup()
  const initial = useRef<PolicyForm | null>(null)
  const source = all.find(p => p.id === id)
  if (!initial.current && source) {
    initial.current = { ...formOf(source), name: t(`${P}row.copy_name`, { name: source.name }), state: 'off', templateKey: '' }
  }
  if (initial.current) return <Creator key={`copy-${id}`} initial={initial.current} />
  if (state) return <ObjectPage back={back} state={state} />
  if (fetching) return <ObjectPage back={back} state={{ kind: 'loading' }} />
  return <ObjectPage back={back} state={{ kind: 'not_found', title: t(`${P}editor.not_found`) }} />
}

function Creator({ initial }: { initial: PolicyForm }) {
  const back = useBack()
  const navigate = useNavigate()
  const location = useLocation()
  // The new policy's page keeps this page's back link (state is carried), and replaces the form in history.
  return <PolicyEditor policy={null} initial={initial} back={back} onCreated={saved => void navigate(policyPath(saved.id), { replace: true, state: location.state })} />
}
