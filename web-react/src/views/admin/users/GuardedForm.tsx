import type { ReactNode } from 'react'
import { useFormValidity } from '@/components/form/FormValidity'

/**
 * The form element of a SaveBar form. Enter submits it, and the SaveBar's button is blocked while a number box holds
 * a draft that is not a value (which `draftProblems` cannot see), so submit must respect the same blocked state or
 * Enter would save what the button refuses. Must sit inside the FormValidityProvider.
 */
export function GuardedForm({ onSubmit, children }: { onSubmit: () => void; children: ReactNode }) {
  const { blocked } = useFormValidity()
  return (
    <form className="flex flex-col gap-4" noValidate onSubmit={e => { e.preventDefault(); if (!blocked) onSubmit() }}>
      {children}
    </form>
  )
}
