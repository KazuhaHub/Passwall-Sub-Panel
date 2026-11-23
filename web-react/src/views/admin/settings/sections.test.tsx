// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, mountAdmin, toast } from '../adminTestHarness'
// Before the page: testSupport registers the mock of the link-probe card, which must precede the import that pulls it in.
import { echoPut, oidcConfig, reads, samlConfig, uiSettings } from './testSupport'
import SettingsView from '../SettingsView'

// One edit per section, and the body that goes out for it: the settings record is written whole, so each of these
// pins that the rest of the record is the server's, untouched, and that exactly the edited key differs.

const open = async (tab: string, ready: string | RegExp) => {
  mountAdmin(<SettingsView />, `/admin/settings?tab=${tab}`)
  await screen.findByText(ready)
}
const field = (name: string | RegExp) => screen.getByRole('textbox', { name }) as HTMLInputElement
const type = (name: string | RegExp, value: string) => fireEvent.change(field(name), { target: { value } })
const toggle = (name: string | RegExp) => fireEvent.click(screen.getByRole('switch', { name }))
const bar = () => within(screen.getByRole('region', { name: 'You have unsaved changes' }))
const save = () => fireEvent.click(bar().getByRole('button', { name: 'Save' }))
const sent = () => api.put.mock.calls[0][1]
const confirmDialog = () => screen.findByRole('alertdialog')

describe('General', () => {
  it('sends the whole record with only the edited key changed, and a typed token', async () => {
    reads({}, { geo_ip_update_source: 'maxmind' })
    await open('general', 'Runtime tunables')
    echoPut()
    type('Traffic poll interval', '7')
    fireEvent.change(document.getElementById('setting-geo_ip_update_token')!, { target: { value: 'license-key' } })
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/ui')
    expect(sent()).toEqual({ ...uiSettings({ geo_ip_update_source: 'maxmind' }), cron_traffic_pull_minutes: 7, geo_ip_update_token: 'license-key' })
  })

  it('stops every save path at a number the form refuses, the GeoIP update included', async () => {
    reads({}, { geo_ip_update_source: 'maxmind' })
    await open('general', 'Runtime tunables')
    type('Native-node sync heartbeat', '4000')
    fireEvent.click(screen.getByRole('button', { name: 'Update now' }))
    await new Promise(r => setTimeout(r, 50))
    expect(api.put).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(document.activeElement?.id).toBe('setting-node_poll_seconds')
  })

  it('leaves a secret out of the request while nothing is typed in it', async () => {
    reads({}, { geo_ip_update_source: 'maxmind', has_geo_ip_update_token: true })
    await open('general', 'Runtime tunables')
    echoPut()
    type('Traffic poll interval', '7')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent()).not.toHaveProperty('geo_ip_update_token')
  })
})

describe('Login and security', () => {
  it('sends the one edited key, and a typed captcha secret only when typed', async () => {
    reads({}, { captcha_enabled: true, captcha_provider: 'turnstile', has_captcha_secret_key: true })
    await open('security', 'Account lockout')
    echoPut()
    type('Login rate limit per IP', '20')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent()).toEqual({ ...uiSettings({ captcha_enabled: true, captcha_provider: 'turnstile', has_captcha_secret_key: true }), login_per_ip_per_min: 20 })
  })

  it('asks before forcing SSO, says SSO is off, and writes nothing when the answer is no', async () => {
    reads()
    await open('security', 'Account lockout')
    fireEvent.change(screen.getByRole('combobox', { name: 'Login mode' }), { target: { value: 'sso_redirect' } })
    save()
    const dialog = await confirmDialog()
    expect(dialog.textContent).toContain('Send everyone straight to SSO?')
    expect(dialog.textContent).toContain('SAML and OIDC are both off right now')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.put).not.toHaveBeenCalled()
  })

  it('writes after the confirmation, and a change of the JWT issuer says it ends every session', async () => {
    reads()
    await open('security', 'Account lockout')
    echoPut()
    type('JWT issuer', 'other')
    save()
    const dialog = await confirmDialog()
    expect(dialog.textContent).toContain('Every signed-in session ends, including yours')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save anyway' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent().jwt_issuer).toBe('other')
  })

  it('asks for nothing when the change is toward the safe side', async () => {
    reads({}, { require_2fa_for_staff: true, totp_enabled: true, lockout_enabled: true })
    await open('security', 'Account lockout')
    echoPut()
    toggle('Require two-factor for all admins/operators')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(sent().require_2fa_for_staff).toBe(false)
  })
})

