<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Edit, Delete } from '@element-plus/icons-vue'
import {
  getUISettings,
  putUISettings,
  getMailSettings,
  putMailSettings,
  putMailTemplate,
  sendTestMail,
  sendMailAnnouncement,
  getSAML,
  putSAML,
  getOIDC,
  putOIDC,
  type SAMLConfig,
  type OIDCConfig,
  type MailSettings,
  type MailTemplate,
  type SubClientRule,
  type SubImportClient,
  type QuickLink,
  type GlobalAnnouncement,
} from '@/api/settings'
import { useSiteStore } from '@/stores/site'
import type { LoginMode } from '@/api/auth'

const activeTab = ref('general')

function copyText(text: string) {
  navigator.clipboard.writeText(text).then(
    () => ElMessage.success('已复制'),
    () => ElMessage.error('复制失败'),
  )
}

// ---- General Settings ----
const loginMode = ref<LoginMode>('dual')
const siteTitle = ref('Passwall')
const appTitle = ref('Passwall')
const iconUrl = ref('/images/HeadPicture.png')
const logoUrl = ref('')
const logoUrlDark = ref('')
const emailDomain = ref('psp.local')
const auditRetentionDays = ref(0)
const syncTaskRetentionDays = ref(0)
const disallowUserLocalLogin = ref(false)
const disallowUserPasswordChange = ref(false)
const emergencyAccessEnabled = ref(false)
const emergencyAccessHours = ref(24)
const emergencyAccessMaxCount = ref(1)
const subBaseURL = ref('')
const cronTrafficPullMinutes = ref(5)
const cronReconcileMinutes = ref(15)
const jwtAccessTTLMinutes = ref(120)
const jwtRefreshTTLMinutes = ref(10080)
const jwtIssuer = ref('passwall-sub-panel')
const subPerIPPerMin = ref(60)
const loginPerIPPerMin = ref(5)
const footerText = ref('© Passwall Sub Panel')
const generalLoading = ref(true)
const generalSaving = ref(false)

// ---- User Portal Settings ----
const portalLoading = ref(true)
const portalSaving = ref(false)
const portalSending = ref(false)
const quickLinks = ref<QuickLink[]>([])
const announcement = reactive<GlobalAnnouncement>({
  enabled: false,
  title: '',
  content: '',
  level: 'info',
  updated_at: '',
})
const sendAnnouncementMail = ref(false)
const announcementOnlyEnabled = ref(true)
const quickLinkDialogVisible = ref(false)
const quickLinkDialogMode = ref<'add' | 'edit'>('add')
const quickLinkEditIndex = ref(-1)
const quickLinkForm = reactive<QuickLink>({
  label: '',
  url: '',
  new_window: true,
  enabled: true,
  sort: 100,
})

// ---- Mail Settings ----
const mailLoading = ref(true)
const mailSaving = ref(false)
const mailTesting = ref(false)
const announcementSending = ref(false)
const testMailTo = ref('')
const announcementForm = reactive({
  subject: '',
  body: '',
  only_enabled: true,
})
const mailSettings = reactive<MailSettings>({
  enabled: false,
  smtp_host: '',
  smtp_port: 587,
  smtp_username: '',
  smtp_password: '',
  has_smtp_password: false,
  from_email: '',
  from_name: '',
  encryption: 'starttls',
  expire_before_days: 3,
  traffic_remain_percent: 10,
})
const mailTemplates = ref<MailTemplate[]>([])

function templateLabel(kind: string) {
  if (kind === 'expire_before') return '到期前提醒'
  if (kind === 'expired') return '到期提醒'
  if (kind === 'traffic_low') return '流量不足提醒'
  if (kind === 'account_disabled') return '账号停用通知'
  if (kind === 'account_enabled') return '账号恢复通知'
  if (kind === 'announcement') return '公告邮件'
  return kind
}

async function loadMail() {
  mailLoading.value = true
  try {
    const res = await getMailSettings()
    Object.assign(mailSettings, res.settings, { smtp_password: '' })
    mailTemplates.value = res.templates
  } finally {
    mailLoading.value = false
  }
}

async function saveMail() {
  mailSaving.value = true
  try {
    await putMailSettings({ ...mailSettings })
    for (const tpl of mailTemplates.value) {
      await putMailTemplate(tpl)
    }
    ElMessage.success('邮件提醒配置已保存')
    await loadMail()
  } finally {
    mailSaving.value = false
  }
}

async function testMail() {
  if (!testMailTo.value) {
    ElMessage.warning('请填写测试收件人')
    return
  }
  mailTesting.value = true
  try {
    await sendTestMail(testMailTo.value)
    ElMessage.success('测试邮件已发送')
  } finally {
    mailTesting.value = false
  }
}

