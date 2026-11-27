import { cn } from '@/lib/utils'

export interface Brand {
  /** The product or site name, shown beside the logo. */
  name: string
  /** The wide logo for the light and dark themes. Either may be missing. */
  logoLight?: string
  logoDark?: string
  /** The square icon: used alone when the sidebar is collapsed to its rail. */
  icon?: string
}

// Both logos are rendered and the theme picks one in CSS (`dark:hidden` /
// `hidden dark:block`, keyed on the `.dark` class on <html>): the effective mode
// is already resolved there, 'auto' included, so no component needs to re-derive it
// from the appearance store and the prefers-color-scheme media query.
export function BrandLogo({ brand, className }: { brand: Brand; className?: string }) {
  const light = brand.logoLight ?? brand.logoDark
  const dark = brand.logoDark ?? brand.logoLight
  if (!light) return <BrandIcon brand={brand} className={className} />
  return (
    <>
      <img src={light} alt="" className={cn('block h-8 w-auto max-w-[8rem] object-contain dark:hidden', className)} />
      <img src={dark} alt="" className={cn('hidden h-8 w-auto max-w-[8rem] object-contain dark:block', className)} />
    </>
  )
}

/** The square mark; the first letter of the name on a brand tile when there is no icon. */
export function BrandIcon({ brand, className }: { brand: Brand; className?: string }) {
  const src = brand.icon ?? brand.logoLight ?? brand.logoDark
  if (src) return <img src={src} alt="" className={cn('block size-8 shrink-0 rounded-md object-contain', className)} />
  return (
    <span aria-hidden className={cn('flex size-8 shrink-0 items-center justify-center rounded-md bg-brand text-sm font-semibold text-brand-foreground', className)}>
      {(brand.name.trim()[0] ?? '?').toUpperCase()}
    </span>
  )
}