describe('Brand', () => {
  it('sends the edited text, and refuses a subscription base URL that is not a URL', async () => {
    reads()
    await open('brand', 'Site text')
    echoPut()
    type('Site title', 'Hub')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent()).toEqual({ ...uiSettings(), site_title: 'Hub' })
  })

  it('does not save a base URL that is not a URL', async () => {
    reads()
    await open('brand', 'Site text')
    type(/Subscription base URL/, 'not a url')
    save()
    expect(api.put).not.toHaveBeenCalled()
    expect(screen.getAllByText('Must be a valid http(s):// URL').length).toBeGreaterThan(0)
  })

  it('says where the page goes before changing the panel path', async () => {
    reads()
    await open('brand', 'Site text')
    type('Panel path prefix', '/panel')
    save()
    const dialog = await confirmDialog()
    expect(dialog.textContent).toContain('Move the panel to /panel?')
    expect(dialog.textContent).toContain('/panel/admin/settings')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(api.put).not.toHaveBeenCalled()
  })

  it('names the other sections whose unsaved changes the reload would drop, and does not ask for a spelling of the same path', async () => {
    reads({}, { panel_path: '/panel' })
    await open('general', 'Runtime tunables')
    type('Traffic poll interval', '7')
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: /^Brand/ }))
    await screen.findByText('Site text')
    echoPut()
    type('Panel path prefix', ' /panel ')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(screen.queryByRole('alertdialog')).toBeNull()
    api.put.mockClear()
    type('Panel path prefix', '/other')
    save()
    const dialog = await confirmDialog()
    expect(dialog.textContent).toContain('unsaved changes in these sections are lost: General')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  })
})

describe('Subscription', () => {
  it('asks before changing the path every link carries, then sends it without the leading slash', async () => {
    reads()
    await open('subscription', 'Abuse protection')
    echoPut()
    type(/Subscription URL path segment/, '/feed')
    save()
    const dialog = await confirmDialog()
    expect(dialog.textContent).toContain('Every subscription link already handed out stops working')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save anyway' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent()).toEqual({ ...uiSettings(), sub_path: 'feed' })
  })
})

describe('User portal', () => {
  it('sends a new quick link, and holds back an enabled one with no label or URL', async () => {
    reads()
    await open('portal', 'Quick links')
    echoPut()
    fireEvent.click(screen.getByRole('button', { name: 'Add link' }))
    save()
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(document.getElementById('setting-quick_link_0_label')!, { target: { value: 'Docs' } })
    fireEvent.change(document.getElementById('setting-quick_link_0_url')!, { target: { value: 'https://docs.example.test' } })
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent().quick_links).toEqual([{ label: 'Docs', url: 'https://docs.example.test', icon: '', description: '', group: '', highlight: false, new_window: true, enabled: true, sort: 10 }])
  })
})