async function sendAnnouncement() {
  if (!announcementForm.subject || !announcementForm.body) {
    ElMessage.warning('请填写公告标题和正文')
    return
  }
  try {
    await ElMessageBox.confirm(
      '将使用当前 SMTP 配置向用户发送公告邮件。发送后无法撤回，是否继续？',
      '确认发送公告',
      { confirmButtonText: '发送', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  announcementSending.value = true
  try {
    const res = await sendMailAnnouncement({
      subject: announcementForm.subject,
      body: announcementForm.body,
      only_enabled: announcementForm.only_enabled,
    })
    if (res.failed > 0) {
      const details = (res.errors || [])
        .slice(0, 5)
        .map((e) => `${e.upn || e.email}: ${e.error}`)
        .join('<br>')
      await ElMessageBox.alert(
        `已发送 ${res.sent} 封，跳过 ${res.skipped} 个，失败 ${res.failed} 个。<br><br>${details}`,
        '公告发送完成',
        { type: 'warning', dangerouslyUseHTMLString: true },
      )
    } else {
      ElMessage.success(`公告已发送 ${res.sent} 封，跳过 ${res.skipped} 个`)
    }
  } finally {
    announcementSending.value = false
  }
}

async function loadGeneral() {
  generalLoading.value = true
  try {
    const s = await getUISettings()
    loginMode.value = s.login_mode
    siteTitle.value = s.site_title || 'Passwall'
    appTitle.value = s.app_title || 'Passwall'
    iconUrl.value = s.icon_url === '/images/HeadPicture.png' ? '' : s.icon_url || ''
    logoUrl.value = s.logo_url || ''
    logoUrlDark.value = s.logo_url_dark || ''
    emailDomain.value = s.email_domain || 'psp.local'
    auditRetentionDays.value = s.audit_retention_days || 0
    syncTaskRetentionDays.value = s.sync_task_retention_days || 0
    disallowUserLocalLogin.value = !!s.disallow_user_local_login
    disallowUserPasswordChange.value = !!s.disallow_user_password_change
    emergencyAccessEnabled.value = !!s.emergency_access_enabled
    emergencyAccessHours.value = s.emergency_access_hours
    emergencyAccessMaxCount.value = s.emergency_access_max_count
    subBaseURL.value = s.sub_base_url || ''
    cronTrafficPullMinutes.value = s.cron_traffic_pull_minutes || 5
    cronReconcileMinutes.value = s.cron_reconcile_minutes || 15
    jwtAccessTTLMinutes.value = s.jwt_access_ttl_minutes || 120
    jwtRefreshTTLMinutes.value = s.jwt_refresh_ttl_minutes || 10080
    jwtIssuer.value = s.jwt_issuer || 'passwall-sub-panel'
    subPerIPPerMin.value = s.sub_per_ip_per_min || 60
    loginPerIPPerMin.value = s.login_per_ip_per_min || 5
    footerText.value = s.footer_text || '© Passwall Sub Panel'
  } finally {
    generalLoading.value = false
  }
}

async function saveGeneral() {
  generalSaving.value = true
  try {
    // Load current settings to preserve subscription fields.
    const current = await getUISettings()
    await putUISettings({
      login_mode: loginMode.value,
      site_title: siteTitle.value,
      app_title: appTitle.value,
      icon_url: iconUrl.value,
      logo_url: logoUrl.value,
      logo_url_dark: logoUrlDark.value,
      email_domain: emailDomain.value,
      audit_retention_days: auditRetentionDays.value,
      sync_task_retention_days: syncTaskRetentionDays.value,
      disallow_user_local_login: disallowUserLocalLogin.value,
      disallow_user_password_change: disallowUserPasswordChange.value,
      emergency_access_enabled: emergencyAccessEnabled.value,
      emergency_access_hours: emergencyAccessHours.value,
      emergency_access_max_count: emergencyAccessMaxCount.value,
      cron_traffic_pull_minutes: cronTrafficPullMinutes.value,
      cron_reconcile_minutes: cronReconcileMinutes.value,
      jwt_access_ttl_minutes: jwtAccessTTLMinutes.value,
      jwt_refresh_ttl_minutes: jwtRefreshTTLMinutes.value,
      jwt_issuer: jwtIssuer.value,
      sub_per_ip_per_min: subPerIPPerMin.value,
      login_per_ip_per_min: loginPerIPPerMin.value,
      // Preserve subscription settings
      sub_base_url: current.sub_base_url,
      sub_path: current.sub_path,
      sub_client_rules: current.sub_client_rules,
      sub_import_clients: current.sub_import_clients,
      sub_log_retention_days: current.sub_log_retention_days,
      sub_block_auto_disable: current.sub_block_auto_disable,
      sub_block_auto_disable_count: current.sub_block_auto_disable_count,
      sub_update_interval_hours: current.sub_update_interval_hours,
      quick_links: current.quick_links || [],
      global_announcement: current.global_announcement || {
        enabled: false,
        title: '',
        content: '',
        level: 'info',
        updated_at: '',
      },
      footer_text: footerText.value,
    })
    useSiteStore().update(siteTitle.value, appTitle.value, iconUrl.value, logoUrl.value, logoUrlDark.value)
    ElMessage.success('已保存')
  } finally {
    generalSaving.value = false
  }
}

function assignAnnouncement(a?: GlobalAnnouncement) {
  Object.assign(announcement, {
    enabled: !!a?.enabled,
    title: a?.title || '',
    content: a?.content || '',
    level: a?.level || 'info',
    updated_at: a?.updated_at || '',
  })
}

function announcementPayload(): GlobalAnnouncement {
  return {
    enabled: announcement.enabled,
    title: announcement.title.trim(),
    content: announcement.content.trim(),
    level: announcement.level,
    updated_at: new Date().toISOString(),
  }
}

async function loadPortal() {
  portalLoading.value = true
  try {
    const s = await getUISettings()
    quickLinks.value = (s.quick_links || []).slice().sort((a, b) => (a.sort || 0) - (b.sort || 0))
    assignAnnouncement(s.global_announcement)
  } finally {
    portalLoading.value = false
  }
}

async function savePortal(options: { sendMail?: boolean } = {}) {
  if (announcement.enabled && !announcement.title.trim() && !announcement.content.trim()) {
    ElMessage.warning('启用公告时请填写标题或正文')
    return
  }
  if (options.sendMail && (!announcement.title.trim() || !announcement.content.trim())) {
    ElMessage.warning('发送邮件时请填写公告标题和正文')
    return
  }
  if (options.sendMail) {
    try {
      await ElMessageBox.confirm(
        '将先保存公告，再向用户发送公告邮件。发送后无法撤回，是否继续？',
        '确认发送公告',
        { confirmButtonText: '保存并发送', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      return
    }
  }
  portalSaving.value = true
  portalSending.value = !!options.sendMail
  try {
    const s = await getUISettings()
    s.quick_links = quickLinks.value
      .map((link) => ({
        ...link,
        label: link.label.trim(),
        url: link.url.trim(),
        sort: link.sort || 100,
      }))
      .filter((link) => link.label && link.url)
      .sort((a, b) => (a.sort || 0) - (b.sort || 0))
    s.global_announcement = announcementPayload()
    const saved = await putUISettings(s)
    quickLinks.value = saved.quick_links || []
    assignAnnouncement(saved.global_announcement)

    if (options.sendMail) {
      const res = await sendMailAnnouncement({
        subject: announcement.title.trim(),
        body: announcement.content.trim(),
        only_enabled: announcementOnlyEnabled.value,
      })
      if (res.failed > 0) {
        const details = (res.errors || [])
          .slice(0, 5)
          .map((e) => `${e.upn || e.email}: ${e.error}`)
          .join('<br>')
        await ElMessageBox.alert(
          `公告已保存。邮件已发送 ${res.sent} 封，跳过 ${res.skipped} 个，失败 ${res.failed} 个。<br><br>${details}`,
          '公告邮件发送完成',
          { type: 'warning', dangerouslyUseHTMLString: true },
        )
      } else {
        ElMessage.success(`公告已保存并发送 ${res.sent} 封邮件，跳过 ${res.skipped} 个`)
      }
    } else {
      ElMessage.success('用户门户配置已保存')
    }
  } finally {
    portalSaving.value = false
    portalSending.value = false
  }
}

function openAddQuickLink() {
  quickLinkDialogMode.value = 'add'
  quickLinkEditIndex.value = -1
  Object.assign(quickLinkForm, {
    label: '',
    url: '',
    new_window: true,
    enabled: true,
    sort: 100,
  })
  quickLinkDialogVisible.value = true
}

function openEditQuickLink(index: number) {
  quickLinkDialogMode.value = 'edit'
  quickLinkEditIndex.value = index
  Object.assign(quickLinkForm, quickLinks.value[index])
  quickLinkDialogVisible.value = true
}

function handleQuickLinkConfirm() {
  if (!quickLinkForm.label.trim()) {
    ElMessage.warning('请输入按钮文字')
    return
  }
  if (!quickLinkForm.url.trim()) {
    ElMessage.warning('请输入跳转链接')
    return
  }
  const item: QuickLink = {
    label: quickLinkForm.label.trim(),
    url: quickLinkForm.url.trim(),
    new_window: quickLinkForm.new_window,
    enabled: quickLinkForm.enabled,
    sort: quickLinkForm.sort || 100,
  }
  if (quickLinkDialogMode.value === 'add') {
    quickLinks.value.push(item)
  } else {
    quickLinks.value[quickLinkEditIndex.value] = item
  }
  quickLinks.value.sort((a, b) => (a.sort || 0) - (b.sort || 0))
  quickLinkDialogVisible.value = false
}

function handleDeleteQuickLink(index: number) {
  const item = quickLinks.value[index]
  ElMessageBox.confirm(
    `确定要删除快捷入口 "${item.label}" 吗？`,
    '确认删除',
    { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
  ).then(() => {
    quickLinks.value.splice(index, 1)
  }).catch(() => {})
}

function toggleQuickLinkEnabled(index: number) {
  quickLinks.value[index].enabled = !quickLinks.value[index].enabled
}

// ---- SAML ----
const samlLoading = ref(true)
const samlSaving = ref(false)
const samlHasKey = ref(false)
const samlAdminGroupsText = ref('')
const saml = reactive<SAMLConfig & { sp: SAMLConfig['sp'] & { key_pem: string } }>({
  enabled: false,
  mode: 'auto',
  sp: { entity_id: '', acs_url: '', cert_pem: '', has_key_pem: false, key_pem: '' },
  idp: { metadata_url: '', metadata_refresh_hours: 24 },
  attribute_mapping: { upn: '', email: '', display_name: '', groups: '' },
  admin_group_ids: [],
  default_group_slug: '',
  new_user_defaults: { expire_days: 0, traffic_limit_bytes: 0, traffic_reset_period: 'monthly' },
})

async function loadSAML() {
  samlLoading.value = true
  try {
    const s = await getSAML()
    Object.assign(saml, s, { sp: { ...s.sp, key_pem: '' } })
    samlHasKey.value = s.sp.has_key_pem
    samlAdminGroupsText.value = (s.admin_group_ids || []).join('\n')
  } finally {
    samlLoading.value = false
  }
}

async function saveSAML() {
  samlSaving.value = true
  try {
    const adminGroups = samlAdminGroupsText.value
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    const res = await putSAML({
      enabled: saml.enabled,
      mode: saml.mode,
      sp: {
        entity_id: saml.sp.entity_id,
        acs_url: saml.sp.acs_url,
        cert_pem: saml.sp.cert_pem,
        key_pem: saml.sp.key_pem,
      },
      idp: { ...saml.idp },
      attribute_mapping: { ...saml.attribute_mapping },
      admin_group_ids: adminGroups,
      default_group_slug: saml.default_group_slug,
      new_user_defaults: { ...saml.new_user_defaults },
    })
    if (res.reload_error) {
      ElMessage.warning(`已保存，但实时重载失败：${res.reload_error}`)
    } else {
      ElMessage.success('SAML 配置已保存并实时生效')
    }
    if (saml.enabled) {
      // Backend disabled OIDC for us; reflect that in the form.
      await loadOIDC()
    }
    await loadSAML()
  } finally {
    samlSaving.value = false
  }
}

// onEnableSAML guards the toggle: turning SAML on while OIDC is also on
// would be silently overridden by the server, so we warn the admin and
// require explicit confirmation.
async function onEnableSAML(val: boolean) {
  if (val && oidc.enabled) {
    try {
      await ElMessageBox.confirm(
        'OIDC 当前已启用。SSO 一次只能启用一种，启用 SAML 会自动关闭 OIDC。是否继续？',
        '提示',
        { confirmButtonText: '继续', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      saml.enabled = false
      return
    }
  }
  saml.enabled = val
}

// ---- OIDC ----
const oidcLoading = ref(true)
const oidcSaving = ref(false)
const oidcHasSecret = ref(false)
const oidcAdminGroupsText = ref('')
const oidcScopesText = ref('openid profile email')
const oidc = reactive<OIDCConfig & { client_secret: string }>({
  enabled: false,
  issuer_url: '',
  client_id: '',
  has_client_secret: false,
  client_secret: '',
  redirect_url: '',
  scopes: [],
  attribute_mapping: { username: 'preferred_username', email: 'email', display_name: 'name', groups: 'groups' },
  admin_group_ids: [],
  default_group_slug: '',
  new_user_defaults: { expire_days: 0, traffic_limit_bytes: 0, traffic_reset_period: 'monthly' },
})

async function loadOIDC() {
  oidcLoading.value = true
  try {
    const s = await getOIDC()
    Object.assign(oidc, s, { client_secret: '' })
    oidcHasSecret.value = s.has_client_secret
    oidcAdminGroupsText.value = (s.admin_group_ids || []).join('\n')
    oidcScopesText.value = (s.scopes || []).join(' ')
  } finally {
    oidcLoading.value = false
  }
}

async function saveOIDC() {
  oidcSaving.value = true
  try {
    const adminGroups = oidcAdminGroupsText.value
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    const scopes = oidcScopesText.value
      .split(/\s+/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    const res = await putOIDC({
      enabled: oidc.enabled,
      issuer_url: oidc.issuer_url,
      client_id: oidc.client_id,
      client_secret: oidc.client_secret,
      redirect_url: oidc.redirect_url,
      scopes,
      attribute_mapping: { ...oidc.attribute_mapping },
      admin_group_ids: adminGroups,
      default_group_slug: oidc.default_group_slug,
      new_user_defaults: { ...oidc.new_user_defaults },
    })
    if (res.reload_error) {
      ElMessage.warning(`已保存，但实时重载失败：${res.reload_error}`)
    } else {
      ElMessage.success('OIDC 配置已保存并实时生效')
    }
    if (oidc.enabled) {
      // Backend disabled SAML for us; reflect that in the form.
      await loadSAML()
    }
    await loadOIDC()
  } finally {
    oidcSaving.value = false
  }
}

async function onEnableOIDC(val: boolean) {
  if (val && saml.enabled) {
    try {
      await ElMessageBox.confirm(
        'SAML 当前已启用。SSO 一次只能启用一种，启用 OIDC 会自动关闭 SAML。是否继续？',
        '提示',
        { confirmButtonText: '继续', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      oidc.enabled = false
      return
    }
  }
  oidc.enabled = val
}

// ---- Subscription Settings ----
const subLoading = ref(true)
const subSaving = ref(false)
const subPath = ref('sub')
const subClientRules = ref<SubClientRule[]>([])
const subImportClients = ref<SubImportClient[]>([])
const subLogRetentionDays = ref(7)
const subBlockAutoDisable = ref(false)
const subBlockAutoDisableCount = ref(3)
const subUpdateIntervalHours = ref(24)

// Client rule dialog state
const dialogVisible = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const editIndex = ref(-1)
const ruleForm = reactive<SubClientRule>({
  name: '',
  keywords: [],
  render_format: 'mihomo',
  enabled: true,
})
const keywordsInput = ref('')

// Import client dialog state
const importDialogVisible = ref(false)
const importDialogMode = ref<'add' | 'edit'>('add')
const importEditIndex = ref(-1)
const platformOptions = [
  { label: 'Windows', value: 'windows' },
  { label: 'macOS', value: 'macos' },
  { label: 'Linux', value: 'linux' },
  { label: 'iOS', value: 'ios' },
  { label: 'Android', value: 'android' },
  { label: '通用', value: 'universal' },
] as const
const importForm = reactive<SubImportClient>({
  name: '',
  platforms: [],
  render_format: 'mihomo',
  import_url_template: '',
  install_url: '',
  enabled: true,
  sort: 100,
})

async function loadSubSettings() {
  subLoading.value = true
  try {
    const s = await getUISettings()
    subPath.value = s.sub_path || 'sub'
    subClientRules.value = s.sub_client_rules || []
    subImportClients.value = s.sub_import_clients || []
    subLogRetentionDays.value = s.sub_log_retention_days || 7
    subBlockAutoDisable.value = !!s.sub_block_auto_disable
    subBlockAutoDisableCount.value = s.sub_block_auto_disable_count || 3
    subUpdateIntervalHours.value = s.sub_update_interval_hours || 24
  } finally {
    subLoading.value = false
  }
}

async function saveSubPath() {
  subSaving.value = true
  try {
    const s = await getUISettings()
    s.sub_base_url = subBaseURL.value
    s.sub_path = subPath.value || 'sub'
    await putUISettings(s)
    ElMessage.success('订阅路径已保存')
  } finally {
    subSaving.value = false
  }
}

async function saveSubRules() {
  subSaving.value = true
  try {
    const s = await getUISettings()
    s.sub_client_rules = subClientRules.value
    s.sub_import_clients = subImportClients.value
    s.sub_log_retention_days = subLogRetentionDays.value
    s.sub_block_auto_disable = subBlockAutoDisable.value
    s.sub_block_auto_disable_count = subBlockAutoDisableCount.value
    s.sub_update_interval_hours = subUpdateIntervalHours.value
    await putUISettings(s)
    ElMessage.success('客户端规则已保存')
  } finally {
    subSaving.value = false
  }
}

function openAddImportClient() {
  importDialogMode.value = 'add'
  importEditIndex.value = -1
  Object.assign(importForm, {
    name: '',
    platforms: [],
    render_format: 'mihomo',
    import_url_template: '',
    install_url: '',
    enabled: true,
    sort: 100,
  })
  importDialogVisible.value = true
}

function openEditImportClient(index: number) {
  importDialogMode.value = 'edit'
  importEditIndex.value = index
  const item = subImportClients.value[index]
  Object.assign(importForm, {
    ...item,
    platforms: [...item.platforms],
  })
  importDialogVisible.value = true
}

function handleImportClientConfirm() {
  if (!importForm.name.trim()) {
    ElMessage.warning('请输入客户端名称')
    return
  }
  if (importForm.platforms.length === 0) {
    ElMessage.warning('请选择至少一个系统')
    return
  }
  if (!importForm.import_url_template.trim()) {
    ElMessage.warning('请输入导入 URL 模板')
    return
  }
  const item: SubImportClient = {
    name: importForm.name.trim(),
    platforms: [...importForm.platforms],
    render_format: importForm.render_format,
    import_url_template: importForm.import_url_template.trim(),
    install_url: importForm.install_url.trim(),
    enabled: importForm.enabled,
    sort: importForm.sort || 100,
  }
  if (importDialogMode.value === 'add') {
    subImportClients.value.push(item)
  } else {
    subImportClients.value[importEditIndex.value] = item
  }
  subImportClients.value.sort((a, b) => (a.sort || 0) - (b.sort || 0))
  importDialogVisible.value = false
}

function handleDeleteImportClient(index: number) {
  const item = subImportClients.value[index]
  ElMessageBox.confirm(
    `确定要删除导入客户端 "${item.name}" 吗？`,
    '确认删除',
    { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
  ).then(() => {
    subImportClients.value.splice(index, 1)
  }).catch(() => {})
}

function toggleImportClientEnabled(index: number) {
  subImportClients.value[index].enabled = !subImportClients.value[index].enabled
}

function openAddRule() {
  dialogMode.value = 'add'
  editIndex.value = -1
  ruleForm.name = ''
  ruleForm.keywords = []
  ruleForm.render_format = 'mihomo'
  ruleForm.enabled = true
  keywordsInput.value = ''
  dialogVisible.value = true
}

function openEditRule(index: number) {
  dialogMode.value = 'edit'
  editIndex.value = index
  const rule = subClientRules.value[index]
  ruleForm.name = rule.name
  ruleForm.keywords = [...rule.keywords]
  ruleForm.render_format = rule.render_format
  ruleForm.enabled = rule.enabled
  keywordsInput.value = rule.keywords.join(', ')
  dialogVisible.value = true
}

function handleRuleConfirm() {
  if (!ruleForm.name.trim()) {
    ElMessage.warning('请输入客户端名称')
    return
  }
  const keywords = keywordsInput.value
    .split(',')
    .map(k => k.trim())
    .filter(k => k)
  if (keywords.length === 0) {
    ElMessage.warning('请输入至少一个关键词')
    return
  }
  const rule: SubClientRule = {
    name: ruleForm.name.trim(),
    keywords,
    render_format: ruleForm.render_format,
    enabled: ruleForm.enabled,
  }
  if (dialogMode.value === 'add') {
    subClientRules.value.push(rule)
  } else {
    subClientRules.value[editIndex.value] = rule
  }
  dialogVisible.value = false
}

function handleDeleteRule(index: number) {
  const rule = subClientRules.value[index]
  ElMessageBox.confirm(
    `确定要删除规则 "${rule.name}" 吗？`,
    '确认删除',
    { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
  ).then(() => {
    subClientRules.value.splice(index, 1)
  }).catch(() => {})
}

function toggleRuleEnabled(index: number) {
  subClientRules.value[index].enabled = !subClientRules.value[index].enabled
}

onMounted(() => {
  loadGeneral()
  loadMail()
  loadPortal()
  loadSAML()
  loadOIDC()
  loadSubSettings()
})
</script>

<template>
  <div>
    <div class="psp-page-header">
      <div class="psp-page-title">系统设置</div>
    </div>

    <!-- Category Tabs -->
    <div class="category-tabs">
      <button
        v-for="t in [
          { key: 'general', label: '基本设置', icon: '⚙' },
          { key: 'portal', label: '用户门户', icon: '🏠' },
          { key: 'subscription', label: '订阅管理', icon: '🔗' },
          { key: 'mail', label: '邮件提醒', icon: '✉' },
          { key: 'brand', label: '站点品牌', icon: '🎨' },
          { key: 'sso', label: 'SSO 认证', icon: '🔐' },
        ]"
        :key="t.key"
        class="category-tab"
        :class="{ active: activeTab === t.key }"
        @click="activeTab = t.key"
      >
        <span class="tab-icon">{{ t.icon }}</span>
        <span>{{ t.label }}</span>
      </button>
    </div>

    <!-- General Settings -->
    <div v-show="activeTab === 'general'" v-loading="generalLoading">
      <el-card class="settings-card">
        <h3 class="section-title">登录页模式</h3>
        <p class="section-hint">
          切换 /login 页的渲染方式。SAML 未配置时不论选什么都自动降级到 local_only。
        </p>

        <el-radio-group v-model="loginMode" class="mode-group">
          <el-radio value="sso_redirect" class="mode-option">
            <div class="mode-title">直接跳转 SSO (sso_redirect)</div>
            <div class="mode-desc">
              访问 <code>/login</code> 时直接发起 SSO 登录。
              <code>/login/local</code> 仍可通过浏览器地址栏访问，是否允许普通用户本地登录由下方策略开关控制。
            </div>
          </el-radio>
          <el-radio value="sso_first" class="mode-option">
            <div class="mode-title">SSO 优先 (sso_first)</div>
            <div class="mode-desc">
              /login 只展示 SSO 按钮，不显示本地登录入口。
              <code>/login/local</code> 仍可通过浏览器地址栏访问，是否允许普通用户本地登录由下方策略开关控制。
            </div>
          </el-radio>
          <el-radio value="dual" class="mode-option">
            <div class="mode-title">双形态 (dual)</div>
            <div class="mode-desc">
              SSO 按钮和本地 UPN/密码表单同时显示在 /login 页。默认值。
            </div>
          </el-radio>
          <el-radio value="local_only" class="mode-option">
            <div class="mode-title">仅本地账号 (local_only)</div>
            <div class="mode-desc">
              不展示 SSO 入口；/login 自动跳转到 /login/local。
            </div>
          </el-radio>
        </el-radio-group>

        <h3 class="section-title" style="margin-top:32px;">3X-UI 客户端邮箱后缀</h3>
        <p class="section-hint">
          所有面板用户（无论本地还是 SSO）在 3X-UI 里的客户端邮箱统一为
          <code>u&lt;userID&gt;-n&lt;nodeID&gt;@域名</code>。修改不会影响已存在的 3X-UI client，只影响后续新建/重同步。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="邮箱后缀">
            <el-input v-model="emailDomain" placeholder="passwall.kazuhahub.com" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">审计日志保留</h3>
        <p class="section-hint">
          自动删除超过指定天数的审计记录。设为 0 表示永不自动删除。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="保留天数">
            <el-input-number v-model="auditRetentionDays" :min="0" :max="3650" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">非管理员策略</h3>
        <p class="section-hint">
          针对普通用户（非管理员）的硬性限制。管理员账户不受影响——
          这两项的设计目的就是让管理员保留"破窗"通道，
          普通用户走 SSO 即可。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item>
            <el-switch v-model="disallowUserLocalLogin" />
            <span style="margin-left: 12px">禁止普通用户使用本地账号密码登录</span>
            <div class="hint-inline">
              即使登录页还在 dual / local_only 模式，也会在后端拒绝普通用户的本地登录提交（403）。
            </div>
          </el-form-item>
          <el-form-item>
            <el-switch v-model="disallowUserPasswordChange" />
            <span style="margin-left: 12px">禁止普通用户修改自己的密码</span>
            <div class="hint-inline">
              普通用户在 /user/me 页面点"修改密码"提交时后端返回 403。
            </div>
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">紧急使用</h3>
        <p class="section-hint">
          允许普通用户在管理员不在线时自助延长账号到期时间。每次从当前到期时间和当前时间中较晚者开始延长；
          使用次数按用户累计，管理员可在用户管理页重置。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item>
            <el-switch v-model="emergencyAccessEnabled" />
            <span style="margin-left: 12px">启用紧急使用按钮</span>
          </el-form-item>
          <el-form-item label="每次延长时长（小时）">
            <el-input-number v-model="emergencyAccessHours" :min="1" :max="720" />
          </el-form-item>
          <el-form-item label="每个用户最多允许次数">
            <el-input-number v-model="emergencyAccessMaxCount" :min="1" :max="100" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">同步任务保留</h3>
        <p class="section-hint">
          自动删除超过指定天数的 <b>已成功</b> 同步任务记录。
          等待中 / 执行中 / 已取消的任务不会被自动清理（前者还在做事，后者有诊断价值）。
          设为 0 表示永不自动删除。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="保留天数">
            <el-input-number v-model="syncTaskRetentionDays" :min="0" :max="3650" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">运行参数 <el-tag type="warning" size="small" style="margin-left:8px">重启生效</el-tag></h3>
        <p class="section-hint">
          影响后台循环、JWT 签发、限流的底层参数。
          这些值在面板启动时读取一次，因此修改后需要重启 <code>psp.exe</code> 才会生效。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="3X-UI 流量拉取间隔 (分钟)">
            <el-input-number v-model="cronTrafficPullMinutes" :min="1" :max="1440" />
          </el-form-item>
          <el-form-item label="节点/客户端 reconcile 间隔 (分钟)">
            <el-input-number v-model="cronReconcileMinutes" :min="1" :max="1440" />
          </el-form-item>
          <el-form-item label="JWT Access Token TTL (分钟)">
            <el-input-number v-model="jwtAccessTTLMinutes" :min="1" :max="10080" />
          </el-form-item>
          <el-form-item label="JWT Refresh Token TTL (分钟)">
            <el-input-number v-model="jwtRefreshTTLMinutes" :min="1" :max="525600" />
          </el-form-item>
          <el-form-item label="JWT Issuer (iss 声明)">
            <el-input v-model="jwtIssuer" placeholder="passwall-sub-panel" />
          </el-form-item>
          <el-form-item label="/sub/&lt;token&gt; 每 IP 每分钟限流">
            <el-input-number v-model="subPerIPPerMin" :min="1" :max="10000" />
          </el-form-item>
          <el-form-item label="本地登录每 IP 每分钟限流">
            <el-input-number v-model="loginPerIPPerMin" :min="1" :max="10000" />
          </el-form-item>
        </el-form>

        <div class="actions">
          <el-button type="primary" :loading="generalSaving" @click="saveGeneral">保存</el-button>
        </div>
      </el-card>
    </div>

    <!-- User Portal Settings -->
    <div v-show="activeTab === 'portal'" v-loading="portalLoading">
      <el-card class="settings-card">
        <h3 class="section-title">快捷入口</h3>
        <p class="section-hint">
          用户个人中心会显示这里启用的入口。适合放 Canvas 教程、工单系统、客户端说明等外部链接。
        </p>
        <div style="margin-bottom: 16px;">
          <el-button type="primary" :icon="Plus" @click="openAddQuickLink">添加入口</el-button>
        </div>
        <el-table :data="quickLinks" stripe>
          <el-table-column prop="label" label="按钮文字" min-width="140" />
          <el-table-column prop="url" label="跳转链接" min-width="260" show-overflow-tooltip />
          <el-table-column label="打开方式" width="110">
            <template #default="{ row }">
              <el-tag size="small" :type="row.new_window ? 'primary' : 'info'">
                {{ row.new_window ? '新窗口' : '当前页' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="sort" label="排序" width="80" />
          <el-table-column label="启用" width="70" align="center">
            <template #default="{ row, $index }">
              <el-switch :model-value="row.enabled" @change="toggleQuickLinkEnabled($index)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100" align="center">
            <template #default="{ $index }">
              <div style="display: flex; justify-content: center; gap: 4px;">
                <el-button text type="primary" :icon="Edit" @click="openEditQuickLink($index)" />
                <el-button text type="danger" :icon="Delete" @click="handleDeleteQuickLink($index)" />
              </div>
            </template>
          </el-table-column>
        </el-table>

        <h3 class="section-title" style="margin-top:32px;">置顶公告</h3>
        <p class="section-hint">
          启用后会在用户个人中心顶部显示。保存并发送邮件会复用当前 SMTP 配置，并套用邮件设置里的“公告邮件”模板。
        </p>
        <el-form label-position="top" style="max-width:720px">
          <el-form-item>
            <el-switch v-model="announcement.enabled" />
            <span style="margin-left: 12px">启用全局公告</span>
          </el-form-item>
          <el-form-item label="公告级别">
            <el-select v-model="announcement.level" style="width: 180px">
              <el-option label="普通" value="info" />
              <el-option label="提醒" value="warning" />
              <el-option label="重要" value="danger" />
            </el-select>
          </el-form-item>
          <el-form-item label="公告标题">
            <el-input v-model="announcement.title" placeholder="维护通知" />
          </el-form-item>
          <el-form-item label="公告内容">
            <el-input
              v-model="announcement.content"
              type="textarea"
              :rows="5"
              resize="vertical"
              placeholder="今晚 23:00-23:30 维护。"
            />
          </el-form-item>
          <el-form-item>
            <el-checkbox v-model="announcementOnlyEnabled">邮件只发送给启用中的用户</el-checkbox>
          </el-form-item>
        </el-form>

        <div class="actions">
          <el-button type="primary" :loading="portalSaving && !portalSending" @click="savePortal()">
            保存
          </el-button>
          <el-button
            type="warning"
            plain
            :loading="portalSending"
            @click="savePortal({ sendMail: true })"
          >
            保存并发送邮件
          </el-button>
        </div>
      </el-card>
    </div>

    <!-- Subscription Settings -->
    <div v-show="activeTab === 'subscription'" v-loading="subLoading">
      <el-card class="settings-card">
        <h3 class="section-title">订阅地址</h3>
        <p class="section-hint">
          配置订阅链接的公网地址和路径前缀。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="公网基地址">
            <el-input v-model="subBaseURL" placeholder="https://panel.example.com" />
            <div class="form-hint">
              格式 <code>https://your.domain</code>（不要加结尾 /）。留空则使用相对路径，仅在面板自身域名下可用。
            </div>
          </el-form-item>
          <el-form-item label="路径前缀">
            <el-input v-model="subPath" placeholder="sub" />
            <div class="form-hint">完整示例: {{ subBaseURL ? subBaseURL : '' }}/{{ subPath }}/&lt;token&gt;</div>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="subSaving" @click="saveSubPath">保存地址设置</el-button>
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">客户端规则</h3>
        <p class="section-hint">
          UA 检测优先于 query 参数，禁用的客户端将直接返回 403。规则按顺序匹配，命中第一条即停止。
        </p>
        <div style="margin-bottom: 16px;">
          <el-button type="primary" :icon="Plus" @click="openAddRule">添加规则</el-button>
        </div>
        <el-table :data="subClientRules" stripe>
          <el-table-column prop="name" label="名称" min-width="100" />
          <el-table-column label="关键词" min-width="150">
            <template #default="{ row }">
              <div style="display: flex; flex-wrap: wrap; gap: 4px;">
                <el-tag v-for="kw in row.keywords" :key="kw" size="small">
                  {{ kw }}
                </el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="渲染格式" width="100">
            <template #default="{ row }">
              <el-tag :type="row.render_format === 'sing-box' ? 'success' : 'primary'" size="small">
                {{ row.render_format }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="允许" width="70" align="center">
            <template #default="{ row, $index }">
              <el-switch :model-value="row.enabled" @change="toggleRuleEnabled($index)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100" align="center">
            <template #default="{ $index }">
              <div style="display: flex; justify-content: center; gap: 4px;">
                <el-button text type="primary" :icon="Edit" @click="openEditRule($index)" />
                <el-button text type="danger" :icon="Delete" @click="handleDeleteRule($index)" />
              </div>
            </template>
          </el-table-column>
        </el-table>

        <h3 class="section-title" style="margin-top:32px;">导入客户端</h3>
        <p class="section-hint">
          用户页会按系统展示这里启用的客户端。导入模板支持
          <code v-pre>{{ sub_url }}</code>、<code v-pre>{{ sub_url_encoded }}</code>、
          <code v-pre>{{ profile_name }}</code>、<code v-pre>{{ profile_name_encoded }}</code>。
        </p>
        <div style="margin-bottom: 16px;">
          <el-button type="primary" :icon="Plus" @click="openAddImportClient">添加客户端</el-button>
        </div>
        <el-table :data="subImportClients" stripe>
          <el-table-column prop="name" label="客户端" min-width="140" />
          <el-table-column label="系统" min-width="180">
            <template #default="{ row }">
              <div style="display: flex; flex-wrap: wrap; gap: 4px;">
                <el-tag v-for="p in row.platforms" :key="p" size="small">{{ p }}</el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="格式" width="100">
            <template #default="{ row }">
              <el-tag :type="row.render_format === 'sing-box' ? 'success' : 'primary'" size="small">
                {{ row.render_format }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="sort" label="排序" width="80" />
          <el-table-column label="启用" width="70" align="center">
            <template #default="{ row, $index }">
              <el-switch :model-value="row.enabled" @change="toggleImportClientEnabled($index)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100" align="center">
            <template #default="{ $index }">
              <div style="display: flex; justify-content: center; gap: 4px;">
                <el-button text type="primary" :icon="Edit" @click="openEditImportClient($index)" />
                <el-button text type="danger" :icon="Delete" @click="handleDeleteImportClient($index)" />
              </div>
            </template>
          </el-table-column>
        </el-table>

        <h3 class="section-title" style="margin-top:32px;">日志保留</h3>
        <p class="section-hint">
          自动清理超过指定天数的订阅访问日志。设为 0 表示永不自动清理。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="保留天数">
            <el-input-number v-model="subLogRetentionDays" :min="0" :max="365" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">违规自动停用</h3>
        <p class="section-hint">
          当用户多次使用被禁止的客户端访问订阅时，自动停用其账号。可用于防止用户使用不兼容的客户端导致配置泄露。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item>
            <el-switch v-model="subBlockAutoDisable" />
            <span style="margin-left: 12px">启用违规自动停用</span>
          </el-form-item>
          <el-form-item v-if="subBlockAutoDisable" label="触发次数">
            <el-input-number v-model="subBlockAutoDisableCount" :min="1" :max="100" />
            <div class="form-hint">用户使用禁用客户端达到此次数后，账号将被自动停用</div>
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">订阅更新间隔</h3>
        <p class="section-hint">
          控制客户端自动更新订阅的频率。设置为 0 表示使用默认值（24 小时）。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="更新间隔（小时）">
            <el-input-number v-model="subUpdateIntervalHours" :min="0" :max="720" />
            <div class="form-hint">客户端将按此间隔自动更新订阅配置</div>
          </el-form-item>
        </el-form>

        <div class="actions">
          <el-button type="primary" :loading="subSaving" @click="saveSubRules">保存规则</el-button>
        </div>
      </el-card>
    </div>

    <!-- Mail Settings -->
    <div v-show="activeTab === 'mail'" v-loading="mailLoading">
      <el-card class="settings-card mail-settings-card">
        <h3 class="section-title">SMTP 发件设置</h3>
        <p class="section-hint">
          用于自动发送到期和流量提醒。SSO 用户收件地址来自 SAML/OIDC 的 Email claim，和 UPN 分开保存。
        </p>
        <el-form label-position="top" style="max-width:640px">
          <el-form-item>
            <el-switch v-model="mailSettings.enabled" />
            <span style="margin-left: 12px">启用邮件提醒</span>
          </el-form-item>
          <div class="mail-form-grid">
            <el-form-item label="SMTP Host">
              <el-input v-model="mailSettings.smtp_host" placeholder="smtp.example.com" />
            </el-form-item>
            <el-form-item label="SMTP Port">
              <el-input-number v-model="mailSettings.smtp_port" :min="1" :max="65535" />
            </el-form-item>
          </div>
          <el-form-item label="加密方式">
            <el-select v-model="mailSettings.encryption" style="width: 220px">
              <el-option label="STARTTLS (587)" value="starttls" />
              <el-option label="TLS (465)" value="tls" />
              <el-option label="None" value="none" />
            </el-select>
          </el-form-item>
          <el-form-item label="SMTP 用户名">
            <el-input v-model="mailSettings.smtp_username" />
          </el-form-item>
          <el-form-item :label="mailSettings.has_smtp_password ? 'SMTP 密码 - 已存在，留空保留' : 'SMTP 密码'">
            <el-input v-model="mailSettings.smtp_password" show-password />
          </el-form-item>
          <div class="mail-form-grid">
            <el-form-item label="发件邮箱">
              <el-input v-model="mailSettings.from_email" placeholder="noreply@example.com" />
            </el-form-item>
            <el-form-item label="发件名称">
              <el-input v-model="mailSettings.from_name" placeholder="Passwall" />
            </el-form-item>
          </div>

          <h3 class="section-title" style="margin-top:24px;">提醒阈值</h3>
          <div class="mail-form-grid">
            <el-form-item label="到期前提醒天数">
              <el-input-number v-model="mailSettings.expire_before_days" :min="1" :max="365" />
            </el-form-item>
            <el-form-item label="剩余流量百分比">
              <el-input-number v-model="mailSettings.traffic_remain_percent" :min="1" :max="100" />
              <span class="input-suffix">%</span>
            </el-form-item>
          </div>

          <h3 class="section-title" style="margin-top:24px;">测试发送</h3>
          <div class="test-mail-row">
            <el-input v-model="testMailTo" placeholder="you@example.com" />
            <el-button :loading="mailTesting" @click="testMail">发送测试</el-button>
          </div>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">手动公告</h3>
        <p class="section-hint">
          即时发送给有收件邮箱的用户。这里填写公告内容，实际邮件外观使用下方“公告邮件”模板。
        </p>
        <el-form label-position="top" style="max-width:720px">
          <el-form-item label="公告标题">
            <el-input v-model="announcementForm.subject" placeholder="{{.SiteTitle}} 服务公告" />
          </el-form-item>
          <el-form-item label="公告正文">
            <el-input
              v-model="announcementForm.body"
              type="textarea"
              :rows="8"
              placeholder="你好 {{.DisplayName}}，&#10;&#10;这里填写公告内容。"
            />
          </el-form-item>
          <el-form-item>
            <el-switch v-model="announcementForm.only_enabled" />
            <span style="margin-left: 12px">仅发送给已启用用户</span>
          </el-form-item>
          <div class="actions">
            <el-button type="warning" :loading="announcementSending" @click="sendAnnouncement">
              发送公告
            </el-button>
          </div>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">邮件模板</h3>
        <p class="section-hint">
          支持 HTML。可用变量包括：SiteTitle、LogoURL、GeneratedAt、UPN、DisplayName、Email、SubURL、ExpireAt、ExpireBeforeDays、TrafficRemainPercent、PeriodUsedGB、TrafficLimitGB、TrafficRemainGB、AnnouncementTitle、AnnouncementBody、AnnouncementBodyHTML。
        </p>
        <div class="mail-template-list">
          <div v-for="tpl in mailTemplates" :key="tpl.kind" class="mail-template-item">
            <div class="mail-template-head">
              <h4 class="sub-section-title">{{ templateLabel(tpl.kind) }}</h4>
              <el-switch v-model="tpl.enabled" active-text="启用" />
            </div>
            <el-form label-position="top">
              <el-form-item label="标题">
                <el-input v-model="tpl.subject" />
              </el-form-item>
              <el-form-item label="正文">
                <el-input v-model="tpl.body" type="textarea" :rows="7" />
              </el-form-item>
            </el-form>
          </div>
        </div>

        <div class="actions">
          <el-button type="primary" :loading="mailSaving" @click="saveMail">保存邮件设置</el-button>
        </div>
      </el-card>
    </div>

    <!-- Brand Settings -->
    <div v-show="activeTab === 'brand'" v-loading="generalLoading">
      <el-card class="settings-card">
        <h3 class="section-title">名称</h3>
        <p class="section-hint">
          站点名称用于浏览器标题、面包屑和系统语境；应用名称显示在左上角和登录页。Logo 可以带品牌名，应用名称可以保持为 Passwall。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="站点名称">
            <el-input v-model="siteTitle" placeholder="Passwall" />
          </el-form-item>
          <el-form-item label="应用名称（左上角 / 登录页）">
            <el-input v-model="appTitle" placeholder="Passwall" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">Logo</h3>
        <p class="section-hint">
          填入 Logo 图片的 URL 地址。留空则使用内置默认 Logo。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="Logo 地址（亮色模式）">
            <el-input v-model="logoUrl" placeholder="留空使用默认 Logo" />
          </el-form-item>
          <el-form-item label="Logo 地址（暗色模式）">
            <el-input v-model="logoUrlDark" placeholder="留空则跟随亮色 Logo" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">Icon</h3>
        <p class="section-hint">
          浏览器标签页和快捷方式图标。留空使用内置默认头像图标。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="Icon 地址">
            <el-input v-model="iconUrl" placeholder="留空使用默认 Icon" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">页脚文本</h3>
        <p class="section-hint">
          登录页面底部显示的文本。支持版权符号和年份。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="页脚文本">
            <el-input v-model="footerText" placeholder="© Passwall Sub Panel" />
          </el-form-item>
        </el-form>

        <div class="actions">
          <el-button type="primary" :loading="generalSaving" @click="saveGeneral">保存</el-button>
        </div>
      </el-card>
    </div>

    <!-- SSO Settings -->
    <div v-show="activeTab === 'sso'">
      <el-tabs v-model="ssoSubTab" class="sso-tabs">
        <el-tab-pane label="SAML 2.0" name="saml">
          <el-card v-loading="samlLoading" class="settings-card">
            <!-- Enable + exclusion hint — outside el-form so Element Plus never collapses it -->
            <div class="sso-top-row">
              <el-switch :model-value="saml.enabled" active-text="启用 SAML SSO"
                @update:model-value="onEnableSAML" />
              <div v-if="oidc.enabled" class="exclusion-hint">
                当前 OIDC 已启用，启用 SAML 会自动关闭 OIDC（SSO 一次只能启用一种）。
              </div>
            </div>

            <!-- Mode selector — outside el-form, always visible -->
            <div class="mode-selector-row">
              <h4 class="sub-section-title">配置模式</h4>
              <el-radio-group v-model="saml.mode" size="large">
                <el-radio-button value="auto">自动（粘贴 Federation Metadata URL）</el-radio-button>
                <el-radio-button value="manual">手动</el-radio-button>
              </el-radio-group>
            </div>

            <el-form label-position="top">
              <!-- AUTO: only ask for the IdP federation metadata URL + group settings. -->
              <template v-if="saml.mode === 'auto'">
                <p class="section-hint">
                  自动模式：只需填 IdP 的 Federation Metadata URL，面板会自动推导 SP 信息并生成自签名密钥对。
                  保存后把下方"填给 IdP 的信息"复制到 IdP（如 Entra ID 企业应用）。
                </p>

                <h4 class="sub-section-title">IdP Federation Metadata URL</h4>
                <el-form-item label="IdP Federation Metadata URL">
                  <el-input v-model="saml.idp.metadata_url"
                    placeholder="https://login.microsoftonline.com/<tenant-id>/federationmetadata/2007-06/federationmetadata.xml" />
                </el-form-item>
                <el-form-item label="自动刷新间隔 (小时)">
                  <el-input-number v-model="saml.idp.metadata_refresh_hours" :min="1" :max="168" />
                </el-form-item>

                <!-- IdP 配置信息 - 仅保存后才出现 -->
                <template v-if="saml.sp.entity_id">
                  <h4 class="sub-section-title">填给 IdP 的信息</h4>
                  <p class="section-hint">将以下信息填入 IdP 的 SAML 应用配置（Entra ID → 企业应用 → 单一登入 → SAML → 基本设定）。</p>

                  <div class="idp-info-block">
                    <div class="idp-info-row">
                      <span class="idp-info-label">SP Metadata URL<br><small>Entra ID 可直接从此 URL 自动导入</small></span>
                      <div class="idp-info-value-row">
                        <a :href="saml.sp.entity_id" target="_blank" class="idp-info-link">{{ saml.sp.entity_id }}</a>
                        <el-button size="small" @click="copyText(saml.sp.entity_id)">复制</el-button>
                      </div>
                    </div>
                    <div class="idp-info-row">
                      <span class="idp-info-label">Identifier (Entity ID)</span>
                      <div class="idp-info-value-row">
                        <code class="idp-info-code">{{ saml.sp.entity_id }}</code>
                        <el-button size="small" @click="copyText(saml.sp.entity_id)">复制</el-button>
                      </div>
                    </div>
                    <div class="idp-info-row">
                      <span class="idp-info-label">Reply URL (ACS URL)</span>
                      <div class="idp-info-value-row">
                        <code class="idp-info-code">{{ saml.sp.acs_url }}</code>
                        <el-button size="small" @click="copyText(saml.sp.acs_url)">复制</el-button>
                      </div>
                    </div>
                    <div v-if="saml.sp.cert_pem" class="idp-info-row idp-info-row--cert">
                      <span class="idp-info-label">SP Certificate (PEM)<br><small>部分 IdP 需要上传以验证 SP 签名</small></span>
                      <div class="idp-info-value-row">
                        <el-button size="small" @click="copyText(saml.sp.cert_pem)">复制证书 PEM</el-button>
                      </div>
                    </div>
                  </div>
                </template>
                <p v-else class="section-hint" style="margin-top:12px;">
                  ⬆ 填入 IdP Metadata URL 并保存后，这里会显示需要填给 IdP 的信息。
                </p>
              </template>

              <!-- MANUAL: full SP / IdP / attribute mapping form -->
              <template v-else>
                <h4 class="sub-section-title">SP（本面板）</h4>
                <el-form-item label="Entity ID">
                  <el-input v-model="saml.sp.entity_id" placeholder="https://panel.example.com/api/auth/saml/metadata" />
                </el-form-item>
                <el-form-item label="ACS URL">
                  <el-input v-model="saml.sp.acs_url" placeholder="https://panel.example.com/api/auth/saml/acs" />
                </el-form-item>
                <el-form-item label="SP 证书 (PEM)">
                  <el-input v-model="saml.sp.cert_pem" type="textarea" :rows="6" placeholder="-----BEGIN CERTIFICATE-----" />
                </el-form-item>
                <el-form-item :label="samlHasKey ? '私钥 (PEM) — 已存在，留空保留' : '私钥 (PEM)'">
                  <el-input v-model="saml.sp.key_pem" type="textarea" :rows="6"
                    :placeholder="samlHasKey ? '留空 = 保留现有私钥' : '-----BEGIN PRIVATE KEY-----'" show-password />
                </el-form-item>

                <h4 class="sub-section-title">IdP</h4>
                <el-form-item label="Metadata URL">
                  <el-input v-model="saml.idp.metadata_url" placeholder="https://login.example.com/saml/metadata.xml" />
                </el-form-item>
                <el-form-item label="Metadata 刷新间隔 (小时)">
                  <el-input-number v-model="saml.idp.metadata_refresh_hours" :min="1" :max="168" />
                </el-form-item>

                <h4 class="sub-section-title">属性映射</h4>
                <el-form-item label="UPN claim">
                  <el-input v-model="saml.attribute_mapping.upn" />
                </el-form-item>
                <el-form-item label="Email claim">
                  <el-input v-model="saml.attribute_mapping.email" />
                </el-form-item>
                <el-form-item label="Display name claim">
                  <el-input v-model="saml.attribute_mapping.display_name" />
                </el-form-item>
                <el-form-item label="Groups claim">
                  <el-input v-model="saml.attribute_mapping.groups" />
                </el-form-item>
              </template>

              <h4 class="sub-section-title">管理员组</h4>
              <p class="section-hint">
                每行填一个 IdP group ID。登录时检查用户所属组，命中则授予管理员权限，否则降为普通用户。
              </p>
              <el-form-item label="管理员 group ID 列表（每行一个）">
                <el-input v-model="samlAdminGroupsText" type="textarea" :rows="3" />
              </el-form-item>

              <div class="actions">
                <el-button type="primary" :loading="samlSaving" @click="saveSAML">保存 SAML 配置</el-button>
              </div>
            </el-form>
          </el-card>
        </el-tab-pane>

        <el-tab-pane label="OIDC / OAuth2" name="oidc">
          <el-card v-loading="oidcLoading" class="settings-card">
            <div class="sso-top-row">
              <el-switch :model-value="oidc.enabled" active-text="启用 OIDC SSO"
                @update:model-value="onEnableOIDC" />
              <div v-if="saml.enabled" class="exclusion-hint">
                当前 SAML 已启用，启用 OIDC 会自动关闭 SAML（SSO 一次只能启用一种）。
              </div>
            </div>

            <el-form label-position="top">
              <h4 class="sub-section-title">Provider</h4>
              <el-form-item label="Issuer URL">
                <el-input v-model="oidc.issuer_url" placeholder="https://login.example.com" />
              </el-form-item>
              <el-form-item label="Client ID">
                <el-input v-model="oidc.client_id" />
              </el-form-item>
              <el-form-item :label="oidcHasSecret ? 'Client Secret — 已存在，留空保留' : 'Client Secret'">
                <el-input v-model="oidc.client_secret"
                  :placeholder="oidcHasSecret ? '留空 = 保留现有 secret' : ''" show-password />
              </el-form-item>
              <el-form-item label="Redirect URL">
                <el-input v-model="oidc.redirect_url" placeholder="https://panel.example.com/api/auth/oidc/callback" />
              </el-form-item>
              <el-form-item label="Scopes (空格分隔)">
                <el-input v-model="oidcScopesText" placeholder="openid profile email" />
              </el-form-item>

              <h4 class="sub-section-title">Claim 映射</h4>
              <el-form-item label="UPN claim">
                <el-input v-model="oidc.attribute_mapping.username" placeholder="preferred_username" />
              </el-form-item>
              <el-form-item label="Email claim">
                <el-input v-model="oidc.attribute_mapping.email" />
              </el-form-item>
              <el-form-item label="Display name claim">
                <el-input v-model="oidc.attribute_mapping.display_name" />
              </el-form-item>
              <el-form-item label="Groups claim">
                <el-input v-model="oidc.attribute_mapping.groups" />
              </el-form-item>

              <h4 class="sub-section-title">管理员组</h4>
              <p class="section-hint">
                每行填一个 IdP group ID。登录时检查用户所属组，命中则授予管理员权限，否则降为普通用户。
              </p>
              <el-form-item label="管理员 group ID 列表（每行一个）">
                <el-input v-model="oidcAdminGroupsText" type="textarea" :rows="3" />
              </el-form-item>

              <div class="actions">
                <el-button type="primary" :loading="oidcSaving" @click="saveOIDC">保存 OIDC 配置</el-button>
              </div>
            </el-form>
          </el-card>
        </el-tab-pane>
      </el-tabs>
    </div>

    <!-- Quick Link Dialog -->
    <el-dialog
      v-model="quickLinkDialogVisible"
      :title="quickLinkDialogMode === 'add' ? '添加快捷入口' : '编辑快捷入口'"
      width="560px"
    >
      <el-form label-width="110px">
        <el-form-item label="按钮文字">
          <el-input v-model="quickLinkForm.label" placeholder="使用教程" />
        </el-form-item>
        <el-form-item label="跳转链接">
          <el-input v-model="quickLinkForm.url" placeholder="https://canvas.example.com/..." />
        </el-form-item>
        <el-form-item label="打开方式">
          <el-radio-group v-model="quickLinkForm.new_window">
            <el-radio :value="true">新窗口</el-radio>
            <el-radio :value="false">当前页</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="quickLinkForm.sort" :min="0" :max="9999" />
        </el-form-item>
        <el-form-item label="状态">
          <el-checkbox v-model="quickLinkForm.enabled">启用</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="quickLinkDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleQuickLinkConfirm">确定</el-button>
      </template>
    </el-dialog>

    <!-- Subscription Rule Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'add' ? '添加规则' : '编辑规则'"
      width="480px"
    >
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="ruleForm.name" placeholder="例如: Shadowrocket" />
        </el-form-item>
        <el-form-item label="关键词">
          <el-input v-model="keywordsInput" placeholder="多个用逗号分隔，例如: shadowrocket" />
          <div class="form-hint">从 User-Agent 中匹配的关键词，多个用逗号分隔</div>
        </el-form-item>
        <el-form-item label="渲染格式">
          <el-radio-group v-model="ruleForm.render_format">
            <el-radio value="mihomo">mihomo</el-radio>
            <el-radio value="sing-box">sing-box</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="状态">
          <el-checkbox v-model="ruleForm.enabled">允许访问</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleRuleConfirm">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="importDialogVisible"
      :title="importDialogMode === 'add' ? '添加导入客户端' : '编辑导入客户端'"
      width="680px"
    >
      <el-form label-width="120px">
        <el-form-item label="客户端名称">
          <el-input v-model="importForm.name" placeholder="例如: Clash Verge Rev" />
        </el-form-item>
        <el-form-item label="适用系统">
          <el-checkbox-group v-model="importForm.platforms">
            <el-checkbox v-for="p in platformOptions" :key="p.value" :value="p.value">
              {{ p.label }}
            </el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item label="渲染格式">
          <el-radio-group v-model="importForm.render_format">
            <el-radio value="mihomo">mihomo</el-radio>
            <el-radio value="sing-box">sing-box</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="导入 URL 模板">
          <el-input
            v-model="importForm.import_url_template"
            type="textarea"
            :rows="2"
            placeholder="clash://install-config?url={{ sub_url_encoded }}"
          />
          <div class="form-hint">用于生成用户页的一键导入链接。</div>
        </el-form-item>
        <el-form-item label="安装地址">
          <el-input v-model="importForm.install_url" placeholder="https://..." />
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="importForm.sort" :min="0" :max="9999" />
        </el-form-item>
        <el-form-item label="状态">
          <el-checkbox v-model="importForm.enabled">启用</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleImportClientConfirm">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script lang="ts">
export default { data() { return { ssoSubTab: 'saml' } } }
</script>

<style scoped>
.psp-page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}

.psp-page-title {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-main);
}

/* Category Tabs */
.category-tabs {
  display: flex;
  gap: 8px;
  margin-bottom: 24px;
  flex-wrap: wrap;
}

.category-tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  border: 1px solid var(--header-border);
  border-radius: 12px;
  background: var(--card-bg);
  color: var(--text-muted);
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  transition: all 0.2s ease;
}

.category-tab:hover {
  color: var(--text-main);
  border-color: #6366f1;
  background: rgba(99, 102, 241, 0.05);
}

.category-tab.active {
  color: #fff;
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
  border-color: transparent;
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.3);
}

.tab-icon {
  font-size: 16px;
}

/* Cards */
.settings-card {
  border-radius: 16px;
  border: 1px solid var(--header-border);
  background: var(--card-bg);
}

.mail-settings-card {
  max-width: 880px;
}

.mail-form-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(160px, 220px);
  gap: 16px;
}

.test-mail-row {
  display: flex;
  gap: 10px;
  max-width: 520px;
}

.mail-template-list {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.mail-template-item {
  border-top: 1px solid var(--header-border);
  padding-top: 14px;
}

.mail-template-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.input-suffix {
  color: var(--text-muted);
  font-size: 13px;
  margin-left: 10px;
}

.sso-tabs {
  max-width: 920px;
}

.section-title {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 6px;
  color: var(--text-main);
}

.sub-section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-main);
  margin: 18px 0 8px;
  border-left: 3px solid var(--accent, #6366f1);
  padding-left: 8px;
}

.section-hint {
  color: var(--text-muted);
  font-size: 13px;
  margin: 0 0 16px;
}

.hint-inline {
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 4px;
  margin-left: 60px;
}

.mode-group {
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: stretch;
  margin-bottom: 24px;
}

.mode-option {
  align-items: flex-start;
  margin-right: 0;
  padding: 12px 16px;
  border: 1px solid var(--header-border);
  border-radius: 12px;
  transition: var(--transition);
  white-space: normal;
  height: auto;
}

.mode-option :deep(.el-radio__label) {
  white-space: normal;
}

.mode-title {
  font-weight: 600;
  color: var(--text-main);
  margin-bottom: 4px;
}

.mode-desc {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}

.actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

.exclusion-hint {
  margin-top: 6px;
  padding: 6px 10px;
  border-radius: 8px;
  background: rgba(245, 158, 11, 0.1);
  border: 1px solid rgba(245, 158, 11, 0.4);
  color: #f59e0b;
  font-size: 12px;
  line-height: 1.4;
}

.sso-top-row {
  margin-bottom: 20px;
}

.mode-selector-row {
  margin-bottom: 20px;
}

/* IdP 配置信息展示块 */
.idp-info-block {
  border: 1px solid var(--header-border);
  border-radius: 12px;
  overflow: hidden;
  margin-bottom: 16px;
}

.idp-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--header-border);
}

.idp-info-row:last-child {
  border-bottom: none;
}

.idp-info-row--cert {
  flex-wrap: wrap;
}

.idp-info-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-main);
  min-width: 180px;
  flex-shrink: 0;
}

.idp-info-label small {
  display: block;
  font-weight: 400;
  color: var(--text-muted);
  font-size: 11px;
  margin-top: 2px;
}

.idp-info-value-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.idp-info-code {
  font-family: monospace;
  font-size: 12px;
  color: var(--text-main);
  word-break: break-all;
  flex: 1;
}

.idp-info-link {
  font-size: 12px;
  color: #6366f1;
  word-break: break-all;
  flex: 1;
  text-decoration: none;
}

.idp-info-link:hover {
  text-decoration: underline;
}

@media (max-width: 768px) {
  .mail-form-grid {
    grid-template-columns: 1fr;
  }

  .test-mail-row {
    flex-direction: column;
  }

  .psp-page-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  .category-tabs {
    flex-wrap: wrap;
    gap: 6px;
  }

  .category-tab {
    padding: 8px 14px;
    font-size: 13px;
  }

  .el-table {
    font-size: 13px;
  }
}

.keyword-tag {
  margin-right: 4px;
  margin-bottom: 4px;
}

.form-hint {
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 4px;
}
</style>
