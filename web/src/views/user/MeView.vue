<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import QRCode from 'qrcode'
import { client } from '@/api/client'
import { getMyUsage, type UsageReport } from '@/api/traffic'

interface MeProfile {
  id: number
  display_name?: string
  upn: string
  sub_url: string
  sub_import_clients: SubImportClient[]
  quick_links: QuickLink[]
  global_announcement?: GlobalAnnouncement | null
  expire_at?: string | null
  traffic_limit_bytes: number
  traffic_reset_period: string
  enabled: boolean
  can_change_password: boolean
  emergency_access: {
    enabled: boolean
    duration_hours: number
    max_count: number
    used_count: number
    remaining: number
  }
}

interface SubImportClient {
  name: string
  platforms: string[]
  render_format: 'mihomo' | 'sing-box'
  import_url_template: string
  install_url: string
  enabled: boolean
  sort: number
}

interface QuickLink {
  label: string
  url: string
  new_window: boolean
  enabled: boolean
  sort: number
}

interface GlobalAnnouncement {
  enabled: boolean
  title: string
  content: string
  level: 'info' | 'warning' | 'danger'
  updated_at: string
}

const profile = ref<MeProfile | null>(null)
const displayName = computed(() => profile.value?.display_name || profile.value?.upn || '')
const detectedPlatform = computed(() => detectPlatform())
const platformLabel = computed(() => platformName(detectedPlatform.value))
const importClients = computed(() => {
  const clients = (profile.value?.sub_import_clients || [])
    .filter((c) => c.enabled)
    .slice()
    .sort((a, b) => (a.sort || 0) - (b.sort || 0))
  const matched = clients.filter((c) => c.platforms.includes(detectedPlatform.value) || c.platforms.includes('universal'))
  return matched.length > 0 ? matched : clients
})
const quickLinks = computed(() => (profile.value?.quick_links || [])
  .filter((link) => link.enabled)
  .slice()
  .sort((a, b) => (a.sort || 0) - (b.sort || 0)))
const announcement = computed(() => profile.value?.global_announcement || null)
const usage = ref<UsageReport | null>(null)
const qrDataURL = ref<string>('')
const showQRCode = ref(false)
const passwordDialog = ref(false)
const rulesDialog = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const emergencyBusy = ref(false)
const personalRules = ref('')
const personalRulesSaved = ref('')
const rulesBusy = ref(false)
const canUseEmergency = computed(() => {
  const e = profile.value?.emergency_access
  return !!profile.value?.expire_at && !!e?.enabled && e.remaining > 0
})
const personalRulesDirty = computed(() => personalRules.value.trim() !== personalRulesSaved.value.trim())

async function load() {
  const [p, u, rules] = await Promise.all([
    client.get<MeProfile>('/user/me').then((r) => r.data),
    getMyUsage().catch(() => null),
    client.get<{ personal_rules: string }>('/user/me/rules').then((r) => r.data).catch(() => ({ personal_rules: '' })),
  ])
  profile.value = p
  usage.value = u
  personalRules.value = rules.personal_rules || ''
  personalRulesSaved.value = rules.personal_rules || ''
  if (p.sub_url) {
    qrDataURL.value = await QRCode.toDataURL(p.sub_url, { width: 200, margin: 2 })
  }
}

function copyText(s: string) {
  navigator.clipboard.writeText(s)
  ElMessage.success('已复制到剪贴板')
}

function subURLFor(format: 'mihomo' | 'sing-box') {
  const raw = profile.value?.sub_url || ''
  const absolute = new URL(raw, window.location.origin)
  absolute.searchParams.set('client', format)
  return absolute.toString()
}

