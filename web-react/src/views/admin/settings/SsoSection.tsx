import { memo } from 'react'
import { OidcPanel } from './sso/OidcPanel'
import { SamlPanel } from './sso/SamlPanel'
import { SubTabs, type SubPanel } from './SubTabs'

const PANELS = { saml: SamlPanel, oidc: OidcPanel } satisfies Record<string, SubPanel>

/** SSO has two providers, each with its own record and its own Save, one sub-tab each. */
export const SsoSection = memo(function SsoSection({ active }: { active: boolean }) {
  return <SubTabs section="sso" active={active} panels={PANELS} />
})
