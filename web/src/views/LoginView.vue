<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { getAuthMethods, samlLoginURL, oidcLoginURL, type LoginMode } from '@/api/auth'
import { homeForRole, isAdminPath } from '@/router/home'

const router = useRouter()
const auth = useAuthStore()

const form = reactive({ username: '', password: '' })
const loading = ref(false)
const mode = ref<LoginMode>('dual')
const siteTitle = ref('')
const logoSrc = ref('/images/logo+title-circle-darkmode.png')
const ssoEnabled = ref(false)
const samlEnabled = ref(false)
const oidcEnabled = ref(false)
const probing = ref(true)

async function probe() {
  probing.value = true
  try {
    const m = await getAuthMethods()
    mode.value = m.login_mode
    siteTitle.value = m.site_title || ''
    logoSrc.value = m.logo_url_dark || m.logo_url || '/images/logo+title-circle-darkmode.png'
    ssoEnabled.value = m.sso
    samlEnabled.value = !!m.saml
    oidcEnabled.value = !!m.oidc
    if (m.login_mode === 'sso_redirect' && m.sso) {
      ssoLogin()
      return
    }
    // If only local is possible and we're not already showing it, send the
    // visitor to the dedicated local page for a cleaner UX.
    if (m.login_mode === 'local_only') {
      const q = router.currentRoute.value.query
      router.replace({ path: '/login/local', query: q })
    }
  } finally {
    probing.value = false
  }
}

async function submit() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.username, form.password)
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

function samlLogin() {
  const returnTo = (router.currentRoute.value.query.return_to as string) || '/user/me'
  location.href = samlLoginURL(returnTo)
}

function oidcLogin() {
  const returnTo = (router.currentRoute.value.query.return_to as string) || '/user/me'
  location.href = oidcLoginURL(returnTo)
}

// Picks the single SSO action when only one provider is enabled; if both
// are enabled the template renders both buttons explicitly.
function ssoLogin() {
  if (samlEnabled.value) samlLogin()
  else if (oidcEnabled.value) oidcLogin()
}

onMounted(probe)
</script>

<template>
  <div class="login-page">
    <div class="bg-shape shape-1"></div>
    <div class="bg-shape shape-2"></div>

    <div class="login-container" v-if="!probing">
      <div class="login-brand">
        <img class="logo" :src="logoSrc" alt="Logo" />
        <div v-if="siteTitle" class="login-title">{{ siteTitle }}</div>
        <div class="login-subtitle">Welcome back, please sign in.</div>
      </div>

      <!-- SSO-only modes: direct redirect is handled in probe(); this button
           view remains as the fallback and for sso_first. -->
      <template v-if="mode === 'sso_redirect' || mode === 'sso_first'">
        <el-button v-if="samlEnabled" class="primary-btn" size="large" @click="samlLogin">
          <el-icon class="mr-2"><Right /></el-icon>
          使用 SAML 登录
        </el-button>
        <el-button v-if="oidcEnabled" class="primary-btn" size="large"
          :style="samlEnabled ? 'margin-top:12px' : ''" @click="oidcLogin">
          <el-icon class="mr-2"><Right /></el-icon>
          使用 OIDC 登录
        </el-button>
      </template>

      <!-- Dual: SSO button(s) on top, local form below -->
      <template v-else-if="mode === 'dual'">
        <el-button v-if="samlEnabled" class="primary-btn" size="large" @click="samlLogin">
          <el-icon class="mr-2"><Right /></el-icon>
          使用 SAML 登录
        </el-button>
        <el-button v-if="oidcEnabled" class="primary-btn" size="large"
          :style="samlEnabled ? 'margin-top:12px' : ''" @click="oidcLogin">
          <el-icon class="mr-2"><Right /></el-icon>
          使用 OIDC 登录
        </el-button>
        <el-divider class="login-divider">OR</el-divider>
        <el-form @submit.prevent="submit" class="login-form">
          <el-form-item>
            <el-input
              v-model="form.username"
              placeholder="用户名"
              autocomplete="username"
              size="large"
              prefix-icon="User"
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="form.password"
              type="password"
              show-password
              placeholder="密码"
              autocomplete="current-password"
              size="large"
              prefix-icon="Lock"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              :loading="loading"
              class="secondary-btn"
              size="large"
              @click="submit"
            >
              登录
            </el-button>
          </el-form-item>
        </el-form>
      </template>

      <!-- local_only is handled by redirect in probe(); render local form as a fallback. -->
      <template v-else>
        <el-form @submit.prevent="submit" class="login-form">
          <el-form-item>
            <el-input
              v-model="form.username"
              placeholder="用户名"
              autocomplete="username"
              size="large"
              prefix-icon="User"
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="form.password"
              type="password"
              show-password
              placeholder="密码"
              autocomplete="current-password"
              size="large"
              prefix-icon="Lock"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              :loading="loading"
              class="primary-btn"
              size="large"
              @click="submit"
            >
              登录
            </el-button>
          </el-form-item>
        </el-form>
      </template>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background-color: #0f172a;
  overflow: hidden;
}