function importURLFor(item: SubImportClient) {
  const subURL = subURLFor(item.render_format)
  const profileName = `${displayName.value || 'Passwall'} - ${item.name}`
  return item.import_url_template
    .replaceAll('{{ sub_url }}', subURL)
    .replaceAll('{{sub_url}}', subURL)
    .replaceAll('{{ sub_url_encoded }}', encodeURIComponent(subURL))
    .replaceAll('{{sub_url_encoded}}', encodeURIComponent(subURL))
    .replaceAll('{{ profile_name }}', profileName)
    .replaceAll('{{profile_name}}', profileName)
    .replaceAll('{{ profile_name_encoded }}', encodeURIComponent(profileName))
    .replaceAll('{{profile_name_encoded}}', encodeURIComponent(profileName))
}

function openImport(item: SubImportClient) {
  window.location.href = importURLFor(item)
}

function openInstall(item: SubImportClient) {
  if (item.install_url) window.open(item.install_url, '_blank', 'noopener,noreferrer')
}

function openQuickLink(link: QuickLink) {
  if (link.new_window) {
    window.open(link.url, '_blank', 'noopener,noreferrer')
    return
  }
  window.location.href = link.url
}

function announcementSymbol(level?: string) {
  if (level === 'warning' || level === 'danger') return '!'
  return 'i'
}

function detectPlatform() {
  const ua = navigator.userAgent.toLowerCase()
  if (/iphone|ipad|ipod/.test(ua)) return 'ios'
  if (/android/.test(ua)) return 'android'
  if (/windows/.test(ua)) return 'windows'
  if (/mac os x|macintosh/.test(ua)) return 'macos'
  if (/linux/.test(ua)) return 'linux'
  return 'universal'
}

function platformName(p: string) {
  switch (p) {
    case 'windows': return 'Windows'
    case 'macos': return 'macOS'
    case 'linux': return 'Linux'
    case 'ios': return 'iOS'
    case 'android': return 'Android'
    default: return '当前系统'
  }
}

async function confirmResetCredentials() {
  try {
    await ElMessageBox.confirm(
      '重置凭证会导致您的旧订阅链接立即失效，且您现有正在使用的所有节点连接都会被强制断开。重置后，您必须去客户端中更新订阅才能重新上网。确定继续吗？',
      '重置凭证',
      { type: 'warning', confirmButtonText: '确定重置', cancelButtonText: '取消' }
    )
    const { data } = await client.post<{ sub_token: string; sub_url: string; uuid: string }>(
      '/user/me/reset-credentials',
    )
    if (profile.value) profile.value.sub_url = data.sub_url
    qrDataURL.value = await QRCode.toDataURL(data.sub_url, { width: 200, margin: 2 })
    ElMessage.success('已重置！请务必更新您的订阅配置。')
  } catch (e: any) {
    if (e !== 'cancel') ElMessage.error('操作失败')
  }
}

async function changePassword() {
  if (!oldPassword.value || !newPassword.value) {
    ElMessage.warning('请填写完整')
    return
  }
  await client.post('/user/me/change-password', {
    old_password: oldPassword.value,
    new_password: newPassword.value,
  })
  ElMessage.success('密码已更新')
  passwordDialog.value = false
  oldPassword.value = ''
  newPassword.value = ''
}

