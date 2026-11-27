import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { getCaptcha } from '@/api/auth'
import type { CaptchaProvider, LoginCaptcha } from '@/api/types'
import { IconRefresh, IconSpinner } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

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
    return <div ref={containerRef} className="flex min-h-[65px] justify-center" />
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="captcha-answer">{t('auth:captcha_label')}</Label>
      <div className="flex items-center gap-2">
        {/* White behind the image whatever the theme: the challenge is drawn for a light page. */}
        <div className="grid h-10 w-30 shrink-0 place-items-center overflow-hidden rounded-lg border bg-white">
          {loading || !image
            ? <IconSpinner aria-hidden className="size-4.5 animate-spin text-neutral-500" />
            : <img src={image} alt="captcha" className="size-full object-cover" />}
        </div>
        <IconButton label={t('auth:captcha_refresh')} size="icon-sm" onClick={() => fetchChallenge()}>
          <IconRefresh />
        </IconButton>
        <Input
          id="captcha-answer"
          value={answer}
          onChange={e => {
            const v = e.target.value
            setAnswer(v)
            onChange({ captcha_id: captchaId, captcha_answer: v })
          }}
          placeholder={t('auth:captcha_placeholder')}
          inputMode="numeric"
          autoComplete="off"
          maxLength={8}
          className="flex-1"
        />
      </div>
    </div>
  )
}
