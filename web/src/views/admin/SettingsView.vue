<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getUISettings,
  putUISettings,
  getSAML,
  putSAML,
  getOIDC,
  putOIDC,
  type SAMLConfig,
  type OIDCConfig,
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
const generalLoading = ref(true)
const generalSaving = ref(false)

async function loadGeneral() {
  generalLoading.value = true
  try {
    const s = await getUISettings()
    loginMode.value = s.login_mode
    siteTitle.value = s.site_title || 'Passwall'
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
  } finally {
    generalLoading.value = false
  }
}

async function saveGeneral() {
  generalSaving.value = true
  try {
    await putUISettings({
      login_mode: loginMode.value,
      site_title: siteTitle.value,
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
      sub_base_url: subBaseURL.value,
      cron_traffic_pull_minutes: cronTrafficPullMinutes.value,
      cron_reconcile_minutes: cronReconcileMinutes.value,
      jwt_access_ttl_minutes: jwtAccessTTLMinutes.value,
      jwt_refresh_ttl_minutes: jwtRefreshTTLMinutes.value,
      jwt_issuer: jwtIssuer.value,
      sub_per_ip_per_min: subPerIPPerMin.value,
      login_per_ip_per_min: loginPerIPPerMin.value,
    })
    useSiteStore().update(siteTitle.value, logoUrl.value, logoUrlDark.value)
    ElMessage.success('已保存')
  } finally {
    generalSaving.value = false
  }
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

onMounted(() => {
  loadGeneral()
  loadSAML()
  loadOIDC()
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
              SSO 按钮和本地用户名密码表单同时显示在 /login 页。默认值。
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
          <code>用户名@域名</code>。修改不会影响已存在的 3X-UI client，只影响后续新建/重同步。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="邮箱后缀">
            <el-input v-model="emailDomain" placeholder="passwall.kazuhahub.com" />
          </el-form-item>
        </el-form>

        <h3 class="section-title" style="margin-top:32px;">公开订阅地址</h3>
        <p class="section-hint">
          用户拿到的订阅链接前缀，格式 <code>https://your.domain</code>（不要加结尾 /）。
          留空则使用相对路径 <code>/sub/&lt;token&gt;</code>，仅在面板自身域名下可用。
          也用于 SAML 自动模式生成 ACS URL。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="公网基地址">
            <el-input v-model="subBaseURL" placeholder="https://panel.example.com" />
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

    <!-- Brand Settings -->
    <div v-show="activeTab === 'brand'" v-loading="generalLoading">
      <el-card class="settings-card">
        <h3 class="section-title">站点名称</h3>
        <p class="section-hint">
          显示在侧边栏、顶栏、面包屑和登录页的品牌名称。留空则使用默认值 "Passwall"。
        </p>
        <el-form label-position="top" style="max-width:480px">
          <el-form-item label="站点标题">
            <el-input v-model="siteTitle" placeholder="Passwall" />
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
              <el-form-item label="Username claim">
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
  max-width: 720px;
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
</style>
