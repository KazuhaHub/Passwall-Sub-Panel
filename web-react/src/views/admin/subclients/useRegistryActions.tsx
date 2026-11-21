import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SubClientFamily } from '@/api/settings'
import { IconAdd } from '@/lib/icons'
import { confirm } from '@/components/ConfirmHost'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { CLIENT_PRESETS, clonePreset } from './presets'
import { blankFamily, removeFamily } from './registryOps'

/**
 * Everything the toolbar and the rows do to the family list, plus which family's sheet is open. The list itself
 * is the page's draft: these only call `onChange` with a new array. New families go to the END (the list is
 * order-sensitive, so a new one never silently outranks an existing one) and open their sheet so they can be filled in.
 */
export function useRegistryActions(families: SubClientFamily[], onChange: (next: SubClientFamily[]) => void) {
  const { t } = useTranslation('admin')
  const [open, setOpen] = useState<number | null>(null)

  const addFamily = () => { onChange([...families, blankFamily()]); setOpen(families.length) }
  const addPreset = (p: SubClientFamily) => { onChange([...families, clonePreset(p)]); setOpen(families.length) }
  async function reset() {
    if (!(await confirm({
      title: t('settings.subscription.reset_clients_confirm_title'),
      message: t('settings.subscription.reset_clients_confirm_body'),
      confirmText: t('settings.subscription.reset_clients_confirm_ok'),
      destructive: true,
      initialFocus: 'cancel',
    }))) return
    setOpen(null)
    onChange(CLIENT_PRESETS.map(clonePreset))
  }
  async function remove(fi: number) {
    const name = families[fi]?.name.trim() || t('settings.subscription.unnamed_family')
    if (!(await confirm({
      title: t('settings.subscription.delete_family_title', { name }),
      message: t('settings.subscription.delete_family_body', { name }),
      confirmText: t('settings.subscription.delete_row'),
      destructive: true,
      initialFocus: 'cancel',
    }))) return
    setOpen(null)
    onChange(removeFamily(families, fi))
  }
  return { open, setOpen, addFamily, addPreset, reset, remove }
}

/** "Add from preset": every built-in family, the ones already in the list (by name) switched off and saying so. */
export function PresetMenu({ families, onPick }: { families: SubClientFamily[]; onPick: (p: SubClientFamily) => void }) {
  const { t } = useTranslation('admin')
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" />}><IconAdd />{t('settings.subscription.add_preset')}</DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-96 w-auto min-w-64 overflow-y-auto">
        {CLIENT_PRESETS.map(p => {
          const exists = families.some(f => f.name === p.name)
          return (
            <DropdownMenuItem key={p.name} disabled={exists} onClick={() => onPick(p)}>
              <span className="flex flex-col">
                <span>{p.name}</span>
                <span className="text-xs text-muted-foreground">
                  {p.keywords.join(', ')} · {p.render_format} · {t('settings.subscription.apps_count', { count: p.apps.length })}
                  {exists ? ` · ${t('settings.subscription.preset_exists')}` : ''}
                </span>
              </span>
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