describe('Mail', () => {
  const mail = { enabled: true, smtp_host: 'smtp.example.test', smtp_port: 587, smtp_username: 'u', has_smtp_password: true, from_email: 'a@example.test', from_name: 'A', encryption: 'starttls' }
  it('sends the SMTP form without the stored password, and with one that is typed', async () => {
    reads({ '/admin/settings/mail': { settings: mail, templates: [] } })
    await open('mail', 'SMTP server')
    echoPut()
    type('SMTP host', 'smtp2.example.test')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/mail')
    expect(sent()).toEqual({ ...mail, smtp_host: 'smtp2.example.test' })
    expect(sent()).not.toHaveProperty('smtp_password')

    api.put.mockClear()
    fireEvent.change(document.getElementById('setting-mail_smtp_password')!, { target: { value: 'secret' } })
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent().smtp_password).toBe('secret')
  })

  it('saves one template on its own and tells the result of a test send in words next to it', async () => {
    reads({ '/admin/settings/mail': { settings: mail, templates: [{ kind: 'expire_before', subject: 'S', body: 'B', enabled: true }] } })
    await open('mail', 'Email templates')
    echoPut()
    type('Subject', 'New subject')
    fireEvent.click(screen.getByRole('button', { name: 'Save template' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/mail/templates/expire_before')
    expect(sent()).toEqual({ kind: 'expire_before', subject: 'New subject', body: 'B', enabled: true })

    api.post.mockResolvedValue({ data: { sent: true } })
    type('Test recipient', 'me@example.test')
    fireEvent.click(screen.getByRole('button', { name: 'Send test' }))
    expect(await screen.findByText('Test email sent to me@example.test')).toBeTruthy()
    expect(api.post).toHaveBeenCalledWith('/admin/settings/mail/test', { to: 'me@example.test' })
    expect(toast).toHaveBeenCalledWith('Sent', 'success')
  })
})

describe('SSO', () => {
  it('sends the SAML configuration with the private key only when one is typed', async () => {
    reads({ '/admin/settings/saml': samlConfig({ mode: 'manual', sp: { entity_id: 'e', acs_url: 'https://a.example.test/acs', cert_pem: 'CERT', has_key_pem: true }, idp: { metadata_url: 'https://idp.example.test/m', metadata_refresh_hours: 24 } }) })
    await open('sso', 'Identity Provider (IdP)')
    echoPut()
    type('Email attribute', 'mail')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/saml')
    expect(sent().sp).toEqual({ entity_id: 'e', acs_url: 'https://a.example.test/acs', cert_pem: 'CERT', key_pem: '' })
    expect(sent().attribute_mapping.email).toBe('mail')
  })

  it('sends the OIDC configuration with the client secret only when one is typed', async () => {
    reads({ '/admin/settings/oidc': oidcConfig({ has_client_secret: true, client_id: 'cid' }) })
    await open('sso&sub=oidc', /Client secret/)
    echoPut()
    type('Client ID', 'cid2')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/oidc')
    expect(sent().client_secret).toBe('')
    expect(sent().client_id).toBe('cid2')
  })
})

describe('Sections save on their own', () => {
  it("does not write another section's unsaved edit, and writes what the server holds now for the rest", async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=brand')
    await screen.findByText('Site text')
    type('Site title', 'Unsaved brand edit')
    // Another admin changed the footer meanwhile: the next read of the record has it.
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/settings/ui') return { data: uiSettings({ audit_retention_days: 111 }) }
      if (url === '/admin/groups') return { data: { items: [], total: 0, page: 1, page_size: 200 } }
      if (url === '/admin/settings/geoip/status') return { data: { enabled: false, dir: '', active: '', available: [], update: { updating: false } } }
      throw new Error(`Unexpected GET ${url}`)
    })
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: /^General/ }))
    await screen.findByText('Runtime tunables')
    echoPut()
    type('Traffic poll interval', '9')
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(sent().site_title).toBe('Site')
    expect(sent().audit_retention_days).toBe(111)
    expect(sent().cron_traffic_pull_minutes).toBe(9)
  })
})

describe('Search', () => {
  it('finds a setting by its label or its key and takes the admin to it', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings')
    await screen.findByText('Runtime tunables')
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'lockout_threshold' } })
    fireEvent.click(await screen.findByRole('button', { name: /Lockout threshold/ }))
    await screen.findByText('Account lockout')
    // The threshold sits behind the lockout switch, which is off: the jump lands on that switch.
    await waitFor(() => expect(document.activeElement?.getAttribute('role')).toBe('switch'))
    expect(document.activeElement?.closest('[data-slot="field"]')?.textContent).toContain('Enable failure lockout')
  })
})
