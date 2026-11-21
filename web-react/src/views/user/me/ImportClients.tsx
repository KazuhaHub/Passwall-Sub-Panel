import type { MeProfile, SubImportClient } from '@/api/me'
import { IconDevices, IconDownload, IconExternalLink, IconHelp, IconStar } from '@/lib/icons'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { copyToClipboard } from '@/utils/clipboard'
import { buildImportURL, detectPlatform, enabledSorted, isHttpUrl, isSafeAppScheme, recommendedClient } from './format'
import { useMeT } from './useMeT'

type ImportProfile = Pick<MeProfile, 'sub_url' | 'profile_name' | 'display_name' | 'upn' | 'sub_update_interval_hours' | 'sub_import_tutorial_url'>

const openNew = (url: string) => window.open(url, '_blank', 'noopener,noreferrer')

/** The clients the portal lists, split into the one recommended for this device and the rest. */
export function splitClients(profile: Pick<MeProfile, 'sub_import_clients'>) {
  const clients = enabledSorted(profile.sub_import_clients)
  const hero = recommendedClient(clients, detectPlatform())
  return { clients, hero, others: hero ? clients.filter(c => c.name !== hero.name) : clients }
}

/**
 * Import is two different flows. A custom app scheme (clashmi://, v2rayng://) is navigated to, so the OS hands the link to
 * the client. A plain https link (V2rayN, Surge: clients with no registered scheme) is COPIED instead, because navigating
 * to it would only show the raw YAML in the browser. Script-capable schemes are refused, so a hostile or mistyped
 * import template cannot run code in this origin.
 */
function useImport(profile: ImportProfile) {
  const { t } = useMeT()
  return async (c: SubImportClient) => {
    const url = buildImportURL(profile, c)
    if (isHttpUrl(url)) { await copyToClipboard(url); return }
    if (!isSafeAppScheme(url)) { pushSnack(t('import.invalid_url'), 'error'); return }
    window.location.href = url
  }
}

function Platforms({ client }: { client: SubImportClient }) {
  const { t } = useMeT()
  return <>{client.platforms.map(p => t(`import.platform_${p}`, { defaultValue: p })).join(' · ')}</>
}

/** The client for this device, with its import and install buttons: the shortest path from "I have a link" to "it works". */
export function RecommendedClient({ profile, client, onShowMore }: {
  profile: ImportProfile
  client: SubImportClient
  /** Present on the overview only: jumps to the full client list. */
  onShowMore?: () => void
}) {
  const { t } = useMeT()
  const doImport = useImport(profile)
  const tutorial = profile.sub_import_tutorial_url
  return (
    <Card data-testid="recommended-client" className="ring-brand/40">
      <CardContent className="flex flex-col gap-3">
        <div>
          <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><IconStar className="size-3.5" />{t('import.recommended_label')}</p>
          <h2 className="mt-1 text-lg leading-tight font-semibold">{client.name}</h2>
          <p className="text-sm text-muted-foreground"><Platforms client={client} /></p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="grid flex-1 grid-cols-2 gap-2 sm:flex-none sm:flex">
            <Button onClick={() => { void doImport(client) }}><IconExternalLink />{t('import.import')}</Button>
            <Button variant="outline" onClick={() => openNew(client.install_url)}><IconDownload />{t('import.install')}</Button>
            {tutorial && <Button variant="outline" className="col-span-2 sm:col-auto" onClick={() => openNew(tutorial)}><IconHelp />{t('import.tutorial')}</Button>}
          </div>
          {onShowMore && <Button variant="ghost" className="sm:ml-auto" onClick={onShowMore}><IconDevices />{t('import.others_title')}</Button>}
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Every other client. When none matches this device it is the whole list, under "Import to client" with its intro;
 * when one is recommended it is "More clients". The tutorial link lives here as well as on the recommended card.
 */
export function ClientList({ profile, clients, hasHero }: { profile: ImportProfile; clients: SubImportClient[]; hasHero: boolean }) {
  const { t } = useMeT()
  const doImport = useImport(profile)
  const tutorial = profile.sub_import_tutorial_url
  if (clients.length === 0) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>{hasHero ? t('import.others_title') : t('import.title')}</CardTitle>
        {!hasHero && <CardDescription>{t('import.intro')}</CardDescription>}
      </CardHeader>
      <CardContent className="flex flex-col">
        <ul className="divide-y">
          {clients.map(c => (
            <li key={c.name} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0">
              <div className="min-w-0">
                <div className="truncate font-medium" title={c.name}>{c.name}</div>
                <div className="text-xs text-muted-foreground"><Platforms client={c} /></div>
              </div>
              <div className="flex shrink-0 gap-2">
                <Button size="sm" onClick={() => { void doImport(c) }}>{t('import.import')}</Button>
                <Button size="sm" variant="outline" onClick={() => openNew(c.install_url)}>{t('import.install')}</Button>
              </div>
            </li>
          ))}
        </ul>
        {tutorial && <Button variant="outline" size="sm" className="mt-3 self-start" onClick={() => openNew(tutorial)}><IconHelp />{t('import.tutorial')}</Button>}
      </CardContent>
    </Card>
  )
}
