<template>
  <div class="sso-error">
    <el-result
      :title="title"
      :sub-title="subTitle"
    >
      <template #icon>
        <LineIcon :name="icon" :size="64" />
      </template>
      <template #extra>
        <el-button type="primary" @click="router.replace('/login')">返回登录</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import LineIcon from '@/components/LineIcon.vue'

const route = useRoute()
const router = useRouter()

const error = computed(() => (route.query.error as string) || '')
const description = computed(() => (route.query.description as string) || '')

const icon = computed(() => {
  if (error.value === 'account_disabled') return 'warning'
  return 'close'
})

const title = computed(() => {
  switch (error.value) {
    case 'auth_failed':
      return '认证失败'
    case 'account_disabled':
      return '账号已停用'
    case 'account_pending':
      return '账号待审核'
    case 'sso_error':
      return 'SSO 错误'
    default:
      return '登录失败'
  }
})

const subTitle = computed(() => {
  if (description.value) return description.value
  switch (error.value) {
    case 'auth_failed':
      return 'SSO 认证失败，请重试或联系管理员。'
    case 'account_disabled':
      return '您的账号已被停用，请联系管理员。'
    case 'account_pending':
      return '您的账号正在等待管理员审核，请稍后再试。'
    case 'sso_error':
      return 'SSO 登录过程中出现错误，请重试或联系管理员。'
    default:
      return '登录过程中出现错误，请重试或联系管理员。'
  }
})
</script>

<style scoped>
.sso-error {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
}
</style>