async function useEmergencyAccess() {
  const e = profile.value?.emergency_access
  if (!profile.value || !e) return
  try {
    await ElMessageBox.confirm(
      `确定使用一次紧急使用机会，将账号延长 ${e.duration_hours} 小时？`,
      '紧急使用',
      { type: 'warning', confirmButtonText: '立即延长', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  emergencyBusy.value = true
  try {
    const { data } = await client.post<{
      expire_at: string
      used_count: number
      max_count: number
      remaining: number
      sync_pending?: boolean
    }>('/user/me/emergency-access')
    profile.value.expire_at = data.expire_at
    profile.value.emergency_access.used_count = data.used_count
    profile.value.emergency_access.max_count = data.max_count
    profile.value.emergency_access.remaining = data.remaining
    ElMessage.success(data.sync_pending ? '已延长，节点配置正在后台同步' : '已延长')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error ?? '紧急使用失败')
  } finally {
    emergencyBusy.value = false
  }
}

async function savePersonalRules() {
  rulesBusy.value = true
  try {
    const rules = personalRules.value.trim()
    await client.put('/user/me/rules', { personal_rules: rules })
    personalRules.value = rules
    personalRulesSaved.value = rules
    rulesDialog.value = false
    ElMessage.success('个人规则已保存，更新客户端订阅后生效')
  } finally {
    rulesBusy.value = false
  }
}

function resetPersonalRulesEditor() {
  personalRules.value = personalRulesSaved.value
}

function openPersonalRulesDialog() {
  personalRules.value = personalRulesSaved.value
  rulesDialog.value = true
}

function formatBytes(n: number): string {
  if (n === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v.toFixed(2)} ${units[u]}`
}

function daysUntil(iso: string): number {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000)
}

onMounted(load)
</script>

<template>
  <div v-if="profile" class="profile-dashboard">
    <!-- Header Summary -->
    <div class="profile-header">
      <div class="user-info-section">
        <div class="avatar-large">{{ displayName.charAt(0).toUpperCase() }}</div>
        <div>
          <h1 class="username">{{ displayName }}</h1>
          <div class="tags">
            <span class="tag status-tag" :class="profile.enabled ? 'active' : 'inactive'">
              {{ profile.enabled ? '运行中' : '已禁用' }}
            </span>
          </div>
        </div>
      </div>
      <div class="header-actions">
        <el-button v-if="profile.can_change_password" @click="passwordDialog = true" plain>
          <el-icon class="mr-1"><Lock /></el-icon> 修改密码
        </el-button>
      </div>
    </div>

    <div
      v-if="announcement"
      class="global-announcement"
      :class="`announcement-${announcement.level || 'info'}`"
    >
      <div class="announcement-symbol">{{ announcementSymbol(announcement.level) }}</div>
      <div class="announcement-body">
        <div v-if="announcement.title" class="announcement-title">{{ announcement.title }}</div>
        <div class="announcement-content">{{ announcement.content }}</div>
      </div>
    </div>

    <div class="dashboard-grid">
      <!-- Left Column: Stats & Usage -->
      <div class="grid-col-left">
        <!-- Usage Card -->
        <el-card class="stat-card">
          <div class="card-header-flex">
            <h3 class="card-title">流量使用情况</h3>
            <span class="reset-period">{{ profile.traffic_reset_period === 'monthly' ? '月度重置' : profile.traffic_reset_period === 'quarterly' ? '季度重置' : '不重置' }}</span>
          </div>
          
          <div v-if="usage" class="usage-stats">
            <div class="usage-numbers">
              <span class="used">{{ formatBytes(usage.period_used_bytes) }}</span>
              <span class="divider">/</span>
              <span class="limit">{{ profile.traffic_limit_bytes > 0 ? formatBytes(profile.traffic_limit_bytes) : '不限' }}</span>
            </div>
            
            <el-progress
              v-if="profile.traffic_limit_bytes > 0"
              :percentage="Math.min(100, Math.round((usage.period_used_bytes / profile.traffic_limit_bytes) * 100))"
              :stroke-width="12"
              :color="[ { color: '#67c23a', percentage: 70 }, { color: '#e6a23c', percentage: 90 }, { color: '#f56c6c', percentage: 100 } ]"
              class="usage-progress"
            />
          </div>
        </el-card>

        <el-card class="actions-card">
          <div class="card-header-flex">
            <h3 class="card-title">快捷入口</h3>
          </div>
          <div class="action-grid">
            <el-button plain @click="openPersonalRulesDialog">
              个人规则
            </el-button>
            <el-button
              v-for="link in quickLinks"
              :key="link.label + link.url"
              plain
              @click="openQuickLink(link)"
            >
              {{ link.label }}
            </el-button>
          </div>
        </el-card>

        <!-- Expiration Card -->
        <el-card class="stat-card">
          <div class="card-header-flex">
            <h3 class="card-title">账户到期时间</h3>
            <span v-if="profile.emergency_access.enabled" class="reset-period">
              紧急剩余 {{ profile.emergency_access.remaining }}/{{ profile.emergency_access.max_count }}
            </span>
          </div>
          <div class="expire-stats">
            <div v-if="profile.expire_at">
              <div class="expire-date">{{ new Date(profile.expire_at).toLocaleDateString() }}</div>
              <div class="expire-countdown" :class="daysUntil(profile.expire_at) < 7 ? 'danger' : 'safe'">
                还剩 {{ daysUntil(profile.expire_at) }} 天
              </div>
            </div>
            <div v-else class="expire-date">永久有效</div>
          </div>
          <div v-if="profile.emergency_access.enabled && profile.expire_at" class="emergency-section">
            <el-button
              type="warning"
              plain
              class="w-full"
              :disabled="!canUseEmergency"
              :loading="emergencyBusy"
              @click="useEmergencyAccess"
            >
              紧急使用，延长 {{ profile.emergency_access.duration_hours }} 小时
            </el-button>
            <p class="action-hint">
              已使用 {{ profile.emergency_access.used_count }} 次，剩余 {{ profile.emergency_access.remaining }} 次。
            </p>
          </div>
        </el-card>
      </div>

      <!-- Right Column: Subscription -->
      <div class="grid-col-right">
        <el-card class="sub-card">
          <h3 class="card-title text-center">快速导入订阅</h3>

          <div v-if="importClients.length > 0" class="client-import-section">
            <div class="section-head">
              <p class="section-label">快速导入</p>
              <el-tag size="small" type="info">{{ platformLabel }}</el-tag>
            </div>
            <div class="client-list">
              <div v-for="item in importClients" :key="item.name" class="client-row">
                <div class="client-main">
                  <div class="client-name">{{ item.name }}</div>
                  <div class="client-meta">
                    <span>{{ item.render_format }}</span>
                    <span>{{ item.platforms.join(' / ') }}</span>
                  </div>
                </div>
                <div class="client-actions">
                  <el-button type="primary" class="client-action-btn" @click="openImport(item)">导入</el-button>
                  <el-button v-if="item.install_url" plain class="client-action-btn" @click="openInstall(item)">
                    下载
                  </el-button>
                </div>
              </div>
            </div>
          </div>

          <div class="qr-toggle-row">
            <el-button plain class="qr-toggle-btn" @click="showQRCode = !showQRCode">
              {{ showQRCode ? '隐藏二维码' : '显示二维码' }}
            </el-button>
          </div>

          <Transition name="qr-fold">
            <div v-if="showQRCode" class="qr-container">
              <div class="qr-frame">
                <img v-if="qrDataURL" :src="qrDataURL" alt="QR Code" class="qr-image" />
              </div>
              <p class="qr-hint">使用客户端扫描二维码导入通用订阅</p>
            </div>
          </Transition>

          <div class="sub-url-section">
            <p class="section-label">复制订阅链接</p>
            <div class="url-box">
              <input type="text" :value="profile.sub_url" readonly class="url-input" />
              <button class="copy-btn" @click="copyText(profile.sub_url)">复制</button>
            </div>
          </div>

          <div class="sub-actions">
            <el-button type="danger" plain @click="confirmResetCredentials" class="w-full">
              重置所有凭证 (Token & UUID)
            </el-button>
            <p class="action-hint">若发生流量盗刷或链接泄露，请立即重置。</p>
          </div>
        </el-card>
      </div>

    </div>

    <el-dialog v-model="rulesDialog" title="个人规则" width="720px" top="8vh">
      <el-input
        v-model="personalRules"
        type="textarea"
        :rows="14"
        resize="vertical"
        placeholder="- DOMAIN-SUFFIX,example.com,DIRECT"
        class="rules-editor"
      />
      <p class="rules-hint">按 mihomo rules 格式填写，每行一条。保存后更新客户端订阅生效。</p>
      <template #footer>
        <el-button :disabled="!personalRulesDirty || rulesBusy" @click="resetPersonalRulesEditor">撤销</el-button>
        <el-button @click="rulesDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!personalRulesDirty" :loading="rulesBusy" @click="savePersonalRules">
          保存规则
        </el-button>
      </template>
    </el-dialog>

    <!-- Password Dialog -->
    <el-dialog v-model="passwordDialog" title="修改密码" width="400px" class="custom-dialog">
      <el-form label-position="top">
        <el-form-item label="旧密码">
          <el-input v-model="oldPassword" type="password" show-password size="large" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="newPassword" type="password" show-password size="large" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passwordDialog = false" size="large">取消</el-button>
        <el-button type="primary" @click="changePassword" size="large">确认修改</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.profile-dashboard {
  max-width: 1000px;
  margin: 0 auto;
}

/* Header */
.profile-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 32px;
  flex-wrap: wrap;
  gap: 16px;
}

.user-info-section {
  display: flex;
  align-items: center;
  gap: 20px;
}

.avatar-large {
  width: 72px;
  height: 72px;
  border-radius: 20px;
  background: var(--sidebar-active-bg);
  color: white;
  font-size: 32px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 8px 24px rgba(99, 102, 241, 0.3);
}

.username {
  font-size: 28px;
  font-weight: 700;
  margin: 0 0 8px 0;
  letter-spacing: -0.5px;
}

.tags {
  display: flex;
  gap: 8px;
}

.tag {
  font-size: 12px;
  font-weight: 600;
  padding: 4px 10px;
  border-radius: 6px;
}

.status-tag.active {
  background: rgba(16, 185, 129, 0.1);
  color: #10b981;
}

.status-tag.inactive {
  background: rgba(239, 68, 68, 0.1);
  color: #ef4444;
}

.global-announcement {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  margin-bottom: 24px;
  padding: 16px 18px;
  border-radius: 8px;
  border: 1px solid transparent;
}

.announcement-info {
  background: rgba(59, 130, 246, 0.08);
  border-color: rgba(59, 130, 246, 0.16);
  color: #2563eb;
}

.announcement-warning {
  background: rgba(245, 158, 11, 0.1);
  border-color: rgba(245, 158, 11, 0.2);
  color: #b45309;
}

.announcement-danger {
  background: rgba(239, 68, 68, 0.1);
  border-color: rgba(239, 68, 68, 0.18);
  color: #ef4444;
}

.announcement-symbol {
  width: 28px;
  height: 28px;
  border-radius: 999px;
  color: white;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-weight: 800;
  font-size: 16px;
}

.announcement-info .announcement-symbol {
  background: #3b82f6;
}

.announcement-warning .announcement-symbol {
  background: #f59e0b;
}

.announcement-danger .announcement-symbol {
  background: #ef4444;
}

.announcement-body {
  min-width: 0;
}

.announcement-title {
  font-weight: 700;
  font-size: 15px;
  margin-bottom: 4px;
}

.announcement-content {
  white-space: pre-wrap;
  line-height: 1.6;
}

/* Grid Layout */
.dashboard-grid {
  display: grid;
  grid-template-columns: 1.2fr 1fr;
  gap: 24px;
}

@media (max-width: 768px) {
  .dashboard-grid {
    grid-template-columns: 1fr;
  }
}

.grid-col-left {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.card-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-muted);
  margin: 0 0 20px 0;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.card-header-flex {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.card-header-flex .card-title {
  margin: 0;
}

.reset-period {
  font-size: 12px;
  background: rgba(99, 102, 241, 0.1);
  color: #6366f1;
  padding: 2px 8px;
  border-radius: 4px;
  font-weight: 500;
}

/* Stats Styling */
.usage-numbers {
  margin-bottom: 16px;
}

.used {
  font-size: 36px;
  font-weight: 700;
  color: var(--text-main);
  letter-spacing: -1px;
}

.divider {
  font-size: 24px;
  color: var(--text-muted);
  margin: 0 8px;
  font-weight: 300;
}

.limit {
  font-size: 20px;
  color: var(--text-muted);
  font-weight: 500;
}

.usage-progress :deep(.el-progress-bar__outer) {
  background-color: rgba(148, 163, 184, 0.1);
  border-radius: 10px;
}

.expire-date {
  font-size: 32px;
  font-weight: 700;
  color: var(--text-main);
  margin-bottom: 8px;
}

.expire-countdown {
  font-size: 14px;
  font-weight: 600;
}

.expire-countdown.safe {
  color: #10b981;
}

.expire-countdown.danger {
  color: #ef4444;
}

.emergency-section {
  margin-top: 20px;
}

/* Right Column (Subscription) */
.sub-card {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.text-center {
  text-align: center;
}

.qr-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-bottom: 18px;
}

.qr-toggle-row {
  display: flex;
  justify-content: center;
  margin-bottom: 18px;
}

.qr-toggle-btn {
  height: 36px;
  min-width: 120px;
  padding: 0 18px;
  font-weight: 600;
}

.qr-frame {
  background: white;
  padding: 10px;
  border-radius: 12px;
  margin-bottom: 12px;
  border: 1px solid rgba(226, 232, 240, 0.8);
}

.qr-image {
  width: 176px;
  height: 176px;
  border-radius: 6px;
  display: block;
}

.qr-hint {
  font-size: 13px;
  color: var(--text-muted);
  margin: 0;
}

.qr-fold-enter-active,
.qr-fold-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.qr-fold-enter-from,
.qr-fold-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.section-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
  margin: 0 0 8px 0;
}

.url-box {
  display: flex;
  background: rgba(148, 163, 184, 0.05);
  border: 1px solid var(--header-border);
  border-radius: 8px;
  overflow: hidden;
  margin-bottom: 10px;
}

.url-input {
  flex: 1;
  background: transparent;
  border: none;
  padding: 12px 16px;
  color: var(--text-main);
  font-size: 13px;
  font-family: monospace;
  outline: none;
}

.copy-btn {
  background: var(--sidebar-active-bg);
  color: white;
  border: none;
  min-width: 76px;
  padding: 0 20px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.2s;
}

.copy-btn:hover {
  opacity: 0.9;
}

.sub-url-section {
  margin-top: 0;
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.client-import-section {
  border: 1px solid var(--header-border);
  border-radius: 10px;
  padding: 14px;
  margin-bottom: 18px;
  background: rgba(148, 163, 184, 0.035);
}

.client-list {
  display: flex;
  flex-direction: column;
  border-top: 1px solid var(--header-border);
}

.client-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 12px 0;
  border-bottom: 1px solid var(--header-border);
}

.client-row:last-child {
  border-bottom: 0;
}

.client-main {
  min-width: 0;
}

.client-name {
  font-size: 14px;
  font-weight: 700;
  color: var(--text-main);
}

.client-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 4px;
  color: var(--text-muted);
  font-size: 12px;
}

.client-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.client-action-btn {
  min-width: 64px;
  height: 36px;
  padding: 0 16px;
  font-weight: 600;
}

.sub-actions {
  margin-top: auto;
}

.rules-editor :deep(textarea) {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.5;
}

.action-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.rules-hint {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}

.w-full {
  width: 100%;
}

.action-hint {
  font-size: 12px;
  color: var(--text-muted);
  text-align: center;
  margin: 8px 0 0 0;
}

.mr-1 {
  margin-right: 4px;
}

@media (max-width: 480px) {
  .client-row {
    align-items: stretch;
    flex-direction: column;
  }

  .client-actions {
    justify-content: flex-end;
  }
}

</style>
