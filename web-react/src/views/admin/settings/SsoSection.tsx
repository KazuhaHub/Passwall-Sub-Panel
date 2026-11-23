import { memo, useEffect, useState } from 'react'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTabParam } from '@/hooks/useTabParam'
import { useAdminT } from '../useAdminT'
import { OidcPanel } from './sso/OidcPanel'
import { SamlPanel } from './sso/SamlPanel'

type Sub = 'saml' | 'oidc'

/**
 * SSO has two providers, each with its own record and its own Save. Both are kept mounted once visited so a draft in
 * one survives a look at the other; the section reports dirty when either is.
 */
export const SsoSection = memo(function SsoSection({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const [sub, setSub] = useTabParam<Sub>('sub', 'saml', ['saml', 'oidc'])
  const [visited, setVisited] = useState<ReadonlySet<Sub>>(new Set([sub]))
  const [dirty, setDirty] = useState<Record<Sub, boolean>>({ saml: false, oidc: false })
  useEffect(() => { onDirtyChange(dirty.saml || dirty.oidc) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])
  const shown = visited.has(sub) ? visited : new Set(visited).add(sub)
  const report = (which: Sub) => (d: boolean) => setDirty(prev => (prev[which] === d ? prev : { ...prev, [which]: d }))
  const [reports] = useState(() => ({ saml: report('saml'), oidc: report('oidc') }))

  return (
    <div className="flex flex-col gap-4">
      <Tabs value={sub} onValueChange={v => { setVisited(s => new Set(s).add(v as Sub)); setSub(v as Sub) }}>
        <TabsList>
          <TabsTrigger value="saml">{t('settings.sso.tab_saml')}</TabsTrigger>
          <TabsTrigger value="oidc">{t('settings.sso.tab_oidc')}</TabsTrigger>
        </TabsList>
      </Tabs>
      {shown.has('saml') && <div hidden={sub !== 'saml'}><SamlPanel onDirtyChange={reports.saml} /></div>}
      {shown.has('oidc') && <div hidden={sub !== 'oidc'}><OidcPanel onDirtyChange={reports.oidc} /></div>}
    </div>
  )
})
