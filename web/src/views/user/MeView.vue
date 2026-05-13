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

const profile = ref<MeProfile | null>(null)
const displayName = computed(() => profile.value?.display_name || profile.value?.upn || '')
const usage = ref<UsageReport | null>(null)
const qrDataURL = ref<string>('')
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
            <h3 class="card-title">操作区</h3>
          </div>
          <div class="action-grid">
            <el-button plain @click="openPersonalRulesDialog">
              个人规则
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
          
          <div class="qr-container">
            <div class="qr-frame">
              <img v-if="qrDataURL" :src="qrDataURL" alt="QR Code" class="qr-image" />
            </div>
            <p class="qr-hint">使用任意支持的客户端扫描二维码</p>
          </div>

          <div class="sub-url-section">
            <p class="section-label">或复制订阅链接：</p>
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
  margin-bottom: 32px;
}

.qr-frame {
  background: white;
  padding: 12px;
  border-radius: 16px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.06);
  margin-bottom: 12px;
  border: 1px solid rgba(226, 232, 240, 0.8);
}

.qr-image {
  border-radius: 8px;
  display: block;
}

.qr-hint {
  font-size: 13px;
  color: var(--text-muted);
  margin: 0;
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
  margin-bottom: 24px;
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
  padding: 0 20px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.2s;
}

.copy-btn:hover {
  opacity: 0.9;
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

</style>
