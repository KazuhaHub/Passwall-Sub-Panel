<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import LineIcon from '@/components/LineIcon.vue'
import { useAuthStore } from '@/stores/auth'
import { getAuthMethods } from '@/api/auth'
import { useTheme } from '@/composables/useTheme'
import { homeForRole, isAdminPath } from '@/router/home'

const { isDark } = useTheme()

const appTitle = ref('')
const logoLight = ref('/images/logo+title-circle.png')
const logoDark = ref('/images/logo+title-circle-darkmode.png')
const iconUrl = ref('/images/HeadPicture.png')

const router = useRouter()
const auth = useAuthStore()

const form = reactive({ upn: '', password: '' })
const loading = ref(false)

async function submit() {
  if (!form.upn || !form.password) {
    ElMessage.warning('请输入账号和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.upn, form.password)
    const fallback = homeForRole(auth.role)
    const requested = (router.currentRoute.value.query.return_to as string) || fallback
    const returnTo = isAdminPath(requested) && !auth.isAdmin ? fallback : requested
    router.push(returnTo)
  } catch {
    /* error toast handled by axios interceptor */
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  try {
    const m = await getAuthMethods()
    document.title = m.site_title || m.app_title || 'Passwall'
    iconUrl.value = m.icon_url || '/images/HeadPicture.png'
    updateIcon(iconUrl.value)
    appTitle.value = m.app_title || 'Passwall'
    logoLight.value = m.logo_url || '/images/logo+title-circle.png'
    logoDark.value = m.logo_url_dark || m.logo_url || '/images/logo+title-circle-darkmode.png'
  } catch { /* ignore */ }
})

function updateIcon(url: string) {
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = url
}
</script>

<template>
  <div class="login-page">
    <el-card class="local-card">
      <div class="header">
        <img class="logo" :src="isDark ? logoDark : logoLight" alt="Logo" />
        <div v-if="appTitle" class="site-title">{{ appTitle }}</div>
        <div class="title">账号登录</div>
        <div class="subtitle">使用管理员分配的账号和密码</div>
      </div>
      <el-form @submit.prevent="submit" label-position="top">
        <el-form-item>
          <el-input
            v-model="form.upn"
            placeholder="邮箱或用户名"
            autocomplete="email"
            size="large"
          >
            <template #prefix><LineIcon name="user" :size="16" /></template>
          </el-input>
        </el-form-item>
        <el-form-item>
          <el-input
            v-model="form.password"
            type="password"
            show-password
            placeholder="密码"
            autocomplete="current-password"
            size="large"
          >
            <template #prefix><LineIcon name="lock" :size="16" /></template>
          </el-input>
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :loading="loading"
            size="large"
            style="width: 100%"
            @click="submit"
          >
            登录
          </el-button>
        </el-form-item>
        <el-form-item>
          <router-link to="/login" class="back-link">← 返回</router-link>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: #eef2f7;
  padding: 20px;
}

.local-card {
  width: 400px;
  border-radius: 14px;
  border: 1px solid #d5dce8;
  background: var(--card-bg);
  box-shadow: 0 22px 60px rgba(15, 23, 42, 0.14);
}

.local-card :deep(.el-card__body) {
  padding: 34px 30px;
}

.header {
  text-align: center;
  margin-bottom: 24px;
}

.title {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-main);
}

.subtitle {
  font-size: 13px;
  color: var(--text-muted);
  margin-top: 6px;
}

.logo {
  height: 56px;
  object-fit: contain;
  margin-bottom: 12px;
}

.site-title {
  font-size: 20px;
  font-weight: 700;
  color: var(--text-main);
  margin-bottom: 12px;
}

.back-link {
  display: block;
  width: 100%;
  text-align: center;
  font-size: 13px;
  color: var(--text-muted);
}

.back-link:hover {
  color: #6366f1;
}

.local-card :deep(.el-input__wrapper) {
  min-height: 48px;
  border-radius: 8px;
  border: 1px solid #cbd5e1;
  box-shadow: inset 0 1px 2px rgba(15, 23, 42, 0.04);
}

.local-card :deep(.el-input__wrapper.is-focus) {
  border-color: #2563eb;
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.15);
}

@media (prefers-color-scheme: dark) {
  .login-page {
    background: #0b1220;
  }

  .local-card {
    border-color: #263244;
    box-shadow: 0 22px 60px rgba(0, 0, 0, 0.4);
  }

  .local-card :deep(.el-input__wrapper) {
    border-color: #334155;
  }
}
</style>
