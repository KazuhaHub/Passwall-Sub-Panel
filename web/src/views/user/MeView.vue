<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import QRCode from 'qrcode'
import { client } from '@/api/client'
import { getMyUsage, type UsageReport } from '@/api/traffic'

interface MeProfile {
  id: number
  username: string
  upn?: string
  source: string
  sub_url: string
  expire_at?: string | null
  traffic_limit_bytes: number
  traffic_reset_period: string
  enabled: boolean
}

const profile = ref<MeProfile | null>(null)
const usage = ref<UsageReport | null>(null)
const qrDataURL = ref<string>('')
const passwordDialog = ref(false)
const oldPassword = ref('')
const newPassword = ref('')

async function load() {
  const [p, u] = await Promise.all([
    client.get<MeProfile>('/user/me').then((r) => r.data),
    getMyUsage().catch(() => null),
  ])
  profile.value = p
  usage.value = u
  if (p.sub_url) {
    qrDataURL.value = await QRCode.toDataURL(p.sub_url, { width: 220 })
  }
}

function copyText(s: string) {
  navigator.clipboard.writeText(s)
  ElMessage.success('已复制')
}

async function resetSubToken() {
  const { data } = await client.post<{ sub_token: string; sub_url: string }>(
    '/user/me/reset-sub-token',
  )
  if (profile.value) profile.value.sub_url = data.sub_url
  qrDataURL.value = await QRCode.toDataURL(data.sub_url, { width: 220 })
  ElMessage.success('已重置，旧 URL 立即失效')
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

function formatBytes(n: number): string {
  if (n === 0) return '0'
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
  <div v-if="profile" class="psp-page" style="max-width: 720px; margin: 0 auto">
    <el-card>
      <div style="display: flex; gap: 24px">
        <div style="flex: 1">
          <h2 style="margin-top: 0">{{ profile.username }}</h2>
          <p style="color: #909399; font-size: 13px">
            来源：{{ profile.source === 'sso' ? 'SSO' : '本地账号' }}
          </p>

          <div v-if="profile.expire_at" style="margin: 16px 0">
            <div style="color: #909399; font-size: 13px">到期</div>
            <div style="font-size: 18px; font-weight: 600">
              {{ new Date(profile.expire_at).toLocaleDateString() }}
              <span
                :style="{
                  color: daysUntil(profile.expire_at) < 7 ? '#f56c6c' : '#67c23a',
                  fontSize: '14px',
                  marginLeft: '8px',
                }"
              >
                还剩 {{ daysUntil(profile.expire_at) }} 天
              </span>
            </div>
          </div>
          <div v-else style="margin: 16px 0">
            <div style="color: #909399; font-size: 13px">到期</div>
            <div style="font-size: 18px; font-weight: 600">永久</div>
          </div>

          <div v-if="usage" style="margin: 16px 0">
            <div style="color: #909399; font-size: 13px">本周期已用 / 限额</div>
            <div style="font-size: 18px; font-weight: 600">
              {{ formatBytes(usage.period_used_bytes) }} /
              {{
                profile.traffic_limit_bytes > 0
                  ? formatBytes(profile.traffic_limit_bytes)
                  : '不限'
              }}
            </div>
            <el-progress
              v-if="profile.traffic_limit_bytes > 0"
              :percentage="
                Math.min(
                  100,
                  Math.round((usage.period_used_bytes / profile.traffic_limit_bytes) * 100),
                )
              "
              style="margin-top: 6px"
            />
          </div>
        </div>

        <div style="text-align: center">
          <img v-if="qrDataURL" :src="qrDataURL" alt="QR" />
          <div style="font-size: 12px; color: #909399; margin-top: 8px">
            扫码导入订阅
          </div>
        </div>
      </div>

      <el-divider />

      <div style="margin-bottom: 8px; color: #909399; font-size: 13px">订阅 URL</div>
      <el-input :model-value="profile.sub_url" readonly>
        <template #append>
          <el-button @click="copyText(profile.sub_url)">复制</el-button>
        </template>
      </el-input>

      <div style="margin-top: 24px; display: flex; gap: 12px">
        <el-button @click="resetSubToken">重置订阅 URL</el-button>
        <el-button v-if="profile.source === 'local'" @click="passwordDialog = true">
          修改密码
        </el-button>
      </div>
    </el-card>

    <el-dialog v-model="passwordDialog" title="修改密码" width="400px">
      <el-form label-width="80px">
        <el-form-item label="旧密码">
          <el-input v-model="oldPassword" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="newPassword" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passwordDialog = false">取消</el-button>
        <el-button type="primary" @click="changePassword">提交</el-button>
      </template>
    </el-dialog>
  </div>
</template>
