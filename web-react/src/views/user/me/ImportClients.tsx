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

/**
 * The client for this device, in a brand-tinted card, with its import and install buttons: the shortest path from "I have
 * a link" to "it works". "More clients" sits at the card's far corner on the overview, where it leads to the full list.
 */
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
    <Card data-testid="recommended-client" className="bg-brand/10 ring-brand/30">
      <CardContent className="flex flex-col gap-4">
        <div>
          <p className="flex items-center gap-1.5 text-xs font-medium tracking-wide text-muted-foreground uppercase"><IconStar className="size-3.5" />{t('import.recommended_label')}</p>
          <h2 className="mt-1.5 text-xl leading-tight font-medium sm:text-2xl">{client.name}</h2>
          <p className="mt-1 text-sm text-muted-foreground"><Platforms client={client} /></p>
        </div>
        <div className="flex flex-wrap items-end justify-between gap-2">
          {/* A phone packs the actions two to a row; the tutorial link takes the row under them. */}
          <div className="grid flex-[1_1_100%] grid-cols-2 gap-2 sm:flex sm:flex-[0_1_auto] sm:gap-3">
            <Button size="lg" onClick={() => { void doImport(client) }}><IconExternalLink />{t('import.import')}</Button>
            <Button size="lg" variant="outline" onClick={() => openNew(client.install_url)}><IconDownload />{t('import.install')}</Button>
            {tutorial && <Button size="lg" variant="outline" className="col-span-2 sm:col-auto" onClick={() => openNew(tutorial)}><IconHelp />{t('import.tutorial')}</Button>}
          </div>
          {onShowMore && <Button variant="ghost" size="sm" className="ml-auto" onClick={onShowMore}><IconDevices />{t('import.others_title')}</Button>}
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Every other client, one small box each (name, platforms, import and install). When none matches this device it is the
 * whole list, under "Import to client" with its intro; when one is recommended it is "More clients". The tutorial link
 * sits at the head of the card, on the right.
 */
export function ClientList({ profile, clients, hasHero }: { profile: ImportProfile; clients: SubImportClient[]; hasHero: boolean }) {
  const { t } = useMeT()
  const doImport = useImport(profile)
  const tutorial = profile.sub_import_tutorial_url
  if (clients.length === 0) return null
  return (
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <CardTitle>{hasHero ? t('import.others_title') : t('import.title')}</CardTitle>
        {tutorial && <Button variant="outline" size="sm" className="row-span-2 self-start" onClick={() => openNew(tutorial)}><IconHelp />{t('import.tutorial')}</Button>}
        {!hasHero && <CardDescription className="col-span-2">{t('import.intro')}</CardDescription>}
      </CardHeader>
      <CardContent>
        <ul className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-3">
          {clients.map(c => (
            <li key={c.name} className="flex flex-col gap-3 rounded-lg bg-card p-4 ring-1 ring-foreground/10">
              <div className="min-w-0">
                <div className="truncate font-medium" title={c.name}>{c.name}</div>
                <div className="mt-0.5 text-xs text-muted-foreground"><Platforms client={c} /></div>
              </div>
              <div className="flex gap-2">
                <Button size="sm" onClick={() => { void doImport(c) }}>{t('import.import')}</Button>
                <Button size="sm" variant="outline" onClick={() => openNew(c.install_url)}>{t('import.install')}</Button>
              </div>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
