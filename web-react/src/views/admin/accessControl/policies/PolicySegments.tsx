import { ToggleButton, ToggleButtonGroup } from '@mui/material'

export default function PolicySegments<T extends string>({ value, label, options, disabled, fullWidth, onChange }: {
  value: T
  label: string
  options: readonly { value: T; label: string }[]
  disabled?: boolean
  fullWidth?: boolean
  onChange: (value: T) => void
}) {
  return <ToggleButtonGroup exclusive fullWidth value={value} disabled={disabled} aria-label={label}
    sx={{ width: fullWidth ? '100%' : { xs: '100%', sm: 'auto' } }}
    onChange={(_, next: T | null) => { if (next !== null) onChange(next) }}
    onKeyDown={event => {
      if (disabled || event.altKey || event.ctrlKey || event.metaKey) return
      const direction = ['ArrowLeft', 'ArrowUp'].includes(event.key) ? -1 : ['ArrowRight', 'ArrowDown'].includes(event.key) ? 1 : 0
      if (!direction && event.key !== 'Home' && event.key !== 'End') return
      const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button')]
      const current = buttons.findIndex(button => button === document.activeElement)
      const index = current < 0 ? options.findIndex(option => option.value === value) : current
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + direction + options.length) % options.length
      event.preventDefault(); onChange(options[next].value); buttons[next]?.focus()
    }}>
    {options.map(option => <ToggleButton key={option.value} value={option.value}>{option.label}</ToggleButton>)}
  </ToggleButtonGroup>
}
