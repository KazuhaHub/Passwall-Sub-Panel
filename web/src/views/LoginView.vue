<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { getAuthMethods, samlLoginURL, oidcLoginURL, type LoginMode } from '@/api/auth'
import { homeForRole, isAdminPath } from '@/router/home'

const router = useRouter()
const auth = useAuthStore()

const form = reactive({ upn: '', password: '' })
const loading = ref(false)
const mode = ref<LoginMode>('dual')
const appTitle = ref('')
const logoSrc = ref('/images/logo+title-circle-darkmode.png')
const logoSrcLight = ref('/images/logo+title-circle.png')
const iconUrl = ref('/images/HeadPicture.png')
const ssoEnabled = ref(false)
const samlEnabled = ref(false)
const oidcEnabled = ref(false)
const probing = ref(true)
const footerText = ref('© Passwall Sub Panel')

async function probe() {
  probing.value = true
  try {
    const m = await getAuthMethods()
    mode.value = m.login_mode
    document.title = m.site_title || m.app_title || 'Passwall'
    iconUrl.value = m.icon_url || '/images/HeadPicture.png'
    updateIcon(iconUrl.value)
    appTitle.value = m.app_title || 'Passwall'
    logoSrc.value = m.logo_url_dark || m.logo_url || '/images/logo+title-circle-darkmode.png'
    logoSrcLight.value = m.logo_url || '/images/logo+title-circle.png'
    ssoEnabled.value = m.sso
    samlEnabled.value = !!m.saml
    oidcEnabled.value = !!m.oidc
    footerText.value = m.footer_text || '© Passwall Sub Panel'
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

function updateIcon(url: string) {
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = url
}

async function submit() {
  if (!form.upn || !form.password) {
    ElMessage.warning('请输入 UPN 和密码')
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
    <div class="login-container" v-if="!probing">
      <div class="login-brand">
        <img class="logo light-logo" :src="logoSrcLight" alt="Logo" />
        <img class="logo dark-logo" :src="logoSrc" alt="Logo" />
        <div v-if="appTitle" class="login-title">{{ appTitle }}</div>
        <div class="login-subtitle">Welcome back, please sign in.</div>
      </div>

      <!-- SSO-only modes -->
      <template v-if="mode === 'sso_redirect' || mode === 'sso_first'">
        <button v-if="ssoEnabled" class="btn btn-primary" @click="ssoLogin">
          <svg class="btn-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor">
            <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"></path>
            <path d="m10 17 5-5-5-5"></path>
            <path d="M15 12H3"></path>
          </svg>
          <span>使用 SSO 登录</span>
        </button>
      </template>

      <!-- Dual mode -->
      <template v-else-if="mode === 'dual'">
        <button v-if="ssoEnabled" class="btn btn-primary" @click="ssoLogin">
          <svg class="btn-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor">
            <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"></path>
            <path d="m10 17 5-5-5-5"></path>
            <path d="M15 12H3"></path>
          </svg>
          <span>使用 SSO 登录</span>
        </button>
        <div class="divider">
          <span>OR</span>
        </div>
        <form @submit.prevent="submit" class="login-form">
          <input
            v-model="form.upn"
            type="text"
            placeholder="UPN"
            autocomplete="email"
            class="input"
          />
          <input
            v-model="form.password"
            type="password"
            placeholder="密码"
            autocomplete="current-password"
            class="input"
          />
          <button type="submit" class="btn btn-primary" :disabled="loading">
            {{ loading ? '登录中...' : '登录' }}
          </button>
        </form>
      </template>

      <!-- Local only -->
      <template v-else>
        <form @submit.prevent="submit" class="login-form">
          <input
            v-model="form.upn"
            type="text"
            placeholder="UPN"
            autocomplete="email"
            class="input"
          />
          <input
            v-model="form.password"
            type="password"
            placeholder="密码"
            autocomplete="current-password"
            class="input"
          />
          <button type="submit" class="btn btn-primary" :disabled="loading">
            {{ loading ? '登录中...' : '登录' }}
          </button>
        </form>
      </template>

      <div class="footer">{{ footerText }}</div>
    </div>
  </div>
</template>

<style scoped>
:root {
  --brand-color: #2563eb;
  --bg: #f6f7f9;
  --surface: #ffffff;
  --text: #111827;
  --text-muted: #6b7280;
  --border: #e5e7eb;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0b0f19;
    --surface: #111827;
    --text: #f3f4f6;
    --text-muted: #9ca3af;
    --border: #1f2937;
  }
}

.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: var(--bg);
  padding: 24px 16px;
}

.login-container {
  background: var(--surface);
  border-radius: 16px;
  padding: 32px 28px;
  max-width: 420px;
  width: 100%;
  box-shadow: 0 8px 32px rgba(0,0,0,0.06);
  border: 1px solid var(--border);
}

@media (prefers-color-scheme: dark) {
  .login-container {
    box-shadow: 0 8px 32px rgba(0,0,0,0.3);
  }
}

.login-brand {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-bottom: 28px;
}

.logo {
  height: 64px;
  object-fit: contain;
  margin-bottom: 16px;
}

.light-logo {
  display: block;
}

.dark-logo {
  display: none;
}

@media (prefers-color-scheme: dark) {
  .light-logo {
    display: none;
  }
  .dark-logo {
    display: block;
  }
}

.login-title {
  font-size: 20px;
  font-weight: 600;
  margin-bottom: 8px;
  color: var(--text);
}

.login-subtitle {
  font-size: 14px;
  color: var(--text-muted);
  margin: 0;
}

/* Buttons */
.btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 100%;
  padding: 12px 20px;
  border-radius: 10px;
  font-size: 15px;
  font-weight: 500;
  cursor: pointer;
  text-decoration: none;
  font-family: inherit;
  transition: opacity 0.15s, background 0.15s;
  border: 1px solid transparent;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn + .btn {
  margin-top: 10px;
}

.btn-primary {
  background: #2563eb;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.9;
}

.btn-primary:active:not(:disabled) {
  opacity: 0.8;
}

.btn-icon {
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  stroke-width: 2;
}

/* Divider */
.divider {
  display: flex;
  align-items: center;
  margin: 20px 0;
  color: var(--text-muted);
  font-size: 13px;
}

.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}

.divider span {
  padding: 0 12px;
}

/* Form inputs */
.input {
  display: block;
  width: 100%;
  padding: 12px 14px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 15px;
  font-family: inherit;
  margin-bottom: 10px;
  box-sizing: border-box;
}

.input:focus {
  outline: none;
  border-color: #2563eb;
  box-shadow: 0 0 0 3px rgba(37,99,235,0.15);
}

.input::placeholder {
  color: var(--text-muted);
}

.login-form {
  width: 100%;
}

/* Footer */
.footer {
  margin-top: 32px;
  text-align: center;
  font-size: 11px;
  color: var(--text-muted);
}
</style>