.bg-shape {
  position: absolute;
  border-radius: 50%;
  filter: blur(80px);
  z-index: 0;
  opacity: 0.6;
}

.shape-1 {
  width: 500px;
  height: 500px;
  background: linear-gradient(135deg, #6366f1, #8b5cf6);
  top: -100px;
  left: -100px;
  animation: float 10s ease-in-out infinite alternate;
}

.shape-2 {
  width: 400px;
  height: 400px;
  background: linear-gradient(135deg, #3b82f6, #2dd4bf);
  bottom: -50px;
  right: -50px;
  animation: float 12s ease-in-out infinite alternate-reverse;
}

@keyframes float {
  0% {
    transform: translateY(0) scale(1);
  }
  100% {
    transform: translateY(30px) scale(1.05);
  }
}

.login-container {
  position: relative;
  z-index: 1;
  width: 420px;
  padding: 40px;
  background: rgba(255, 255, 255, 0.05);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 24px;
  box-shadow: 0 24px 40px rgba(0, 0, 0, 0.2);
  color: white;
}

.login-brand {
  text-align: center;
  margin-bottom: 28px;
}

.logo {
  height: 64px;
  object-fit: contain;
  margin-bottom: 16px;
}

.login-title {
  font-size: 24px;
  font-weight: 700;
  letter-spacing: 1px;
  margin-bottom: 8px;
}

.login-subtitle {
  font-size: 14px;
  color: #94a3b8;
}

.primary-btn {
  width: 100%;
  border-radius: 12px;
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
  border: none;
  color: #fff;
  font-weight: 600;
  transition: transform 0.2s, box-shadow 0.2s;
}

.primary-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 20px rgba(99, 102, 241, 0.4);
  color: #fff;
}

.secondary-btn {
  width: 100%;
  border-radius: 12px;
}

.secondary-link {
  display: block;
  text-align: center;
  margin-top: 18px;
  font-size: 13px;
  color: rgba(255, 255, 255, 0.7);
}

.secondary-link:hover {
  color: #fff;
}

.login-divider :deep(.el-divider__text) {
  background: transparent;
  color: #64748b;
}

.login-divider :deep(.el-divider) {
  border-color: rgba(255, 255, 255, 0.1);
}

.login-form :deep(.el-input__wrapper) {
  background: rgba(255, 255, 255, 0.05);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.1) inset;
  border-radius: 12px;
}

.login-form :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 1px #6366f1 inset !important;
  background: rgba(255, 255, 255, 0.1);
}

.login-form :deep(.el-input__inner) {
  color: #f8fafc;
}

.login-form :deep(.el-input__inner::placeholder) {
  color: #64748b;
}

.mr-2 {
  margin-right: 6px;
}
</style>
