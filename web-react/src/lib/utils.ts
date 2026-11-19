import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// The shadcn class-name helper. clsx resolves conditionals, tailwind-merge
// makes the last conflicting utility win (so `cn('px-2', className)` lets a
// caller override padding instead of both fighting on source order).
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
