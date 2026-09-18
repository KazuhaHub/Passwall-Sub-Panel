import { queryOptions, useQuery } from '@tanstack/react-query'
import {
  getMailSettings,
  getOIDC,
  getSAML,
  getUISettings,
  type MailSettings,
  type MailTemplate,
  type OIDCConfig,
  type SAMLConfig,
  type UISettings,
} from '@/api/settings'
import { settingsKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The global UI settings blob.
 *
 * One shared entry on purpose: SettingsView, SubClientsView and the scope
 * editor all edit slices of the same server record, and this is the cache they
 * read from — so a write on one page invalidates the others.
 *
 * The settings pages keep their EDITS in local state; this query holds only the
 * last thing the server said. Nothing here is allowed to reset a draft.
 */
export function uiSettingsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: settingsKeys.ui(scope),
    queryFn: ({ signal }): Promise<UISettings> => getUISettings({ signal }),
    ...freshness(policies.uiSettings),
  })
}

export function useUISettings(scope: QueryScope) {
  return useQuery(uiSettingsQuery(scope))
}

/**
 * SMTP settings plus the reminder templates, one endpoint and one read. Owned by
 * the Mail tab; the page-level settings blob is a separate entry.
 */
export function mailSettingsQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: settingsKeys.mail(scope),
    queryFn: (): Promise<{ settings: MailSettings; templates: MailTemplate[] }> => getMailSettings(),
    ...freshness(policies.uiSettings),
  })
}

export function useMailSettings(scope: QueryScope) {
  return useQuery(mailSettingsQuery(scope))
}

/** SAML IdP configuration (the SSO tab's first half). */
export function samlConfigQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: settingsKeys.saml(scope),
    queryFn: (): Promise<SAMLConfig> => getSAML(),
    ...freshness(policies.uiSettings),
  })
}

export function useSamlConfig(scope: QueryScope) {
  return useQuery(samlConfigQuery(scope))
}

/** OIDC provider configuration (the SSO tab's second half). */
export function oidcConfigQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: settingsKeys.oidc(scope),
    queryFn: (): Promise<OIDCConfig> => getOIDC(),
    ...freshness(policies.uiSettings),
  })
}

export function useOidcConfig(scope: QueryScope) {
  return useQuery(oidcConfigQuery(scope))
}
