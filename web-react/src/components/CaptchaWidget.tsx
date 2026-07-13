import { useCallback, useEffect, useRef, useState } from 'react'
import { Box, CircularProgress, IconButton, TextField, Typography, useTheme } from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useTranslation } from 'react-i18next'

import { getCaptcha } from '@/api/auth'
import type { CaptchaProvider, LoginCaptcha } from '@/api/types'

interface CaptchaWidgetProps {
  provider: CaptchaProvider
  siteKey?: string
  // Bumped by the parent (e.g. after a failed login) to force a fresh challenge.
  refreshKey?: number
  onChange: (c: LoginCaptcha) => void
}

// Token-provider integration table. All three expose the same explicit-render
// shape — render(container, {sitekey, callback}) → widget id, reset(id) — so one
// generic loader drives them.
const TOKEN_PROVIDERS: Record<string, { src: string; global: string }> = {
  turnstile: { src: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit', global: 'turnstile' },
  hcaptcha: { src: 'https://js.hcaptcha.com/1/api.js?render=explicit', global: 'hcaptcha' },
  recaptcha: { src: 'https://www.google.com/recaptcha/api.js?render=explicit', global: 'grecaptcha' },
}

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) {
      resolve()
      return
    }
    const el = document.createElement('script')
    el.src = src
    el.async = true
    el.defer = true
    el.onload = () => resolve()
    el.onerror = () => reject(new Error(`failed to load ${src}`))
    document.head.appendChild(el)
  })
}

export default function CaptchaWidget({ provider, siteKey, refreshKey = 0, onChange }: CaptchaWidgetProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth'])

  // ----- image provider -----
  const [image, setImage] = useState('')
  const [captchaId, setCaptchaId] = useState('')
  const [answer, setAnswer] = useState('')
  const [loading, setLoading] = useState(false)

  const fetchChallenge = useCallback(() => {
    setLoading(true)
    setAnswer('')
    onChange({})
    let cancelled = false
    getCaptcha()
      .then(ch => {
        if (cancelled) return
        setImage(ch.image ?? '')
        setCaptchaId(ch.captcha_id ?? '')
      })
      .catch(() => { /* leave blank; submit will surface the failure */ })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // ----- token providers -----
  const containerRef = useRef<HTMLDivElement | null>(null)
  const widgetIdRef = useRef<unknown>(null)

  useEffect(() => {
    if (provider !== 'image') return
    return fetchChallenge()
  }, [provider, refreshKey, fetchChallenge])

  useEffect(() => {
    if (provider === 'image' || !siteKey) return
    const cfg = TOKEN_PROVIDERS[provider]
    if (!cfg) return
    let cancelled = false
    loadScript(cfg.src).then(() => {
      if (cancelled || !containerRef.current) return
      const api = (window as unknown as Record<string, any>)[cfg.global]
      if (!api?.render) return
      if (widgetIdRef.current != null && api.reset) {
        try { api.reset(widgetIdRef.current) } catch { /* ignore */ }
      }
      containerRef.current.innerHTML = ''
      widgetIdRef.current = api.render(containerRef.current, {
        sitekey: siteKey,
        callback: (token: string) => onChange({ captcha_token: token }),
        'expired-callback': () => onChange({}),
        'error-callback': () => onChange({}),
      })
    }).catch(() => { /* network blocked — nothing to render */ })
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [provider, siteKey, refreshKey])

  if (provider !== 'image') {
    return <Box ref={containerRef} sx={{ display: 'flex', justifyContent: 'center', minHeight: 65 }} />
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('auth:captcha_label')}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box sx={{
          width: 120, height: 40, borderRadius: 1, overflow: 'hidden',
          display: 'grid', placeItems: 'center',
          border: `1px solid ${md.outlineVariant}`, bgcolor: '#fff',
        }}>
          {loading || !image
            ? <CircularProgress size={18} />
            : <Box component="img" src={image} alt="captcha" sx={{ width: '100%', height: '100%', objectFit: 'cover' }} />}
        </Box>
        <IconButton size="small" aria-label={t('auth:captcha_refresh')} onClick={() => fetchChallenge()}>
          <RefreshIcon fontSize="small" />
        </IconButton>
        <TextField
          size="small"
          value={answer}
          onChange={e => {
            const v = e.target.value
            setAnswer(v)
            onChange({ captcha_id: captchaId, captcha_answer: v })
          }}
          placeholder={t('auth:captcha_placeholder')}
          sx={{ flex: 1 }}
          slotProps={{
            htmlInput: { inputMode: 'numeric', autoComplete: 'off', maxLength: 8 }
          }}
        />
      </Box>
    </Box>
  );
}
