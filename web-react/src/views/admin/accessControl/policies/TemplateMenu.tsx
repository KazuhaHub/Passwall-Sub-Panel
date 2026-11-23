import { useTranslation } from 'react-i18next'

import type { DestCatalog, DestTemplate } from '@/api/accessControl'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconChevronDown } from '@/lib/icons'
import { pickLocalized } from './templates'

const T = 'admin:access_control.templates.'

/** "New policy": a blank one, the catalog's rule groups, and why some have none. */
export default function TemplateMenu({ catalog, onBlank, onUse, onShowUnavailable }: {
  catalog: DestCatalog | undefined
  onBlank: () => void
  onUse: (tpl: DestTemplate) => void
  onShowUnavailable: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const unavailable = (catalog?.unavailable.length ?? 0) > 0
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button />}>
        {t('admin:access_control.policies.new')}
        <IconChevronDown aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-56">
        <DropdownMenuItem onClick={onBlank}>{t('admin:access_control.policies.new_blank')}</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t(`${T}menu_heading`)}</DropdownMenuLabel>
          {(catalog?.templates ?? []).map(tpl => (
            <DropdownMenuItem key={tpl.key} onClick={() => onUse(tpl)}>
              <span className="flex flex-col">
                {pickLocalized(tpl.name, i18n.language)}
                {tpl.added && <span className="text-xs text-muted-foreground">{t(`${T}added`)}</span>}
              </span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        {unavailable && <DropdownMenuSeparator />}
        {unavailable && <DropdownMenuItem onClick={onShowUnavailable}>{t(`${T}why_none`)}</DropdownMenuItem>}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
