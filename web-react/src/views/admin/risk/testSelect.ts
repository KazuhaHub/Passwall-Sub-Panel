import { fireEvent, within } from '@testing-library/react'

/** Chooses an option of a native select by the words it shows (the filters are native selects). */
export function pick(select: HTMLElement, label: string) {
  const option = within(select).getByRole('option', { name: label }) as HTMLOptionElement
  fireEvent.change(select, { target: { value: option.value } })
}

/** The words of the option a native select currently shows. */
export function shown(select: HTMLElement): string {
  return (select as HTMLSelectElement).selectedOptions[0]?.textContent ?? ''
}
